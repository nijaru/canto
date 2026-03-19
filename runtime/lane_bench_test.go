package runtime

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkLaneManagerSameSession(b *testing.B) {
	mgr := NewLaneManager()
	defer mgr.Stop()

	for b.Loop() {
		errCh := mgr.Execute(b.Context(), "same-session", func(ctx context.Context) error {
			return nil
		})
		if err := <-errCh; err != nil {
			b.Fatalf("lane execute: %v", err)
		}
	}
}

func BenchmarkLaneManagerDistinctSessions(b *testing.B) {
	mgr := NewLaneManager()
	defer mgr.Stop()
	var seq int

	for b.Loop() {
		sessionID := fmt.Sprintf("session-%d", seq)
		seq++
		errCh := mgr.Execute(b.Context(), sessionID, func(ctx context.Context) error {
			return nil
		})
		if err := <-errCh; err != nil {
			b.Fatalf("lane execute: %v", err)
		}
	}
}
