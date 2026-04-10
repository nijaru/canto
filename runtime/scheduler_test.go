package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLocalScheduler_ScheduleAndWait(t *testing.T) {
	scheduler := NewLocalScheduler()
	defer scheduler.Close()

	started := make(chan struct{})
	dueAt := time.Now().Add(50 * time.Millisecond)
	task, err := scheduler.Schedule(t.Context(), dueAt, func(context.Context) error {
		close(started)
		return nil
	})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}

	ref := task.Ref()
	if ref.ID == "" {
		t.Fatal("expected scheduled task id")
	}
	if !ref.DueAt.Equal(dueAt.UTC()) {
		t.Fatalf("dueAt = %v, want %v", ref.DueAt, dueAt.UTC())
	}
	if ref.Queued.IsZero() {
		t.Fatal("expected queued timestamp")
	}

	select {
	case <-started:
		t.Fatal("task started before due time")
	case <-time.After(20 * time.Millisecond):
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for scheduled task")
	}

	if err := task.Wait(t.Context()); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func TestLocalScheduler_CancelBeforeStart(t *testing.T) {
	scheduler := NewLocalScheduler()
	defer scheduler.Close()

	started := make(chan struct{})
	task, err := scheduler.Schedule(
		t.Context(),
		time.Now().Add(time.Hour),
		func(context.Context) error {
			close(started)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}

	if err := task.Cancel(t.Context()); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	select {
	case <-started:
		t.Fatal("task started after cancel")
	case <-time.After(20 * time.Millisecond):
	}

	if err := task.Wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context.Canceled", err)
	}
}
