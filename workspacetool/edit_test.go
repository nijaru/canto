package workspacetool

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/workspace"
)

func openEditRoot(t *testing.T) *workspace.Root {
	t.Helper()
	root, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatalf("workspace.Open: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestEditTool_Success(t *testing.T) {
	root := openEditRoot(t)
	if err := root.WriteFile("a.txt", []byte("hello world"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewEditTool(root)
	out, err := tool.Execute(
		context.Background(),
		`{"path":"a.txt","edits":[{"before":"world","after":"team"}]}`,
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, `"replacements": 1`) {
		t.Fatalf("unexpected output: %s", out)
	}
	data, err := root.ReadFile("a.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello team" {
		t.Fatalf("got %q", string(data))
	}
}

func TestEditTool_FailsOnAmbiguousMatch(t *testing.T) {
	root := openEditRoot(t)
	if err := root.WriteFile("a.txt", []byte("x x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewEditTool(root)
	if _, err := tool.Execute(context.Background(), `{"path":"a.txt","edits":[{"before":"x","after":"y"}]}`); err == nil {
		t.Fatal("expected ambiguous match error")
	}
	data, _ := root.ReadFile("a.txt")
	if string(data) != "x x" {
		t.Fatalf("file changed on failure: %q", string(data))
	}
}

func TestEditTool_AppliesMultipleSameFileEditsAgainstOriginalContent(t *testing.T) {
	root := openEditRoot(t)
	if err := root.WriteFile("a.txt", []byte("alpha beta gamma"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewEditTool(root)
	_, err := tool.Execute(
		context.Background(),
		`{"path":"a.txt","edits":[{"before":"alpha","after":"A"},{"before":"beta","after":"B"}]}`,
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := root.ReadFile("a.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "A B gamma" {
		t.Fatalf("got %q", string(data))
	}
}

func TestEditTool_ValidatesAllEditsBeforeWrite(t *testing.T) {
	root := openEditRoot(t)
	if err := root.WriteFile("a.txt", []byte("alpha beta"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewEditTool(root)
	_, err := tool.Execute(
		context.Background(),
		`{"path":"a.txt","edits":[{"before":"alpha","after":"A"},{"before":"missing","after":"B"}]}`,
	)
	if err == nil {
		t.Fatal("expected edit to fail")
	}
	data, err := root.ReadFile("a.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "alpha beta" {
		t.Fatalf("file changed on failed edit: %q", string(data))
	}
}

func TestEditTool_RejectsOverlappingEdits(t *testing.T) {
	root := openEditRoot(t)
	if err := root.WriteFile("a.txt", []byte("alpha beta"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewEditTool(root)
	_, err := tool.Execute(
		context.Background(),
		`{"path":"a.txt","edits":[{"before":"alpha","after":"A"},{"before":"alpha beta","after":"AB"}]}`,
	)
	if err == nil {
		t.Fatal("expected overlapping edit error")
	}
	data, err := root.ReadFile("a.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "alpha beta" {
		t.Fatalf("file changed on failed edit: %q", string(data))
	}
}

func TestEditTool_ApprovalRequirementIsWrite(t *testing.T) {
	root := openEditRoot(t)
	req, ok, err := NewEditTool(root).ApprovalRequirement(
		`{"path":"a.txt","edits":[{"before":"alpha","after":"A"}]}`,
	)
	if err != nil {
		t.Fatalf("ApprovalRequirement: %v", err)
	}
	if !ok {
		t.Fatal("expected approval requirement")
	}
	if req.Category != string(safety.CategoryWrite) {
		t.Fatalf("category = %q, want %q", req.Category, safety.CategoryWrite)
	}
	if req.Resource != "a.txt" {
		t.Fatalf("resource = %q, want a.txt", req.Resource)
	}
}
