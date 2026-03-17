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
func (r *Registry) Specs() []*llm.Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]*llm.Spec, 0, len(r.tools))
	for _, t := range r.tools {
		spec := t.Spec()
		res = append(res, &spec)
	}
	return res
}

// Names returns the names of all registered tools.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Execute looks up and runs a tool.
func (r *Registry) Execute(ctx context.Context, name, args string) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("tool not found: %s", name)
	}
	return t.Execute(ctx, args)
}
