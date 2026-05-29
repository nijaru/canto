package workspacetool

import (
	"context"
	"fmt"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/nijaru/ion/approval"
	"github.com/nijaru/ion/llm"
	"github.com/nijaru/ion/safety"
	"github.com/nijaru/ion/workspace"
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
		Description: "Apply one or more exact text replacements to a single file within the workspace root.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
				"edits": map[string]any{
					"type":        "array",
					"description": "One or more exact replacements. Each before value is matched against the original file, not against another edit's output.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"before": map[string]any{
								"type":        "string",
								"description": "Exact text to replace. Must match one unique, non-overlapping region of the original file.",
							},
							"after": map[string]any{
								"type":        "string",
								"description": "Replacement text.",
							},
						},
						"required": []string{"before", "after"},
					},
				},
			},
			"required": []string{"path", "edits"},
		},
	}
}

func (t *EditTool) Execute(_ context.Context, args string) (string, error) {
	var input struct {
		Path  string            `json:"path"`
		Edits []editReplacement `json:"edits"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	updated, replacements, err := applyEdit(t.root, input.Path, input.Edits)
	if err != nil {
		return "", err
	}
	result := map[string]any{
		"path":         input.Path,
		"replacements": replacements,
		"changed":      true,
		"preview":      previewEdits(input.Edits),
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
