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

// New creates a BaseAgent with a default context builder chain.
func New(id, instructions, model string, p llm.Provider, t *tool.Registry) *BaseAgent {
	a := &BaseAgent{
		agentID:      id,
		Instructions: instructions,
		Model:        model,
		MaxSteps:     10, // Default safety break
		Provider:     p,
		Tools:        t,
		Hooks:        hook.NewRunner(),
	}

	a.Builder = ccontext.NewBuilder(
		ccontext.InstructionProcessor(instructions),
		ccontext.ToolProcessor(t),
		ccontext.HistoryProcessor(),
	)

	return a
}
