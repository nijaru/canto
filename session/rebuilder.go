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
		return sess.rawEntriesLocked()
	}

	entries := normalizeTranscriptEntries(slices.Clone(snapshot.entries()))
	if fileEntry, ok := r.fileContextEntry(snapshot); ok {
		entries = insertAfterDurableContextEntries(entries, fileEntry)
	}

	cutoffSeen := false
	for i := range sess.events {
		e := &sess.events[i]
		if e.Type != MessageAdded && e.Type != ContextAdded {
			continue
		}
		if !cutoffSeen {
			if e.ID.String() == snapshot.CutoffEventID {
				cutoffSeen = true
			}
			continue
		}

		entry, err := sess.historyEntryFromEvent(e)
		if err != nil {
			return nil, fmt.Errorf("effective history: decode message %s: %w", e.ID, err)
		}
		entries = append(entries, entry)
	}

	if !cutoffSeen {
		return nil, fmt.Errorf(
			"effective history: compaction cutoff %q not found",
			snapshot.CutoffEventID,
		)
	}
	return entries, nil
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
		EventType:   ContextAdded,
		ContextKind: ContextKindWorkingSet,
		Message: contextEntryMessage(ContextEntry{
			Kind:    ContextKindWorkingSet,
			Content: sb.String(),
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

func normalizeTranscriptEntries(entries []HistoryEntry) []HistoryEntry {
	for i := range entries {
		entries[i].Message = normalizeTranscriptMessage(entries[i].Message)
		if entries[i].EventType == ContextAdded && entries[i].ContextKind == "" {
			entries[i].ContextKind = ContextKindGeneric
		}
	}
	return entries
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
	if entry.EventType == ContextAdded {
		return true
	}

	// Older snapshots did not persist HistoryEntry.EventType. Keep recognizing
	// the built-in context blocks so those snapshots rebuild in the same order.
	content := entry.Message.Content
	return strings.Contains(content, "<conversation_summary>") ||
		strings.Contains(content, "<working_set>")
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
