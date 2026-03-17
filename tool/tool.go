package tool

import (
	"context"

	"github.com/nijaru/canto/llm"
)

// Tool is the interface for all executable tools.
type Tool interface {
	// Spec returns the LLM-compatible tool definition.
	Spec() llm.ToolSpec

	// Execute runs the tool with the given JSON arguments.
	Execute(ctx context.Context, args string) (string, error)
}

type StreamingTool interface {
	Tool
	// ExecuteStreaming runs the tool and calls emit for each chunk of output.
	// emit may return an error (e.g., if session storage fails), in which case
	// the tool should ideally stop and return that error.
	// It returns the final complete output.
	ExecuteStreaming(ctx context.Context, args string, emit func(string) error) (string, error)
}
