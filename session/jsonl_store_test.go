package session

import (
	"os"
	"path/filepath"
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

func TestJSONLStoreLoadMissingSessionKeepsWriter(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new jsonl store: %v", err)
	}

	sess, err := store.Load(t.Context(), "missing-jsonl")
	if err != nil {
		t.Fatalf("load missing session: %v", err)
	}

	if err := sess.Append(t.Context(), NewEvent("missing-jsonl", MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "hello",
	})); err != nil {
		t.Fatalf("append missing session: %v", err)
	}

	path := filepath.Join(store.root.Name(), "missing-jsonl.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat missing session file: %v", err)
	}

	loaded, err := store.Load(t.Context(), "missing-jsonl")
	if err != nil {
		t.Fatalf("reload missing session: %v", err)
	}
	if got := len(loaded.Messages()); got != 1 {
		t.Fatalf("loaded messages = %d, want 1", got)
	}
}

func TestSessionBranchUsesJSONLLiveParentState(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new jsonl store: %v", err)
	}

	parent := New("live-parent").WithWriter(store)
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	} {
		if err := parent.Append(t.Context(), NewMessage(parent.ID(), msg)); err != nil {
			t.Fatalf("append parent message: %v", err)
		}
	}

	child, err := parent.Branch(t.Context(), "live-child", ForkOptions{
		BranchLabel: "fanout",
		ForkReason:  "test",
	})
	if err != nil {
		t.Fatalf("branch session: %v", err)
	}

	reloaded, err := store.Load(t.Context(), child.ID())
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	if got := len(reloaded.Messages()); got != 2 {
		t.Fatalf("reloaded child messages = %d, want 2", got)
	}

	parentAncestry, err := store.Parent(t.Context(), child.ID())
	if err != nil {
		t.Fatalf("load parent ancestry: %v", err)
	}
	if parentAncestry == nil || parentAncestry.SessionID != parent.ID() {
		t.Fatalf("child parent ancestry = %#v, want %q", parentAncestry, parent.ID())
	}
}
