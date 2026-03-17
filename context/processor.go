package context

import (
	"context"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Processor transforms a session and a request into an LLM request.
// It can add messages, tools, or modify parameters based on the session history
// and the specific model/provider being used.
type Processor interface {
	Process(
		ctx context.Context,
		p llm.Provider,
		model string,
		sess *session.Session,
		req *llm.Request,
	) error
}

// ProcessorFunc is an adapter to allow the use of ordinary functions as context processors.
type ProcessorFunc func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error

func (f ProcessorFunc) Process(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	return f(ctx, p, model, sess, req)
}
