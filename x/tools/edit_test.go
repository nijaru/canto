package tools

import (
	"context"
	"strings"
	"testing"

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
	out, err := tool.Execute(context.Background(), `{"path":"a.txt","before":"world","after":"team"}`)
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
	if _, err := tool.Execute(context.Background(), `{"path":"a.txt","before":"x","after":"y"}`); err == nil {
		t.Fatal("expected ambiguous match error")
	}
	data, _ := root.ReadFile("a.txt")
	if string(data) != "x x" {
		t.Fatalf("file changed on failure: %q", string(data))
	}
}

func TestMultiEditTool_AllOrNothing(t *testing.T) {
	root := openEditRoot(t)
	if err := root.WriteFile("a.txt", []byte("alpha"), 0o644); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := root.WriteFile("b.txt", []byte("beta"), 0o644); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}
	tool := NewMultiEditTool(root)
	_, err := tool.Execute(context.Background(), `{"edits":[{"path":"a.txt","before":"alpha","after":"A"},{"path":"b.txt","before":"missing","after":"B"}]}`)
	if err == nil {
		t.Fatal("expected multi_edit to fail")
	}
	aData, _ := root.ReadFile("a.txt")
	bData, _ := root.ReadFile("b.txt")
	if string(aData) != "alpha" || string(bData) != "beta" {
		t.Fatalf("files changed on failed multi_edit: %q %q", string(aData), string(bData))
	}
}
