package session

import (
	"context"
	"errors"
	"testing"

	"github.com/nijaru/canto/llm"
)

type failingWriter struct {
	err error
}

func (w *failingWriter) Save(_ context.Context, _ Event) error {
	return w.err
}

func TestSessionAppend_DoesNotMutateStateWhenWriterFails(t *testing.T) {
	sess := New("append-fail").WithWriter(&failingWriter{err: errors.New("boom")})
	sess.WithReducer(func(state map[string]any, e Event) map[string]any {
		count, _ := state["count"].(int)
		state["count"] = count + 1
		return state
	})

	subCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sub := sess.Watch(subCtx)
	defer sub.Close()

	err := sess.Append(t.Context(), NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "hello",
	}))
	if err == nil {
		t.Fatal("expected append to fail")
	}
	if len(sess.Events()) != 0 {
		t.Fatalf("expected no in-memory events after failed append, got %d", len(sess.Events()))
	}
	if len(sess.State()) != 0 {
		t.Fatalf("expected reducer state to remain empty, got %#v", sess.State())
	}

	select {
	case e := <-sub.Events():
		t.Fatalf("unexpected subscriber event after failed append: %#v", e)
	default:
	}
}
