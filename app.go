// Package canto provides a small authoring surface over Canto's core
// primitives. The lower-level packages remain the source of truth.
package canto

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/approval"
	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// App is the assembled authoring surface: an agent, runner, registry, and
// session store. Callers can use the fields directly for full composition.
type App struct {
	Agent  agent.Agent
	Runner *runtime.Runner
	Tools  *tool.Registry
	Store  session.Store
}

// Send appends a user message and executes one agent turn through the Runner.
func (a *App) Send(
	ctx context.Context,
	sessionID string,
	message string,
) (agent.StepResult, error) {
	if a == nil || a.Runner == nil {
		return agent.StepResult{}, fmt.Errorf("canto app: nil runner")
	}
	return a.Runner.Send(ctx, sessionID, message)
}

// SendStream appends a user message and executes one streaming agent turn.
func (a *App) SendStream(
	ctx context.Context,
	sessionID string,
	message string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if a == nil || a.Runner == nil {
		return agent.StepResult{}, fmt.Errorf("canto app: nil runner")
	}
	return a.Runner.SendStream(ctx, sessionID, message, chunkFn)
}

// Run executes the agent on an existing session through the Runner.
func (a *App) Run(ctx context.Context, sessionID string) (agent.StepResult, error) {
	if a == nil || a.Runner == nil {
		return agent.StepResult{}, fmt.Errorf("canto app: nil runner")
	}
	return a.Runner.Run(ctx, sessionID)
}

// Close releases resources owned by the runner and store when supported.
func (a *App) Close() error {
	if a == nil {
		return nil
	}
	if a.Runner != nil {
		a.Runner.Close()
	}
	if closer, ok := a.Store.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// AgentBuilder assembles the common agent + runner wiring while preserving
// access to the underlying primitives.
type AgentBuilder struct {
	id           string
	instructions string
	model        string
	provider     llm.Provider
	registry     *tool.Registry
	tools        []tool.Tool
	store        session.Store

	agentOptions   []agent.Option
	runtimeOptions []runtime.Option
}

// NewAgent starts an authoring builder for a Canto app.
func NewAgent(id string) *AgentBuilder {
	return &AgentBuilder{
		id: id,
	}
}

func (b *AgentBuilder) Instructions(instructions string) *AgentBuilder {
	b.instructions = instructions
	return b
}

func (b *AgentBuilder) Model(model string) *AgentBuilder {
	b.model = model
	return b
}

func (b *AgentBuilder) Provider(provider llm.Provider) *AgentBuilder {
	b.provider = provider
	return b
}

func (b *AgentBuilder) Registry(registry *tool.Registry) *AgentBuilder {
	b.registry = registry
	return b
}

func (b *AgentBuilder) Tool(t tool.Tool) *AgentBuilder {
	if t != nil {
		b.tools = append(b.tools, t)
	}
	return b
}

func (b *AgentBuilder) Tools(tools ...tool.Tool) *AgentBuilder {
	for _, t := range tools {
		b.Tool(t)
	}
	return b
}

func (b *AgentBuilder) ToolSet(tools []tool.Tool) *AgentBuilder {
	return b.Tools(tools...)
}

func (b *AgentBuilder) SessionStore(store session.Store) *AgentBuilder {
	b.store = store
	return b
}

func (b *AgentBuilder) AgentOptions(opts ...agent.Option) *AgentBuilder {
	b.agentOptions = append(b.agentOptions, opts...)
	return b
}

func (b *AgentBuilder) RuntimeOptions(opts ...runtime.Option) *AgentBuilder {
	b.runtimeOptions = append(b.runtimeOptions, opts...)
	return b
}

func (b *AgentBuilder) Approvals(manager *approval.Manager) *AgentBuilder {
	if manager != nil {
		b.agentOptions = append(b.agentOptions, agent.WithApprovalManager(manager))
	}
	return b
}

func (b *AgentBuilder) Hooks(hooks *hook.Runner) *AgentBuilder {
	if hooks != nil {
		b.agentOptions = append(b.agentOptions, agent.WithHookRunner(hooks))
		b.runtimeOptions = append(b.runtimeOptions, runtime.WithHooks(hooks))
	}
	return b
}

func (b *AgentBuilder) RequestProcessors(
	processors ...ccontext.RequestProcessor,
) *AgentBuilder {
	if len(processors) > 0 {
		b.agentOptions = append(b.agentOptions, agent.WithRequestProcessors(processors...))
	}
	return b
}

func (b *AgentBuilder) Mutators(mutators ...ccontext.ContextMutator) *AgentBuilder {
	if len(mutators) > 0 {
		b.agentOptions = append(b.agentOptions, agent.WithMutators(mutators...))
	}
	return b
}

func (b *AgentBuilder) Build() (*App, error) {
	if b == nil {
		return nil, fmt.Errorf("canto app: nil builder")
	}
	if b.id == "" {
		return nil, fmt.Errorf("canto app: agent id is required")
	}
	if b.provider == nil {
		return nil, fmt.Errorf("canto app: provider is required")
	}
	if b.model == "" {
		return nil, fmt.Errorf("canto app: model is required")
	}

	registry := b.registry
	if registry == nil {
		registry = tool.NewRegistry()
	}
	for _, t := range b.tools {
		registry.Register(t)
	}

	store := b.store
	if store == nil {
		var err error
		store, err = session.NewSQLiteStore(":memory:")
		if err != nil {
			return nil, fmt.Errorf("canto app: default session store: %w", err)
		}
	}

	a := agent.New(
		b.id,
		b.instructions,
		b.model,
		b.provider,
		registry,
		b.agentOptions...,
	)
	return &App{
		Agent:  a,
		Runner: runtime.NewRunner(store, a, b.runtimeOptions...),
		Tools:  registry,
		Store:  store,
	}, nil
}
