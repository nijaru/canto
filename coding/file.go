package coding

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/workspace"
)

// ReadFileTool reads a file within a sandboxed root directory.
type ReadFileTool struct {
	root workspace.WorkspaceFS
}

// NewReadFileTool creates a ReadFileTool sandboxed to root.
// Paths outside root are rejected by the OS.
func NewReadFileTool(root workspace.WorkspaceFS) *ReadFileTool {
	return &ReadFileTool{root: root}
}

func (t *ReadFileTool) Spec() llm.Spec {
	return llm.Spec{
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
	data, err := t.root.ReadFile(input.Path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	return string(data), nil
}

// WriteFileTool writes a file within a sandboxed root directory.
type WriteFileTool struct {
	root workspace.WorkspaceFS
}

// NewWriteFileTool creates a WriteFileTool sandboxed to root.
func NewWriteFileTool(root workspace.WorkspaceFS) *WriteFileTool {
	return &WriteFileTool{root: root}
}

func (t *WriteFileTool) Spec() llm.Spec {
	return llm.Spec{
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
	if err := t.root.WriteFile(input.Path, []byte(input.Content), 0o644); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(input.Content), input.Path), nil
}

func (t *WriteFileTool) ApprovalRequirement(args string) (approval.Requirement, bool, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return approval.Requirement{}, false, err
	}
	return approval.Requirement{
		Category:  string(safety.CategoryWrite),
		Operation: "write_file",
		Resource:  input.Path,
	}, true, nil
}

// ListDirTool lists the contents of a directory within a sandboxed root.
type ListDirTool struct {
	root workspace.WorkspaceFS
}

// NewListDirTool creates a ListDirTool sandboxed to root.
func NewListDirTool(root workspace.WorkspaceFS) *ListDirTool {
	return &ListDirTool{root: root}
}

func (t *ListDirTool) Spec() llm.Spec {
	return llm.Spec{
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
	entries, err := t.root.ReadDir(input.Path)
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
