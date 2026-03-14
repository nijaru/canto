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
	Provider     llm.Provider
	Tools        *tool.Registry
}

// New creates a new agent.
func New(id, instructions, model string, p llm.Provider, t *tool.Registry) *Agent {
	return &Agent{
		ID:           id,
		Instructions: instructions,
		Model:        model,
		Provider:     p,
		Tools:        t,
	}
}
