package tools

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

// ReadFileTool reads a file within a sandboxed root directory.
type ReadFileTool struct {
	root *os.Root
}

// NewReadFileTool creates a ReadFileTool sandboxed to root.
// Paths outside root are rejected by the OS.
func NewReadFileTool(root *os.Root) *ReadFileTool {
	return &ReadFileTool{root: root}
}

func (t *ReadFileTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "read_file",
		Description: "Read the contents of a file. Paths are relative to the workspace root.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative path to the file.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadFileTool) Execute(_ context.Context, args string) (string, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	f, err := t.root.Open(input.Path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	return string(data), nil
}

// WriteFileTool writes a file within a sandboxed root directory.
type WriteFileTool struct {
	root *os.Root
}

// NewWriteFileTool creates a WriteFileTool sandboxed to root.
func NewWriteFileTool(root *os.Root) *WriteFileTool {
	return &WriteFileTool{root: root}
}

func (t *WriteFileTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "write_file",
		Description: "Write content to a file. Creates parent directories if needed. Overwrites existing content.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative path to the file.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to write.",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t *WriteFileTool) Execute(_ context.Context, args string) (string, error) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	// Ensure parent directories exist within root.
	dir := filepath.Dir(input.Path)
	if dir != "." {
		if err := t.root.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("write_file mkdir: %w", err)
		}
	}

	f, err := t.root.OpenFile(input.Path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(input.Content); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(input.Content), input.Path), nil
}

// ListDirTool lists the contents of a directory within a sandboxed root.
type ListDirTool struct {
	root *os.Root
}

// NewListDirTool creates a ListDirTool sandboxed to root.
func NewListDirTool(root *os.Root) *ListDirTool {
	return &ListDirTool{root: root}
}

func (t *ListDirTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "list_dir",
		Description: "List the contents of a directory. Returns names and types (file/dir).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative path to the directory. Use \".\" for the root.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ListDirTool) Execute(_ context.Context, args string) (string, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	f, err := t.root.Open(input.Path)
	if err != nil {
		return "", fmt.Errorf("list_dir: %w", err)
	}
	defer f.Close()

	entries, err := f.ReadDir(-1)
	if err != nil {
		return "", fmt.Errorf("list_dir: %w", err)
	}

	var sb strings.Builder
	for _, e := range entries {
		kind := "file"
		if e.IsDir() {
			kind = "dir"
		}
		fmt.Fprintf(&sb, "%s\t%s\n", kind, e.Name())
	}
	return sb.String(), nil
}

// GlobTool matches files by pattern within a sandboxed root.
type GlobTool struct {
	root *os.Root
}

// NewGlobTool creates a GlobTool sandboxed to root.
func NewGlobTool(root *os.Root) *GlobTool {
	return &GlobTool{root: root}
}

func (t *GlobTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "glob",
		Description: "Find files matching a glob pattern within the workspace. Uses filepath.Match syntax: * matches any sequence of non-separator characters, ? matches any single non-separator character. Single directory level only; use list_dir for recursive exploration.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern using filepath.Match syntax, e.g. \"*.go\" or \"src/*.ts\".",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GlobTool) Execute(ctx context.Context, args string) (string, error) {
	var input struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	var matches []string
	err := fs.WalkDir(t.root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		ok, matchErr := filepath.Match(input.Pattern, path)
		if matchErr != nil {
			return fmt.Errorf("glob: invalid pattern %q: %w", input.Pattern, matchErr)
		}
		if ok {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return strings.Join(matches, "\n"), nil
}

// FileTools returns ReadFile, WriteFile, ListDir, and Glob as a slice,
// all sandboxed to root.
func FileTools(root *os.Root) []tool.Tool {
	return []tool.Tool{
		NewReadFileTool(root),
		NewWriteFileTool(root),
		NewListDirTool(root),
		NewGlobTool(root),
	}
}
