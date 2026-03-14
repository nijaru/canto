package agent

import (
	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

// Agent is a configured LLM that can perform tasks.
type Agent struct {
	ID           string
	Instructions string
	Model        string
	MaxSteps     int // Maximum tool-calling steps per turn
	Provider     llm.Provider
	Tools        *tool.Registry
	Builder      *ccontext.Builder
}

// New creates a new agent with a default context builder chain.
func New(id, instructions, model string, p llm.Provider, t *tool.Registry) *Agent {
	a := &Agent{
		ID:           id,
		Instructions: instructions,
		Model:        model,
		MaxSteps:     10, // Default safety break
		Provider:     p,
		Tools:        t,
	}

	a.Builder = ccontext.NewBuilder(
		ccontext.InstructionProcessor(instructions),
		ccontext.ToolProcessor(t),
		ccontext.HistoryProcessor(),
	)

	return a
}
