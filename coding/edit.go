package coding

import (
	"context"
	"fmt"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/safety"
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
