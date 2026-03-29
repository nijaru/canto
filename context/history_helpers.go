package context

import (
	"fmt"

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
			EventID: entry.EventID,
			Message: entry.Message,
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
