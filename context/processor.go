package context

import (
	"context"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// ContextProcessor transforms a session and a request into an LLM request.
// It can add messages, tools, or modify parameters based on the session history
// and the specific model/provider being used.
type ContextProcessor interface {
	Process(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.LLMRequest) error
}

// ProcessorFunc is an adapter to allow the use of ordinary functions as context processors.
type ProcessorFunc func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.LLMRequest) error

func (f ProcessorFunc) Process(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
	req *llm.LLMRequest,
) error {
	return f(ctx, p, model, sess, req)
}
