package coding

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/workspace"
)

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

	contents := make(map[string]string)
	var writeOrder []string
	preparedEdits := make([]preparedEdit, 0, len(input.Edits))

	for _, edit := range input.Edits {
		content, ok := contents[edit.Path]
		if !ok {
			data, err := t.root.ReadFile(edit.Path)
			if err != nil {
				return "", err
			}
			content = string(data)
			contents[edit.Path] = content
			writeOrder = append(writeOrder, edit.Path)
		}

		updated, replacements, err := applyEditToContent(
			edit.Path,
			content,
			edit.Before,
			edit.After,
		)
		if err != nil {
			return "", err
		}
		contents[edit.Path] = updated
		preparedEdits = append(preparedEdits, preparedEdit{
			path:         edit.Path,
			replacements: replacements,
			before:       edit.Before,
			after:        edit.After,
		})
	}

	for _, path := range writeOrder {
		if err := t.root.WriteFile(path, []byte(contents[path]), 0o644); err != nil {
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
		Category:  string(safety.CategoryWrite),
		Operation: "multi_edit",
		Resource:  strings.Join(paths, ","),
	}, true, nil
}
