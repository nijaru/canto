package session

import (
	"context"
	"log/slog"
)

// AttachWriteThrough subscribes to sess and saves every newly appended event
// to store immediately, rather than batching after the agent turn.
//
// This is essential for long-horizon agents where a mid-turn crash would
// otherwise lose dozens of steps of work.
//
// Returns a cancel function; call it to detach and release resources.
// Typically called just before the agent turn and deferred to cancel after.
//
//	cancel := session.AttachWriteThrough(ctx, sess, store)
//	defer cancel()
//	agent.Turn(ctx, sess)
func AttachWriteThrough(ctx context.Context, sess *Session, store Store) func() {
	wctx, cancel := context.WithCancel(ctx)
	ch := sess.Subscribe(wctx)

	go func() {
		for e := range ch {
			if err := store.Save(context.Background(), e); err != nil {
				slog.Warn("write-through save failed",
					"session_id", e.SessionID,
					"event_id", e.ID,
					"error", err,
				)
			}
		}
	}()

	return cancel
}
