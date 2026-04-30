package session

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nijaru/canto/llm"
)

const defaultRebuilderFilesLimit = 5

// Rebuilder standardizes how model-visible history is reconstructed after a
// durable compaction or projection snapshot. It keeps the append-only event
// log untouched and materializes a canonical prompt view from snapshot state
// plus later events.
type Rebuilder struct {
	FilesLimit int
}

// NewRebuilder creates a Rebuilder with default limits.
func NewRebuilder() *Rebuilder {
	return &Rebuilder{FilesLimit: defaultRebuilderFilesLimit}
}

// RebuildEntries returns the canonical model-visible history after compaction.
func (r *Rebuilder) RebuildEntries(sess *Session) ([]HistoryEntry, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return r.rebuildEntriesLocked(sess)
}

// RebuildMessages returns the rebuilt model-visible history as plain messages.
func (r *Rebuilder) RebuildMessages(sess *Session) ([]llm.Message, error) {
	entries, err := r.RebuildEntries(sess)
	if err != nil {
		return nil, err
	}
	messages := make([]llm.Message, 0, len(entries))
	for _, entry := range entries {
		messages = append(messages, entry.Message)
	}
	return messages, nil
}

func (r *Rebuilder) rebuildEntriesLocked(sess *Session) ([]HistoryEntry, error) {
	snapshot, ok, err := sess.latestDurableSnapshot()
	if err != nil {
		return nil, err
	}
	if !ok {
		entries, err := sess.rawEntriesLocked()
		if err != nil {
			return nil, err
		}
		return withToolHistory(
			arrangePromptEntries(normalizeEffectiveEntries(entries)),
			sess.events,
		)
	}

	entries := normalizeEffectiveEntries(slices.Clone(snapshot.entries()))
	if fileEntry, ok := r.fileContextEntry(snapshot); ok {
		entries = insertAfterDurableContextEntries(entries, fileEntry)
	}

	cutoffSeen := false
	for i := range sess.events {
		e := &sess.events[i]
		if !cutoffSeen {
			if e.ID.String() == snapshot.CutoffEventID {
				cutoffSeen = true
			}
			continue
		}
		if e.Type != MessageAdded && e.Type != ContextAdded {
			continue
		}

		entry, err := sess.historyEntryFromEvent(e)
		if err != nil {
			return nil, fmt.Errorf("effective history: decode message %s: %w", e.ID, err)
		}
		var ok bool
		entry, ok = normalizeEffectiveEntry(entry)
		if !ok {
			continue
		}
		entries = append(entries, entry)
	}

	if !cutoffSeen {
		return nil, fmt.Errorf(
			"effective history: compaction cutoff %q not found",
			snapshot.CutoffEventID,
		)
	}
	return withToolHistory(arrangePromptEntries(entries), sess.events)
}

func (r *Rebuilder) fileContextEntry(snapshot CompactionSnapshot) (HistoryEntry, bool) {
	limit := r.FilesLimit
	if limit <= 0 {
		limit = defaultRebuilderFilesLimit
	}

	modified := uniqueSorted(snapshot.ModifiedFiles)
	readOnly := subtract(uniqueSorted(snapshot.ReadFiles), modified)
	if len(modified) == 0 && len(readOnly) == 0 {
		return HistoryEntry{}, false
	}

	var sb strings.Builder
	sb.WriteString("<working_set>\n")
	if len(modified) > 0 {
		sb.WriteString("Modified files:\n")
		for _, path := range modified[:min(limit, len(modified))] {
			sb.WriteString("- ")
			sb.WriteString(path)
			sb.WriteByte('\n')
		}
	}
	if len(readOnly) > 0 {
		sb.WriteString("Read-only files:\n")
		for _, path := range readOnly[:min(limit, len(readOnly))] {
			sb.WriteString("- ")
			sb.WriteString(path)
			sb.WriteByte('\n')
		}
	}
	sb.WriteString("</working_set>")

	return HistoryEntry{
		EventType:        ContextAdded,
		ContextKind:      ContextKindWorkingSet,
		ContextPlacement: ContextPlacementPrefix,
		Message: contextEntryMessage(ContextEntry{
			Kind:      ContextKindWorkingSet,
			Placement: ContextPlacementPrefix,
			Content:   sb.String(),
		}),
	}, true
}

func insertAfterDurableContextEntries(entries []HistoryEntry, extra HistoryEntry) []HistoryEntry {
	idx := 0
	for idx < len(entries) && isDurableContextEntry(entries[idx]) {
		idx++
	}
	res := make([]HistoryEntry, 0, len(entries)+1)
	res = append(res, entries[:idx]...)
	res = append(res, extra)
	res = append(res, entries[idx:]...)
	return res
}

func uniqueSorted(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := append([]string(nil), items...)
	slices.Sort(out)
	out = slices.Compact(out)
	return out
}

func normalizeEffectiveEntries(entries []HistoryEntry) []HistoryEntry {
	out := entries[:0]
	pending := make(map[string]int)
	for _, entry := range entries {
		entry, ok := normalizeEffectiveEntry(entry)
		if !ok {
			continue
		}
		msg := entry.Message
		if msg.Role == llm.RoleTool {
			if msg.ToolID == "" || pending[msg.ToolID] == 0 {
				continue
			}
			pending[msg.ToolID]--
			if pending[msg.ToolID] == 0 {
				delete(pending, msg.ToolID)
			}
			out = append(out, entry)
			continue
		}
		clear(pending)
		out = append(out, entry)
		if msg.Role == llm.RoleAssistant {
			addPendingToolCalls(pending, msg.Calls)
		}
	}
	return out
}

func normalizeEffectiveEntry(entry HistoryEntry) (HistoryEntry, bool) {
	entry.Message = normalizeTranscriptMessage(entry.Message)
	if entry.EventType == "" {
		inferLegacyContextMarkers(&entry)
	}
	if entry.EventType == ContextAdded && entry.ContextKind == "" {
		entry.ContextKind = ContextKindGeneric
	}
	if entry.EventType == ContextAdded && entry.ContextPlacement == "" {
		contextEntry := ContextEntry{Kind: entry.ContextKind}
		normalizeContextEntry(&contextEntry)
		entry.ContextPlacement = contextEntry.Placement
	}
	if entry.EventType != ContextAdded && !validModelMessage(entry.Message) {
		return HistoryEntry{}, false
	}
	return entry, true
}

type toolLifecycle struct {
	started      ToolStartedData
	completed    ToolCompletedData
	hasStarted   bool
	hasCompleted bool
}

func withToolHistory(entries []HistoryEntry, events []Event) ([]HistoryEntry, error) {
	lifecycle := make(map[string]toolLifecycle)
	for i := range events {
		e := &events[i]
		switch e.Type {
		case ToolStarted:
			data, ok, err := e.ToolStartedData()
			if err != nil {
				return nil, err
			}
			if !ok || data.ID == "" {
				continue
			}
			record := lifecycle[data.ID]
			record.started = data
			record.hasStarted = true
			lifecycle[data.ID] = record
		case ToolCompleted:
			data, ok, err := e.ToolCompletedData()
			if err != nil {
				return nil, err
			}
			if !ok || data.ID == "" {
				continue
			}
			record := lifecycle[data.ID]
			record.completed = data
			record.hasCompleted = true
			lifecycle[data.ID] = record
		}
	}
	if len(lifecycle) == 0 {
		return entries, nil
	}

	for i := range entries {
		if entries[i].Message.Role != llm.RoleTool || entries[i].Message.ToolID == "" {
			continue
		}
		record, ok := lifecycle[entries[i].Message.ToolID]
		if !ok && entries[i].Tool == nil {
			continue
		}
		tool := mergeToolHistory(entries[i].Tool, entries[i].Message, record)
		entries[i].Tool = &tool
	}
	return entries, nil
}

func mergeToolHistory(existing *ToolHistory, msg llm.Message, record toolLifecycle) ToolHistory {
	var tool ToolHistory
	if existing != nil {
		tool = *existing
	}
	if tool.ID == "" {
		tool.ID = msg.ToolID
	}
	if tool.Name == "" {
		tool.Name = msg.Name
	}
	if record.hasStarted {
		if tool.Name == "" {
			tool.Name = record.started.Tool
		}
		if tool.Arguments == "" {
			tool.Arguments = record.started.Arguments
		}
		if tool.IdempotencyKey == "" {
			tool.IdempotencyKey = record.started.IdempotencyKey
		}
	}
	if record.hasCompleted {
		if tool.Name == "" {
			tool.Name = record.completed.Tool
		}
		if tool.IdempotencyKey == "" {
			tool.IdempotencyKey = record.completed.IdempotencyKey
		}
		if record.completed.Error != "" {
			tool.IsError = true
			if tool.Error == "" {
				tool.Error = record.completed.Error
			}
		}
	}
	return tool
}

func validModelMessage(msg llm.Message) bool {
	if msg.Role != llm.RoleAssistant {
		return true
	}
	return strings.TrimSpace(msg.Content) != "" ||
		strings.TrimSpace(msg.Reasoning) != "" ||
		len(msg.ThinkingBlocks) > 0 ||
		len(msg.Calls) > 0
}

func pendingToolCalls(events []Event) (map[string]int, error) {
	pending := make(map[string]int)
	for i := range events {
		e := &events[i]
		if e.Type != MessageAdded {
			continue
		}
		msg, err := e.ensureMessage()
		if err != nil {
			return nil, err
		}
		switch msg.Role {
		case llm.RoleAssistant:
			clear(pending)
			addPendingToolCalls(pending, msg.Calls)
		case llm.RoleTool:
			if msg.ToolID == "" || pending[msg.ToolID] == 0 {
				continue
			}
			pending[msg.ToolID]--
			if pending[msg.ToolID] == 0 {
				delete(pending, msg.ToolID)
			}
		default:
			clear(pending)
		}
	}
	return pending, nil
}

func addPendingToolCalls(pending map[string]int, calls []llm.Call) {
	for _, call := range calls {
		if call.ID == "" {
			continue
		}
		pending[call.ID]++
	}
}

func inferLegacyContextMarkers(entry *HistoryEntry) {
	switch {
	case strings.Contains(entry.Message.Content, "<conversation_summary>"):
		entry.EventType = ContextAdded
		entry.ContextKind = ContextKindSummary
		entry.ContextPlacement = ContextPlacementPrefix
	case strings.Contains(entry.Message.Content, "<working_set>"):
		entry.EventType = ContextAdded
		entry.ContextKind = ContextKindWorkingSet
		entry.ContextPlacement = ContextPlacementPrefix
	}
}

func arrangePromptEntries(entries []HistoryEntry) []HistoryEntry {
	prefixCount := 0
	for _, entry := range entries {
		if isPrefixContextEntry(entry) {
			prefixCount++
		}
	}
	if prefixCount == 0 {
		return entries
	}

	out := make([]HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		if isPrefixContextEntry(entry) {
			out = append(out, entry)
		}
	}
	for _, entry := range entries {
		if !isPrefixContextEntry(entry) {
			out = append(out, entry)
		}
	}
	return out
}

func normalizeTranscriptMessage(msg llm.Message) llm.Message {
	if msg.Role != llm.RoleSystem && msg.Role != llm.RoleDeveloper {
		return msg
	}
	msg.Role = llm.RoleUser
	msg.CacheControl = nil
	return msg
}

func isDurableContextEntry(entry HistoryEntry) bool {
	if isPrefixContextEntry(entry) {
		return true
	}

	// Older snapshots did not persist HistoryEntry.EventType. Keep recognizing
	// the built-in context blocks so those snapshots rebuild in the same order.
	content := entry.Message.Content
	return strings.Contains(content, "<conversation_summary>") ||
		strings.Contains(content, "<working_set>")
}

func isPrefixContextEntry(entry HistoryEntry) bool {
	return entry.EventType == ContextAdded && entry.ContextPlacement == ContextPlacementPrefix
}

func subtract(items, remove []string) []string {
	if len(items) == 0 {
		return nil
	}
	if len(remove) == 0 {
		return items
	}
	removeSet := make(map[string]struct{}, len(remove))
	for _, item := range remove {
		removeSet[item] = struct{}{}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := removeSet[item]; ok {
			continue
		}
		out = append(out, item)
	}
	return out
}
