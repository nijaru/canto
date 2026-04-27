package governor

import (
	"fmt"
	"strings"

	"github.com/nijaru/canto/session"
)

func lastMessageEventID(sess *session.Session) string {
	var id string
	for e := range sess.Backward() {
		if e.Type == session.MessageAdded {
			id = e.ID.String()
			break
		}
	}
	return id
}

func cloneHistoryEntries(entries []session.HistoryEntry) []session.HistoryEntry {
	res := make([]session.HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		res = append(res, session.HistoryEntry{
			EventID:     entry.EventID,
			EventType:   entry.EventType,
			ContextKind: entry.ContextKind,
			Placement:   entry.Placement,
			Message:     entry.Message,
		})
	}
	return res
}

func offloadCandidateID(
	sessionID string,
	cutoffEventID string,
	entry session.HistoryEntry,
	index int,
) string {
	if entry.EventID != "" {
		return entry.EventID
	}
	if cutoffEventID != "" {
		return fmt.Sprintf("offload-%s-%s-%d", sessionID, cutoffEventID, index)
	}
	return fmt.Sprintf("offload-%s-%d", sessionID, index)
}

func offloadPlaceholder(path string) string {
	return fmt.Sprintf("[Content offloaded to %s. Use read_offload tool to retrieve.]", path)
}

// extractPreviousSummary finds the most recent <conversation_summary> block
// in durable context entries from a prior compaction. Returns the content
// inside the tags and true if found, or empty string and false otherwise.
func extractPreviousSummary(entries []session.HistoryEntry) (string, bool) {
	const openTag = "<conversation_summary>"
	const closeTag = "</conversation_summary>"

	// Walk backwards — most recent compaction snapshot summary comes last in
	// the snapshot entries.
	for i := len(entries) - 1; i >= 0; i-- {
		m := entries[i].Message
		start := strings.Index(m.Content, openTag)
		if start < 0 {
			continue
		}
		start += len(openTag)
		end := strings.Index(m.Content[start:], closeTag)
		if end < 0 {
			continue
		}
		return strings.TrimSpace(m.Content[start : start+end]), true
	}
	return "", false
}

func isDurableContextEntry(entry session.HistoryEntry) bool {
	if entry.EventType == session.ContextAdded &&
		entry.Placement == session.ContextPlacementPrefix {
		return true
	}
	content := entry.Message.Content
	return strings.Contains(content, "<conversation_summary>") ||
		strings.Contains(content, "<working_set>")
}
