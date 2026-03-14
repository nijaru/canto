package session

import (
	"context"
	"os"
	"testing"

	"github.com/nijaru/canto/llm"
)

func TestSQLiteStore(t *testing.T) {
	dbFile := "test_canto.db"
	defer os.Remove(dbFile)

	store, err := NewSQLiteStore(dbFile)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	sessionID := "test-session"

	// 1. Save an event
	msg := llm.Message{Role: llm.RoleUser, Content: "find me a sandwich"}
	event := NewEvent(sessionID, EventTypeMessageAdded, msg)
	if err := store.Save(ctx, event); err != nil {
		t.Fatalf("failed to save event: %v", err)
	}

	// 2. Load session
	sess, err := store.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("failed to load session: %v", err)
	}
	if len(sess.Events()) != 1 {
		t.Errorf("expected 1 event, got %d", len(sess.Events()))
	}
	if sess.Events()[0].ID != event.ID {
		t.Errorf("expected event ID %s, got %s", event.ID, sess.Events()[0].ID)
	}

	// 3. Search (FTS5)
	results, err := store.Search(ctx, sessionID, "sandwich")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 search result, got %d", len(results))
	}
}
