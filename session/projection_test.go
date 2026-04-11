package session

import (
	"context"
	"testing"
	"time"

	"github.com/nijaru/canto/llm"
)

func TestProjectionSnapshotterSnapshotIfNeededUsesCountPolicy(t *testing.T) {
	sess := New("projection-count")
	snapshotter := &ProjectionSnapshotter{
		MaxEvents: 2,
		Rebuilder: NewRebuilder(),
	}

	first := llm.Message{Role: llm.RoleUser, Content: "one"}
	second := llm.Message{Role: llm.RoleAssistant, Content: "two"}
	for _, msg := range []llm.Message{first, second} {
		if err := sess.Append(t.Context(), NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}

	ok, err := snapshotter.SnapshotIfNeeded(t.Context(), sess)
	if err != nil {
		t.Fatalf("SnapshotIfNeeded: %v", err)
	}
	if !ok {
		t.Fatal("expected snapshot to be appended")
	}

	events := sess.Events()
	if got := events[len(events)-1].Type; got != ProjectionSnapshotted {
		t.Fatalf("last event type = %q, want projection snapshot", got)
	}
	snapshot, ok, err := events[len(events)-1].ProjectionSnapshot()
	if err != nil {
		t.Fatalf("decode projection snapshot: %v", err)
	}
	if !ok {
		t.Fatal("expected projection snapshot payload")
	}
	if snapshot.Strategy != string(ProjectionTriggerCount) {
		t.Fatalf("snapshot strategy = %q, want %q", snapshot.Strategy, ProjectionTriggerCount)
	}
	if snapshot.CutoffEventID != events[len(events)-2].ID.String() {
		t.Fatalf(
			"snapshot cutoff = %q, want %q",
			snapshot.CutoffEventID,
			events[len(events)-2].ID,
		)
	}

	if err := sess.Append(t.Context(), NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "three",
	})); err != nil {
		t.Fatalf("append post-snapshot message: %v", err)
	}

	ok, err = snapshotter.SnapshotIfNeeded(t.Context(), sess)
	if err != nil {
		t.Fatalf("SnapshotIfNeeded after checkpoint: %v", err)
	}
	if ok {
		t.Fatal("expected checkpoint count to reset after projection snapshot")
	}
}

func TestProjectionSnapshotterSnapshotIfNeededUsesAgePolicy(t *testing.T) {
	sess := New("projection-age")
	now := time.Unix(1_000, 0).UTC()
	snapshotter := &ProjectionSnapshotter{
		MaxEvents: 100,
		MaxAge:    time.Minute,
		Now:       func() time.Time { return now.Add(2 * time.Minute) },
		Rebuilder: NewRebuilder(),
	}

	event := NewMessage(sess.ID(), llm.Message{Role: llm.RoleUser, Content: "one"})
	event.Timestamp = now
	if err := sess.Append(context.Background(), event); err != nil {
		t.Fatalf("append message: %v", err)
	}

	ok, err := snapshotter.SnapshotIfNeeded(t.Context(), sess)
	if err != nil {
		t.Fatalf("SnapshotIfNeeded: %v", err)
	}
	if !ok {
		t.Fatal("expected age policy to trigger a snapshot")
	}
}
