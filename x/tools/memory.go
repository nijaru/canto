package tools

import (
	"context"
	"fmt"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/memory"
)

type RememberTool struct {
	Writer    memory.Writer
	Namespace memory.Namespace
	Role      memory.Role
}

func (t *RememberTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "remember_memory",
		Description: "Store durable memory in the configured namespace and role.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{"type": "string"},
				"key":     map[string]any{"type": "string"},
				"role":    map[string]any{"type": "string"},
				"metadata": map[string]any{
					"type": "object",
				},
				"importance": map[string]any{"type": "number"},
				"mode":       map[string]any{"type": "string"},
			},
			"required": []string{"content"},
		},
	}
}

func (t *RememberTool) Execute(ctx context.Context, args string) (string, error) {
	var input struct {
		Content    string         `json:"content"`
		Key        string         `json:"key"`
		Role       string         `json:"role"`
		Metadata   map[string]any `json:"metadata"`
		Importance float64        `json:"importance"`
		Mode       string         `json:"mode"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	role := t.Role
	if input.Role != "" {
		role = memory.Role(input.Role)
	}
	mode := memory.WriteMode(input.Mode)
	result, err := t.Writer.Write(ctx, memory.WriteInput{
		Namespace:  t.Namespace,
		Role:       role,
		Key:        input.Key,
		Content:    input.Content,
		Metadata:   input.Metadata,
		Importance: input.Importance,
		Mode:       mode,
	})
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result, jsontext.WithIndent("  "))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

type RecallTool struct {
	Retriever  memory.Retriever
	Namespaces []memory.Namespace
	Roles      []memory.Role
	Limit      int
}

func (t *RecallTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "recall_memory",
		Description: "Retrieve durable memory from the configured namespaces and roles.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":        map[string]any{"type": "string"},
				"use_semantic": map[string]any{"type": "boolean"},
				"limit":        map[string]any{"type": "integer"},
			},
			"required": []string{"query"},
		},
	}
}

func (t *RecallTool) Execute(ctx context.Context, args string) (string, error) {
	var input struct {
		Query       string `json:"query"`
		UseSemantic bool   `json:"use_semantic"`
		Limit       int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = t.Limit
	}
	results, err := t.Retriever.Retrieve(ctx, memory.Query{
		Namespaces:    t.Namespaces,
		Roles:         t.Roles,
		Text:          input.Query,
		UseSemantic:   input.UseSemantic,
		IncludeCore:   true,
		IncludeRecent: true,
		Limit:         limit,
	})
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(results, jsontext.WithIndent("  "))
	if err != nil {
		return "", err
	}
	return string(out), nil
}
