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
	event := NewEvent(sessionID, MessageAdded, msg)
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

func TestSQLiteStoreFork(t *testing.T) {
	dbFile := "test_canto_fork.db"
	defer os.Remove(dbFile)

	store, err := NewSQLiteStore(dbFile)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	parentID := "parent-session"
	childID := "child-session"

	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	} {
		if err := store.Save(ctx, NewEvent(parentID, MessageAdded, msg)); err != nil {
			t.Fatalf("save parent event: %v", err)
		}
	}

	child, err := store.Fork(ctx, parentID, childID)
	if err != nil {
		t.Fatalf("fork failed: %v", err)
	}
	if child.ID() != childID {
		t.Fatalf("forked session ID = %q, want %q", child.ID(), childID)
	}

	loaded, err := store.Load(ctx, childID)
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	if len(loaded.Events()) != 2 {
		t.Fatalf("expected 2 child events, got %d", len(loaded.Events()))
	}
	for _, event := range loaded.Events() {
		if event.SessionID != childID {
			t.Fatalf("child event session_id = %q, want %q", event.SessionID, childID)
		}
		origin, ok, err := event.ForkOrigin()
		if err != nil {
			t.Fatalf("fork origin decode: %v", err)
		}
		if !ok {
			t.Fatalf("child event missing fork origin metadata: %#v", event.Metadata)
		}
		if origin.SessionID != parentID {
			t.Fatalf("fork origin session_id = %q, want %q", origin.SessionID, parentID)
		}
	}
}
