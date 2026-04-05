package tool

import (
	"context"
	"iter"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
)

// Tool is the interface for all executable tools.
type Tool interface {
	// Spec returns the LLM-compatible tool definition.
	Spec() llm.Spec

	// Execute runs the tool with the given JSON arguments.
	Execute(ctx context.Context, args string) (string, error)
}

type StreamingTool interface {
	Tool
	// ExecuteStreaming runs the tool and returns an iterator that yields
	// chunks of output.
	ExecuteStreaming(ctx context.Context, args string) iter.Seq2[string, error]
}

type ApprovalTool interface {
	Tool
	ApprovalRequirement(args string) (approval.Requirement, bool, error)
}
