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
	resolver  map[string]string        // modelID -> providerID
}

// NewRegistry creates a new registry.
func NewRegistry() *Registry {
	r := &Registry{
		providers: make(map[string]Provider),
		models:    make(map[string]catwalk.Model),
		resolver:  make(map[string]string),
	}
	return r
}

// Register adds a provider to the registry.
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.ID()] = p

	// Pre-populate resolver from provider's static model list if available
	models, err := p.Models(context.Background())
	if err == nil {
		for _, m := range models {
			r.models[m.ID] = m
			r.resolver[m.ID] = p.ID()
		}
	}
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
			// Only update resolver if we have the provider registered
			if _, ok := r.providers[string(p.ID)]; ok {
				r.resolver[m.ID] = string(p.ID)
			}
		}
	}

	return nil
}

// ResolveModel finds which provider can handle the given model ID.
func (r *Registry) ResolveModel(modelID string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providerID, ok := r.resolver[modelID]
	if !ok {
		return nil, fmt.Errorf("model not found: %s", modelID)
	}

	provider, ok := r.providers[providerID]
	if !ok {
		return nil, fmt.Errorf("provider %s not registered for model %s", providerID, modelID)
	}

	return provider, nil
}

