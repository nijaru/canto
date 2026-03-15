package context

import (
	"context"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// ContextProcessor is a pure function that transforms a session and a request
// into an LLM request. It can add messages, tools, or modify parameters.
// Invariant: it should not have side effects on the session itself.
type ContextProcessor interface {
	Process(ctx context.Context, sess *session.Session, req *llm.LLMRequest) error
}

// ProcessorFunc is an adapter to allow the use of ordinary functions as context processors.
type ProcessorFunc func(ctx context.Context, sess *session.Session, req *llm.LLMRequest) error

func (f ProcessorFunc) Process(
	ctx context.Context,
	sess *session.Session,
	req *llm.LLMRequest,
) error {
	return f(ctx, sess, req)
}
