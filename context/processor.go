package context

import (
	"context"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Processor transforms session context into an LLM request.
// Processors always mutate the in-flight request and may optionally append
// durable session facts or write external artifacts. Processors that do so
// should implement EffectDescriber so callers can reason about side effects.
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

// Effects reports that ProcessorFunc only rewrites the in-flight request.
func (f ProcessorFunc) Effects() ProcessorEffects {
	return ProcessorEffects{}
}

func (f ProcessorFunc) Process(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	return f(ctx, p, model, sess, req)
}
