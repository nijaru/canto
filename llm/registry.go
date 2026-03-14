package llm

import (
	"context"
	"fmt"
	"sync"

	"charm.land/catwalk/pkg/catwalk"
)

// Registry manages the available LLM providers and models.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	models    map[string]catwalk.Model // modelID -> Model
}

// NewRegistry creates a new registry.
func NewRegistry() *Registry {
	r := &Registry{
		providers: make(map[string]Provider),
		models:    make(map[string]catwalk.Model),
	}
	return r
}

// Register adds a provider to the registry.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.ID()] = p
}

// GetProvider returns a provider by its ID.
func (r *Registry) GetProvider(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	return p, ok
}

// AllProviders returns all registered providers.
func (r *Registry) AllProviders() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		res = append(res, p)
	}
	return res
}

// Sync fetches models and provider configurations from catwalk.
// This updates the internal model registry.
func (r *Registry) Sync(ctx context.Context) error {
	client := catwalk.New()
	providers, err := client.GetProviders(ctx, "")
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, p := range providers {
		for _, m := range p.Models {
			r.models[m.ID] = m
		}
	}

	return nil
}

// ResolveModel finds which provider can handle the given model ID.
func (r *Registry) ResolveModel(modelID string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 1. Lookup in model registry to find provider ID
	// 2. Lookup provider in registry
	
	for _, p := range r.providers {
		models, err := p.Models(context.Background())
		if err != nil {
			continue
		}
		for _, m := range models {
			if m.ID == modelID {
				return p, nil
			}
		}
	}

	return nil, fmt.Errorf("model not found: %s", modelID)
}
