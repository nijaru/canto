package context

import (
	"fmt"
	"slices"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func historyPrefix(req *llm.Request, historyLen int) []llm.Message {
	prefixLen := len(req.Messages) - historyLen
	if prefixLen <= 0 {
		return nil
	}
	return slices.Clone(req.Messages[:prefixLen])
}

func lastMessageEventID(sess *session.Session) string {
	var id string
	sess.ForEachEventReverse(func(e session.Event) bool {
		if e.Type == session.MessageAdded {
			id = e.ID.String()
			return false
		}
		return true
	})
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
