package agent

import (
	"context"

	"github.com/nijaru/canto/approval"
	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/governor"
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
	agentID          string
	instructions     string
	model            string
	maxSteps         int // Maximum tool-calling steps per turn
	maxEscalations   int // Maximum recoverable retries per turn
	maxParallelTools int // Maximum concurrent tool executions per step
	provider         llm.Provider
	tools            *tool.Registry
	builder          *ccontext.Builder
	hooks            *hook.Runner
	approvals        *approval.Manager
}

// ID returns the agent's unique identifier.
func (a *BaseAgent) ID() string { return a.agentID }

// Instructions returns the assembled system instructions for the agent.
func (a *BaseAgent) Instructions() string { return a.instructions }

// Option configures a BaseAgent after construction.
type Option func(*BaseAgent)

// WithMaxSteps sets the maximum number of tool-calling steps per turn.
func WithMaxSteps(n int) Option { return func(a *BaseAgent) { a.maxSteps = n } }

// WithMaxParallelTools sets the maximum concurrent tool executions per step.
func WithMaxParallelTools(n int) Option { return func(a *BaseAgent) { a.maxParallelTools = n } }

// WithMaxEscalations sets the maximum recoverable retry attempts per turn.
func WithMaxEscalations(n int) Option { return func(a *BaseAgent) { a.maxEscalations = n } }

// WithHooks appends one or more hooks to the agent's hook runner.
func WithHooks(hs ...hook.Hook) Option {
	return func(a *BaseAgent) {
		for _, h := range hs {
			a.hooks.Register(h)
		}
	}
}

// WithHookRunner replaces the agent's hook runner.
func WithHookRunner(h *hook.Runner) Option { return func(a *BaseAgent) { a.hooks = h } }

// WithApprovalManager configures a reusable approval manager for gated tool execution.
func WithApprovalManager(
	m *approval.Manager,
) Option {
	return func(a *BaseAgent) { a.approvals = m }
}

// WithBuilder replaces the agent's context builder pipeline.
func WithBuilder(b *ccontext.Builder) Option { return func(a *BaseAgent) { a.builder = b } }

// WithRequestProcessors inserts preview-safe request processors into the
// default builder chain, placed before Capabilities (which must run last).
func WithRequestProcessors(ps ...ccontext.RequestProcessor) Option {
	return func(a *BaseAgent) {
		a.builder.InsertRequestProcessorsBeforeLast(ps...)
	}
}

// WithMutators inserts commit-time mutators into the default builder chain,
// preserving mutator order ahead of request shaping during commit builds.
func WithMutators(ms ...ccontext.ContextMutator) Option {
	return func(a *BaseAgent) {
		a.builder.AppendMutators(ms...)
	}
}

// WithBudgetGuard halts turns cleanly once the session's accumulated cost hits
// the configured budget limit.
func WithBudgetGuard(limit float64) Option {
	return WithRequestProcessors(governor.NewBudgetGuard(limit))
}

// WithModel overrides the model used for LLM calls.
func WithModel(m string) Option { return func(a *BaseAgent) { a.model = m } }

// New creates a BaseAgent with a default context builder chain.
// Optional opts are applied after defaults are set.
func New(
	id, instructions, model string,
	p llm.Provider,
	t *tool.Registry,
	opts ...Option,
) *BaseAgent {
	a := &BaseAgent{
		agentID:          id,
		instructions:     instructions,
		model:            model,
		maxSteps:         10,
		maxEscalations:   2,
		maxParallelTools: 10,
		provider:         p,
		tools:            t,
		hooks:            hook.NewRunner(),
	}

	a.builder = ccontext.NewBuilder(
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
