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

// StreamingTool is a Tool that can emit partial output during execution.
type StreamingTool interface {
	Tool
	// ExecuteStreaming runs the tool and calls emit for each chunk of output.
	// It returns the final complete output.
	ExecuteStreaming(ctx context.Context, args string, emit func(string)) (string, error)
}
