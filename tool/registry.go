package tool

import (
	"context"
	"fmt"
	"sync"

	"github.com/nijaru/canto/llm"
)

// Registry manages the available tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Spec().Name] = t
}

// Get returns a tool by its name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Specs returns all tool specifications.
func (r *Registry) Specs() []llm.ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]llm.ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		res = append(res, t.Spec())
	}
	return res
}

// Execute looks up and runs a tool.
func (r *Registry) Execute(ctx context.Context, name, args string) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("tool not found: %s", name)
	}
	return t.Execute(ctx, args)
}
