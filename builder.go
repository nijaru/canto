package canto

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// HarnessBuilder assembles the common agent + runner wiring while preserving
// access to the underlying primitives.
type HarnessBuilder struct {
	id           string
	instructions string
	model        string
	provider     llm.Provider
	registry     *tool.Registry
	tools        []tool.Tool
	store        session.Store
	ephemeral    bool
	compaction   *governor.CompactOptions
	environment  Environment

	agentOptions   []agent.Option
	runtimeOptions []runtime.Option
}

// NewHarness starts an authoring builder for a Canto harness.
func NewHarness(id string) *HarnessBuilder {
	return &HarnessBuilder{
		id: id,
	}
}

func (b *HarnessBuilder) Instructions(instructions string) *HarnessBuilder {
	b.instructions = instructions
	return b
}

func (b *HarnessBuilder) Model(model string) *HarnessBuilder {
	b.model = model
	return b
}

func (b *HarnessBuilder) Provider(provider llm.Provider) *HarnessBuilder {
	b.provider = provider
	return b
}

func (b *HarnessBuilder) Registry(registry *tool.Registry) *HarnessBuilder {
	b.registry = registry
	return b
}

func (b *HarnessBuilder) Tool(t tool.Tool) *HarnessBuilder {
	if t != nil {
		b.tools = append(b.tools, t)
	}
	return b
}

func (b *HarnessBuilder) Tools(tools ...tool.Tool) *HarnessBuilder {
	for _, t := range tools {
		b.Tool(t)
	}
	return b
}

func (b *HarnessBuilder) ToolSet(tools []tool.Tool) *HarnessBuilder {
	return b.Tools(tools...)
}

func (b *HarnessBuilder) SessionStore(store session.Store) *HarnessBuilder {
	b.store = store
	b.ephemeral = false
	return b
}

func (b *HarnessBuilder) Environment(env Environment) *HarnessBuilder {
	b.environment = cloneEnvironment(env)
	return b
}

// Ephemeral uses an in-memory SQLite session store. This is useful for tests,
// examples, and short-lived tools where session durability is not needed.
func (b *HarnessBuilder) Ephemeral() *HarnessBuilder {
	b.store = nil
	b.ephemeral = true
	return b
}

func (b *HarnessBuilder) AgentOptions(opts ...agent.Option) *HarnessBuilder {
	b.agentOptions = append(b.agentOptions, opts...)
	return b
}

func (b *HarnessBuilder) RuntimeOptions(opts ...runtime.Option) *HarnessBuilder {
	b.runtimeOptions = append(b.runtimeOptions, opts...)
	return b
}

func (b *HarnessBuilder) Approvals(manager *approval.Gate) *HarnessBuilder {
	if manager != nil {
		b.agentOptions = append(b.agentOptions, agent.WithApprovalGate(manager))
	}
	return b
}

func (b *HarnessBuilder) Hooks(hooks *hook.Runner) *HarnessBuilder {
	if hooks != nil {
		b.agentOptions = append(b.agentOptions, agent.WithHookRunner(hooks))
		b.runtimeOptions = append(b.runtimeOptions, runtime.WithHooks(hooks))
	}
	return b
}

func (b *HarnessBuilder) RequestProcessors(
	processors ...prompt.RequestProcessor,
) *HarnessBuilder {
	if len(processors) > 0 {
		b.agentOptions = append(b.agentOptions, agent.WithRequestProcessors(processors...))
	}
	return b
}

func (b *HarnessBuilder) Mutators(mutators ...prompt.ContextMutator) *HarnessBuilder {
	if len(mutators) > 0 {
		b.agentOptions = append(b.agentOptions, agent.WithMutators(mutators...))
	}
	return b
}

// Compaction enables proactive compaction before each runner execution and
// overflow recovery retry on context overflow errors.
func (b *HarnessBuilder) Compaction(opts governor.CompactOptions) *HarnessBuilder {
	b.compaction = &opts
	return b
}

func (b *HarnessBuilder) Build() (*Harness, error) {
	if b == nil {
		return nil, fmt.Errorf("canto harness: nil builder")
	}
	if b.id == "" {
		return nil, fmt.Errorf("canto harness: agent id is required")
	}
	if b.provider == nil {
		return nil, fmt.Errorf("canto harness: provider is required")
	}
	if b.model == "" {
		return nil, fmt.Errorf("canto harness: model is required")
	}
	if b.compaction != nil {
		if err := validateCompactionOptions(*b.compaction); err != nil {
			return nil, err
		}
	}

	registry := b.registry
	if registry == nil {
		registry = tool.NewRegistry()
	}
	for _, t := range b.tools {
		registry.Register(t)
	}

	store := b.store
	ownsStore := false
	if store == nil {
		if !b.ephemeral {
			return nil, fmt.Errorf(
				"canto harness: session store is required; call SessionStore or Ephemeral",
			)
		}
		var err error
		store, err = session.NewSQLiteStore(":memory:")
		if err != nil {
			return nil, fmt.Errorf("canto harness: ephemeral session store: %w", err)
		}
		ownsStore = true
	}

	provider := b.provider
	runtimeOptions := append([]runtime.Option(nil), b.runtimeOptions...)
	if b.compaction != nil {
		opts := *b.compaction
		compact := func(ctx context.Context, sess *session.Session) error {
			_, err := governor.CompactSession(ctx, provider, b.model, sess, opts)
			return err
		}
		runtimeOptions = append(runtimeOptions,
			runtime.WithBeforeRun(compact),
			runtime.WithOverflowRecovery(provider.IsContextOverflow, compact, 1),
		)
	}

	a := agent.New(
		b.id,
		b.instructions,
		b.model,
		provider,
		registry,
		b.agentOptions...,
	)
	return &Harness{
		Agent:       a,
		Runner:      runtime.NewRunner(store, a, runtimeOptions...),
		Tools:       registry,
		Store:       store,
		Environment: cloneEnvironment(b.environment),
		ownsStore:   ownsStore,
	}, nil
}

func cloneEnvironment(env Environment) Environment {
	env.Bootstrap = append([]session.ContextEntry(nil), env.Bootstrap...)
	return env
}

func validateCompactionOptions(opts governor.CompactOptions) error {
	if opts.MaxTokens <= 0 {
		return fmt.Errorf("canto harness: compaction max tokens must be > 0")
	}
	if (opts.Artifacts == nil) == (opts.OffloadDir == "") {
		return fmt.Errorf("canto harness: compaction requires exactly one offload target")
	}
	return nil
}
