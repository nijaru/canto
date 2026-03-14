package agent

import (
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
}

// New creates a new agent.
func New(id, instructions, model string, p llm.Provider, t *tool.Registry) *Agent {
	return &Agent{
		ID:           id,
		Instructions: instructions,
		Model:        model,
		MaxSteps:     10, // Default safety break
		Provider:     p,
		Tools:        t,
	}
}
