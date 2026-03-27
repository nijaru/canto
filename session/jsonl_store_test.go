package session

import (
	"testing"

	"github.com/nijaru/canto/llm"
)

func TestJSONLStoreForkPersistsTreeQueries(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new jsonl store: %v", err)
	}

	parentID := "parent-jsonl"
	childID := "child-jsonl"

	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	} {
		if err := store.Save(t.Context(), NewEvent(parentID, MessageAdded, msg)); err != nil {
			t.Fatalf("save parent event: %v", err)
		}
	}

	if _, err := store.ForkWithOptions(t.Context(), parentID, childID, ForkOptions{
		BranchLabel: "review",
		ForkReason:  "compare approaches",
	}); err != nil {
		t.Fatalf("fork with options: %v", err)
	}

	parent, err := store.Parent(t.Context(), childID)
	if err != nil {
		t.Fatalf("parent query failed: %v", err)
	}
	if parent == nil || parent.SessionID != parentID {
		t.Fatalf("parent ancestry = %#v, want session %q", parent, parentID)
	}

	children, err := store.Children(t.Context(), parentID)
	if err != nil {
		t.Fatalf("children query failed: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	if children[0].SessionID != childID {
		t.Fatalf("child session_id = %q, want %q", children[0].SessionID, childID)
	}
	if children[0].BranchLabel != "review" || children[0].ForkReason != "compare approaches" {
		t.Fatalf("child ancestry metadata = %#v", children[0])
	}
	if children[0].Depth != 1 {
		t.Fatalf("child depth = %d, want 1", children[0].Depth)
	}

	lineage, err := store.Lineage(t.Context(), childID)
	if err != nil {
		t.Fatalf("lineage query failed: %v", err)
	}
	if len(lineage) != 2 || lineage[0].SessionID != parentID || lineage[1].SessionID != childID {
		t.Fatalf("lineage = %#v, want [%q, %q]", lineage, parentID, childID)
	}
}

func TestJSONLStoreRootSessionHasNilParent(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new jsonl store: %v", err)
	}

	sessionID := "root-jsonl"
	if err := store.Save(t.Context(), NewEvent(sessionID, MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "hello",
	})); err != nil {
		t.Fatalf("save root event: %v", err)
	}

	parent, err := store.Parent(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("parent query failed: %v", err)
	}
	if parent != nil {
		t.Fatalf("expected nil root parent, got %#v", parent)
	}

	lineage, err := store.Lineage(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("lineage query failed: %v", err)
	}
	if len(lineage) != 1 || lineage[0].SessionID != sessionID || lineage[0].Depth != 0 {
		t.Fatalf("lineage = %#v, want only root session", lineage)
	}
}
