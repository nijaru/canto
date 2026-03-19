package session

import (
	"fmt"
	"slices"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/llm"
)

// HistoryEntry captures a model-visible message together with its originating
// message event ID when one exists.
type HistoryEntry struct {
	EventID string      `json:"event_id,omitzero"`
	Message llm.Message `json:"message"`
}

// CompactionSnapshot captures the model-visible history after a compaction step.
type CompactionSnapshot struct {
	Strategy      string         `json:"strategy"`
	MaxTokens     int            `json:"max_tokens,omitzero"`
	ThresholdPct  float64        `json:"threshold_pct,omitzero"`
	CurrentTokens int            `json:"current_tokens,omitzero"`
	CutoffEventID string         `json:"cutoff_event_id,omitzero"`
	Entries       []HistoryEntry `json:"entries,omitzero"`
	Messages      []llm.Message  `json:"messages,omitzero"`
}

// ForkOrigin identifies the parent event copied into a forked session.
type ForkOrigin struct {
	SessionID string `json:"session_id"`
	EventID   string `json:"event_id"`
}

func (o ForkOrigin) metadataValue() map[string]any {
	return map[string]any{
		"session_id": o.SessionID,
		"event_id":   o.EventID,
	}
}

// NewCompactionEvent records a durable compaction snapshot in the session log.
func NewCompactionEvent(sessionID string, snapshot CompactionSnapshot) Event {
	return NewEvent(sessionID, CompactionTriggered, snapshot)
}

// CompactionSnapshot decodes the payload of a compaction event.
func (e Event) CompactionSnapshot() (CompactionSnapshot, bool, error) {
	if e.Type != CompactionTriggered {
		return CompactionSnapshot{}, false, nil
	}

	var snapshot CompactionSnapshot
	if err := e.UnmarshalData(&snapshot); err != nil {
		return CompactionSnapshot{}, true, fmt.Errorf("decode compaction event %s: %w", e.ID, err)
	}
	return snapshot, true, nil
}

// ForkOrigin decodes the fork lineage metadata attached to a copied event.
func (e Event) ForkOrigin() (ForkOrigin, bool, error) {
	raw, ok := e.Metadata["fork_origin"]
	if !ok {
		return ForkOrigin{}, false, nil
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return ForkOrigin{}, true, fmt.Errorf("marshal fork origin for event %s: %w", e.ID, err)
	}

	var origin ForkOrigin
	if err := json.Unmarshal(data, &origin); err != nil {
		return ForkOrigin{}, true, fmt.Errorf("decode fork origin for event %s: %w", e.ID, err)
	}
	return origin, true, nil
}

// EffectiveMessages returns the model-visible session history after applying
// the latest durable compaction snapshot, if any.
func (s *Session) EffectiveMessages() ([]llm.Message, error) {
	entries, err := s.EffectiveEntries()
	if err != nil {
		return nil, err
	}
	messages := make([]llm.Message, 0, len(entries))
	for _, entry := range entries {
		messages = append(messages, entry.Message)
	}
	return messages, nil
}

// EffectiveEntries returns the model-visible session history after applying
// the latest durable compaction snapshot, together with the originating event
// ID for each message when known.
func (s *Session) EffectiveEntries() ([]HistoryEntry, error) {
	return effectiveEntriesFromEvents(s.snapshotEvents())
}

func effectiveEntriesFromEvents(events []Event) ([]HistoryEntry, error) {
	snapshot, ok, err := latestCompactionSnapshot(events)
	if err != nil {
		return nil, err
	}
	if !ok {
		return rawEntriesFromEvents(events)
	}

	entries := slices.Clone(snapshot.entries())
	cutoffSeen := false
	for _, e := range events {
		if e.Type != MessageAdded {
			continue
		}
		if !cutoffSeen {
			if e.ID.String() == snapshot.CutoffEventID {
				cutoffSeen = true
			}
			continue
		}

		entry, err := historyEntryFromEvent(e)
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

func rawEntriesFromEvents(events []Event) ([]HistoryEntry, error) {
	res := make([]HistoryEntry, 0, len(events))
	for _, e := range events {
		if e.Type != MessageAdded {
			continue
		}

		entry, err := historyEntryFromEvent(e)
		if err != nil {
			return nil, fmt.Errorf("effective history: decode raw message %s: %w", e.ID, err)
		}
		res = append(res, entry)
	}
	return res, nil
}

func historyEntryFromEvent(e Event) (HistoryEntry, error) {
	var msg llm.Message
	if err := e.UnmarshalData(&msg); err != nil {
		return HistoryEntry{}, err
	}
	return HistoryEntry{
		EventID: e.ID.String(),
		Message: msg,
	}, nil
}

func latestCompactionSnapshot(events []Event) (CompactionSnapshot, bool, error) {
	for i := len(events) - 1; i >= 0; i-- {
		snapshot, ok, err := events[i].CompactionSnapshot()
		if err != nil {
			return CompactionSnapshot{}, false, err
		}
		if !ok {
			continue
		}
		if snapshot.CutoffEventID == "" || len(snapshot.entries()) == 0 {
			continue
		}
		return snapshot, true, nil
	}

	return CompactionSnapshot{}, false, nil
}

func (s CompactionSnapshot) entries() []HistoryEntry {
	if len(s.Entries) > 0 {
		return s.Entries
	}
	entries := make([]HistoryEntry, 0, len(s.Messages))
	for _, msg := range s.Messages {
		entries = append(entries, HistoryEntry{Message: msg})
	}
	return entries
}

func remapCompactionSnapshot(
	snapshot CompactionSnapshot,
	idMap map[string]string,
) CompactionSnapshot {
	if newID, ok := idMap[snapshot.CutoffEventID]; ok {
		snapshot.CutoffEventID = newID
	}
	if len(snapshot.Entries) == 0 {
		return snapshot
	}

	entries := make([]HistoryEntry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		if newID, ok := idMap[entry.EventID]; ok {
			entry.EventID = newID
		}
		entries = append(entries, entry)
	}
	snapshot.Entries = entries
	return snapshot
}
