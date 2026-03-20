package agent

import (
	"context"

	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// Agent is the interface for all agents. Implementations may extend BaseAgent
// or provide custom behavior.
type Agent interface {
	ID() string
	Step(ctx context.Context, sess *session.Session) (StepResult, error)
	Turn(ctx context.Context, sess *session.Session) (StepResult, error)
}

// BaseAgent is the default Agent implementation. It runs an LLM with a
// context pipeline, tool registry, and lifecycle hooks.
type BaseAgent struct {
	agentID      string
	Instructions string
	Model        string
	MaxSteps     int // Maximum tool-calling steps per turn
	Provider     llm.Provider
	Tools        *tool.Registry
	Builder      *ccontext.Builder
	Hooks        *hook.Runner
}

// ID returns the agent's unique identifier.
func (a *BaseAgent) ID() string { return a.agentID }

// Option configures a BaseAgent after construction.
type Option func(*BaseAgent)

// WithMaxSteps sets the maximum number of tool-calling steps per turn.
func WithMaxSteps(n int) Option { return func(a *BaseAgent) { a.MaxSteps = n } }

// WithHooks replaces the agent's hook runner.
func WithHooks(h *hook.Runner) Option { return func(a *BaseAgent) { a.Hooks = h } }

// WithBuilder replaces the agent's context builder pipeline.
func WithBuilder(b *ccontext.Builder) Option { return func(a *BaseAgent) { a.Builder = b } }

// WithProcessors inserts legacy context processors into the default builder
// chain, placed before Capabilities (which must run last).
// New code should prefer WithRequestProcessors and WithMutators when possible.
func WithProcessors(ps ...ccontext.Processor) Option {
	return func(a *BaseAgent) {
		a.Builder.InsertBeforeLast(ps...)
	}
}

// WithRequestProcessors inserts preview-safe request processors into the
// default builder chain, placed before Capabilities (which must run last).
func WithRequestProcessors(ps ...ccontext.RequestProcessor) Option {
	return func(a *BaseAgent) {
		a.Builder.InsertRequestProcessorsBeforeLast(ps...)
	}
}

// WithMutators inserts commit-time mutators into the default builder chain,
// placed before Capabilities (which must run last).
func WithMutators(ms ...ccontext.ContextMutator) Option {
	return func(a *BaseAgent) {
		a.Builder.InsertMutatorsBeforeLast(ms...)
	}
}

// WithModel overrides the model used for LLM calls.
func WithModel(m string) Option { return func(a *BaseAgent) { a.Model = m } }

// New creates a BaseAgent with a default context builder chain.
// Optional opts are applied after defaults are set.
func New(
	id, instructions, model string,
	p llm.Provider,
	t *tool.Registry,
	opts ...Option,
) *BaseAgent {
	a := &BaseAgent{
		agentID:      id,
		Instructions: instructions,
		Model:        model,
		MaxSteps:     10,
		Provider:     p,
		Tools:        t,
		Hooks:        hook.NewRunner(),
	}

	a.Builder = ccontext.NewBuilder(
		ccontext.Instructions(instructions),
		ccontext.Tools(t),
		ccontext.History(),
		ccontext.Capabilities(), // must be last: adapts system/temp for reasoning models
	)

	for _, opt := range opts {
		opt(a)
	}

	return a
}
