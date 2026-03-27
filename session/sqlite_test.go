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
	parentLoaded, err := store.Load(ctx, parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	parentEvents := parentLoaded.Events()

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

	parent, err := store.Parent(ctx, childID)
	if err != nil {
		t.Fatalf("parent query failed: %v", err)
	}
	if parent == nil || parent.SessionID != parentID {
		t.Fatalf("parent ancestry = %#v, want session %q", parent, parentID)
	}

	children, err := store.Children(ctx, parentID)
	if err != nil {
		t.Fatalf("children query failed: %v", err)
	}
	if len(children) != 1 || children[0].SessionID != childID {
		t.Fatalf("children = %#v, want child %q", children, childID)
	}
	if children[0].ParentSessionID != parentID {
		t.Fatalf("child parent_session_id = %q, want %q", children[0].ParentSessionID, parentID)
	}
	if children[0].Depth != 1 {
		t.Fatalf("child depth = %d, want 1", children[0].Depth)
	}
	if children[0].ForkPointEventID != parentEvents[len(parentEvents)-1].ID.String() {
		t.Fatalf(
			"child fork_point_event_id = %q, want %q",
			children[0].ForkPointEventID,
			parentEvents[len(parentEvents)-1].ID,
		)
	}

	lineage, err := store.Lineage(ctx, childID)
	if err != nil {
		t.Fatalf("lineage query failed: %v", err)
	}
	if len(lineage) != 2 || lineage[0].SessionID != parentID || lineage[1].SessionID != childID {
		t.Fatalf("lineage = %#v, want [%q, %q]", lineage, parentID, childID)
	}
}

func TestSQLiteStoreLoadMaterializesMetadataOnEvents(t *testing.T) {
	dbFile := "test_canto_metadata.db"
	defer os.Remove(dbFile)

	store, err := NewSQLiteStore(dbFile)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.Close()

	event := NewEvent("meta-session", Handoff, map[string]string{"note": "hello"})
	event.Metadata = map[string]any{
		"kind": "handoff",
		"seq":  float64(1),
	}
	if err := store.Save(t.Context(), event); err != nil {
		t.Fatalf("save event: %v", err)
	}

	sess, err := store.Load(t.Context(), "meta-session")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}

	events := sess.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if got := events[0].Metadata["kind"]; got != "handoff" {
		t.Fatalf("metadata kind = %#v, want %q", got, "handoff")
	}
}

func TestSQLiteStoreLoadUsesMaxULIDBound(t *testing.T) {
	dbFile := "test_canto_max_ulid.db"
	defer os.Remove(dbFile)

	store, err := NewSQLiteStore(dbFile)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.Close()

	event := NewEvent("max-ulid-session", MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "latest",
	})
	event.ID = maxULID()
	if err := store.Save(t.Context(), event); err != nil {
		t.Fatalf("save event: %v", err)
	}

	sess, err := store.Load(t.Context(), "max-ulid-session")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if len(sess.Events()) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sess.Events()))
	}
}
