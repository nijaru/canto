package context

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// CompactTool exposes session compaction as an LLM-callable tool.
// The agent decides when to compact by invoking it with an optional
// message that guides the summarizer.
type CompactTool struct {
	Provider llm.Provider
	Model    string
	Session  *session.Session
	Options  CompactOptions
}

// NewCompactTool creates a CompactTool wired to the given provider, model,
// session, and compaction options.
func NewCompactTool(
	provider llm.Provider,
	model string,
	sess *session.Session,
	opts CompactOptions,
) tool.Tool {
	return &CompactTool{
		Provider: provider,
		Model:    model,
		Session:  sess,
		Options:  opts,
	}
}

func (t *CompactTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "compact",
		Description: "Compact the current conversation context by summarizing older messages. Use when switching topics or when earlier context is no longer needed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "Optional message guiding the summarizer on what to preserve or emphasize.",
				},
			},
		},
	}
}

func (t *CompactTool) Execute(ctx context.Context, args string) (string, error) {
	var input struct {
		Message string `json:"message"`
	}
	if args != "" {
		if err := json.Unmarshal([]byte(args), &input); err != nil {
			return "", fmt.Errorf("compact: invalid args: %w", err)
		}
	}

	opts := t.Options
	opts.Message = input.Message

	result, err := CompactSession(ctx, t.Provider, t.Model, t.Session, opts)
	if err != nil {
		return "", fmt.Errorf("compact: %w", err)
	}

	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("compact: encode result: %w", err)
	}
	return string(out), nil
}
