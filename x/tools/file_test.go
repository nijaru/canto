package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTestRoot(t *testing.T) *os.Root {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	t.Cleanup(func() { root.Close() })
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

	// Create files via root.
	f, err := root.Create("alpha.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

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

func TestGlob(t *testing.T) {
	dir := t.TempDir()

	// Write files directly (glob walks root FS).
	if err := os.WriteFile(filepath.Join(dir, "main.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	g := NewGlobTool(root)
	out, err := g.Execute(context.Background(), `{"pattern":"*.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go") {
		t.Fatalf("missing main.go in: %q", out)
	}
	if strings.Contains(out, "README.md") {
		t.Fatalf("unexpected README.md in: %q", out)
	}
}

func TestGlob_NoMatches(t *testing.T) {
	root := openTestRoot(t)

	g := NewGlobTool(root)
	out, err := g.Execute(context.Background(), `{"pattern":"*.rs"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "(no matches)" {
		t.Fatalf("output = %q, want no-matches message", out)
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

func TestFileTools_Count(t *testing.T) {
	root := openTestRoot(t)
	tools := FileTools(root)
	if len(tools) != 4 {
		t.Fatalf("FileTools returned %d tools, want 4", len(tools))
	}
}
