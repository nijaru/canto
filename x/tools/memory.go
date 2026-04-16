package tools

import (
	"context"
	"fmt"
	"time"

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
				"content":     map[string]any{"type": "string"},
				"key":         map[string]any{"type": "string"},
				"role":        map[string]any{"type": "string"},
				"observed_at": map[string]any{"type": "string"},
				"valid_from":  map[string]any{"type": "string"},
				"valid_to":    map[string]any{"type": "string"},
				"supersedes":  map[string]any{"type": "string"},
				"metadata": map[string]any{
					"type": "object",
				},
				"importance": map[string]any{"type": "number"},
				"mode": map[string]any{
					"type":        "string",
					"enum":        []string{"sync", "async"},
					"description": "Optional write mode override. Omit to use the manager's default.",
				},
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
		ObservedAt string         `json:"observed_at"`
		ValidFrom  string         `json:"valid_from"`
		ValidTo    string         `json:"valid_to"`
		Supersedes string         `json:"supersedes"`
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
	observedAt, err := parseOptionalTime(input.ObservedAt)
	if err != nil {
		return "", fmt.Errorf("invalid observed_at: %w", err)
	}
	validFrom, err := parseOptionalTime(input.ValidFrom)
	if err != nil {
		return "", fmt.Errorf("invalid valid_from: %w", err)
	}
	validTo, err := parseOptionalTime(input.ValidTo)
	if err != nil {
		return "", fmt.Errorf("invalid valid_to: %w", err)
	}
	mode := memory.WriteMode("")
	if input.Mode != "" {
		switch memory.WriteMode(input.Mode) {
		case memory.WriteSync, memory.WriteAsync:
			mode = memory.WriteMode(input.Mode)
		default:
			return "", fmt.Errorf("invalid mode: %q", input.Mode)
		}
	}
	result, err := t.Writer.Write(ctx, memory.WriteInput{
		Namespace:  t.Namespace,
		Role:       role,
		Key:        input.Key,
		Content:    input.Content,
		ObservedAt: observedAt,
		ValidFrom:  validFrom,
		ValidTo:    validTo,
		Supersedes: input.Supersedes,
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
				"query":              map[string]any{"type": "string"},
				"use_semantic":       map[string]any{"type": "boolean"},
				"include_recent":     map[string]any{"type": "boolean"},
				"include_forgotten":  map[string]any{"type": "boolean"},
				"include_superseded": map[string]any{"type": "boolean"},
				"valid_at":           map[string]any{"type": "string"},
				"observed_after":     map[string]any{"type": "string"},
				"observed_before":    map[string]any{"type": "string"},
				"limit":              map[string]any{"type": "integer"},
			},
		},
	}
}

func (t *RecallTool) Execute(ctx context.Context, args string) (string, error) {
	var input struct {
		Query             string `json:"query"`
		UseSemantic       bool   `json:"use_semantic"`
		IncludeRecent     bool   `json:"include_recent"`
		IncludeForgotten  bool   `json:"include_forgotten"`
		IncludeSuperseded bool   `json:"include_superseded"`
		ValidAt           string `json:"valid_at"`
		ObservedAfter     string `json:"observed_after"`
		ObservedBefore    string `json:"observed_before"`
		Limit             int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	validAt, err := parseOptionalTime(input.ValidAt)
	if err != nil {
		return "", fmt.Errorf("invalid valid_at: %w", err)
	}
	observedAfter, err := parseOptionalTime(input.ObservedAfter)
	if err != nil {
		return "", fmt.Errorf("invalid observed_after: %w", err)
	}
	observedBefore, err := parseOptionalTime(input.ObservedBefore)
	if err != nil {
		return "", fmt.Errorf("invalid observed_before: %w", err)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = t.Limit
	}
	results, err := t.Retriever.Retrieve(ctx, memory.Query{
		Namespaces:        t.Namespaces,
		Roles:             t.Roles,
		Text:              input.Query,
		UseSemantic:       input.UseSemantic,
		IncludeRecent:     input.IncludeRecent,
		IncludeForgotten:  input.IncludeForgotten,
		IncludeSuperseded: input.IncludeSuperseded,
		ValidAt:           validAt,
		ObservedAfter:     observedAfter,
		ObservedBefore:    observedBefore,
		Limit:             limit,
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

func parseOptionalTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, err
	}
	return &value, nil
}
