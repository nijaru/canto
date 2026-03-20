package runtime

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkLocalQueueSameSession(b *testing.B) {
	mgr := newSerialQueue()
	defer mgr.stop()

	for b.Loop() {
		errCh := mgr.execute(b.Context(), "same-session", func(ctx context.Context) error {
			return nil
		})
		if err := <-errCh; err != nil {
			b.Fatalf("lane execute: %v", err)
		}
	}
}

func BenchmarkLocalQueueDistinctSessions(b *testing.B) {
	mgr := newSerialQueue()
	defer mgr.stop()
	var seq int

	for b.Loop() {
		sessionID := fmt.Sprintf("session-%d", seq)
		seq++
		errCh := mgr.execute(b.Context(), sessionID, func(ctx context.Context) error {
			return nil
		})
		if err := <-errCh; err != nil {
			b.Fatalf("lane execute: %v", err)
		}
	}
}
