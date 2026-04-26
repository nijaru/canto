package coding

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/workspace"
)

func openTestRoot(t *testing.T) *workspace.Root {
	t.Helper()
	dir := t.TempDir()
	root, err := workspace.Open(dir)
	if err != nil {
		t.Fatalf("workspace.Open: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestWriteReadFile(t *testing.T) {
	root := openTestRoot(t)

	w := NewWriteFileTool(root)
	out, err := w.Execute(context.Background(), `{"path":"hello.txt","content":"hello world"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello.txt") {
		t.Fatalf("unexpected write output: %q", out)
	}

	r := NewReadFileTool(root)
	content, err := r.Execute(context.Background(), `{"path":"hello.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if content != "hello world" {
		t.Fatalf("content = %q, want %q", content, "hello world")
	}
}

func TestWriteFile_CreatesDirectories(t *testing.T) {
	root := openTestRoot(t)

	w := NewWriteFileTool(root)
	_, err := w.Execute(context.Background(), `{"path":"a/b/c.txt","content":"nested"}`)
	if err != nil {
		t.Fatal(err)
	}

	r := NewReadFileTool(root)
	content, err := r.Execute(context.Background(), `{"path":"a/b/c.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if content != "nested" {
		t.Fatalf("content = %q", content)
	}
}

func TestListDir(t *testing.T) {
	root := openTestRoot(t)

	if err := root.WriteFile("alpha.txt", nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := root.MkdirAll("subdir", 0o755); err != nil {
		t.Fatal(err)
	}

	l := NewListDirTool(root)
	out, err := l.Execute(context.Background(), `{"path":"."}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha.txt") {
		t.Fatalf("missing alpha.txt in: %q", out)
	}
	if !strings.Contains(out, "subdir") {
		t.Fatalf("missing subdir in: %q", out)
	}
}

func TestReadFile_Missing(t *testing.T) {
	root := openTestRoot(t)

	r := NewReadFileTool(root)
	_, err := r.Execute(context.Background(), `{"path":"nope.txt"}`)
	if err == nil {
		t.Fatal("expected error reading missing file")
	}
}
