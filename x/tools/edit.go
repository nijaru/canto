package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/workspace"
)

type EditTool struct {
	root workspace.WorkspaceFS
}

func NewEditTool(root workspace.WorkspaceFS) *EditTool {
	return &EditTool{root: root}
}

func (t *EditTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "edit",
		Description: "Replace an exact text snippet in a file within the workspace root.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string"},
				"before": map[string]any{"type": "string"},
				"after":  map[string]any{"type": "string"},
			},
			"required": []string{"path", "before", "after"},
		},
	}
}

func (t *EditTool) Execute(_ context.Context, args string) (string, error) {
	var input struct {
		Path   string `json:"path"`
		Before string `json:"before"`
		After  string `json:"after"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	updated, replacements, err := applyEdit(t.root, input.Path, input.Before, input.After)
	if err != nil {
		return "", err
	}
	result := map[string]any{
		"path":         input.Path,
		"replacements": replacements,
		"changed":      true,
		"preview":      previewChange(input.Before, input.After),
	}
	if err := t.root.WriteFile(input.Path, []byte(updated), 0o644); err != nil {
		return "", err
	}
	out, err := json.Marshal(result, jsontext.WithIndent("  "))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t *EditTool) ApprovalRequirement(args string) (approval.Requirement, bool, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return approval.Requirement{}, false, err
	}
	return approval.Requirement{
		Category:  string(safety.CategoryWrite),
		Operation: "edit",
		Resource:  input.Path,
	}, true, nil
}

type MultiEditTool struct {
	root workspace.WorkspaceFS
}

func NewMultiEditTool(root workspace.WorkspaceFS) *MultiEditTool {
	return &MultiEditTool{root: root}
}

func (t *MultiEditTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "multi_edit",
		Description: "Apply exact-match edits across one or more workspace files. All edits validate before any write occurs.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"edits": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path":   map[string]any{"type": "string"},
							"before": map[string]any{"type": "string"},
							"after":  map[string]any{"type": "string"},
						},
						"required": []string{"path", "before", "after"},
					},
				},
			},
			"required": []string{"edits"},
		},
	}
}

func (t *MultiEditTool) Execute(_ context.Context, args string) (string, error) {
	var input struct {
		Edits []struct {
			Path   string `json:"path"`
			Before string `json:"before"`
			After  string `json:"after"`
		} `json:"edits"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	type prepared struct {
		path         string
		content      string
		replacements int
		before       string
		after        string
	}
	preparedEdits := make([]prepared, 0, len(input.Edits))
	for _, edit := range input.Edits {
		updated, replacements, err := applyEdit(t.root, edit.Path, edit.Before, edit.After)
		if err != nil {
			return "", err
		}
		preparedEdits = append(preparedEdits, prepared{
			path:         edit.Path,
			content:      updated,
			replacements: replacements,
			before:       edit.Before,
			after:        edit.After,
		})
	}
	for _, edit := range preparedEdits {
		if err := t.root.WriteFile(edit.path, []byte(edit.content), 0o644); err != nil {
			return "", err
		}
	}
	summaries := make([]map[string]any, 0, len(preparedEdits))
	for _, edit := range preparedEdits {
		summaries = append(summaries, map[string]any{
			"path":         edit.path,
			"replacements": edit.replacements,
			"preview":      previewChange(edit.before, edit.after),
		})
	}
	out, err := json.Marshal(summaries, jsontext.WithIndent("  "))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t *MultiEditTool) ApprovalRequirement(args string) (approval.Requirement, bool, error) {
	var input struct {
		Edits []struct {
			Path string `json:"path"`
		} `json:"edits"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return approval.Requirement{}, false, err
	}
	paths := make([]string, 0, len(input.Edits))
	for _, edit := range input.Edits {
		paths = append(paths, edit.Path)
	}
	return approval.Requirement{
		Category:  "workspace",
		Operation: "multi_edit",
		Resource:  strings.Join(paths, ","),
	}, true, nil
}

func EditTools(root workspace.WorkspaceFS) []tool.Tool {
	return []tool.Tool{
		NewEditTool(root),
		NewMultiEditTool(root),
	}
}

func WorkspaceTools(root workspace.WorkspaceFS) []tool.Tool {
	tools := FileTools(root)
	return append(tools, EditTools(root)...)
}

func applyEdit(root workspace.WorkspaceFS, path, before, after string) (string, int, error) {
	data, err := root.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	content := string(data)
	count := strings.Count(content, before)
	if count == 0 {
		return "", 0, fmt.Errorf("edit %s: exact match not found", path)
	}
	if count > 1 {
		return "", 0, fmt.Errorf("edit %s: exact match is ambiguous (%d matches)", path, count)
	}
	return strings.Replace(content, before, after, 1), 1, nil
}

func previewChange(before, after string) string {
	return fmt.Sprintf("--- before\n%s\n+++ after\n%s", before, after)
}
