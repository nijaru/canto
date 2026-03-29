package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Registry manages the available LLM providers and models.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	models    map[string]Model    // modelID -> Model
	resolver  map[string][]string // modelID -> []providerID
}

// NewRegistry creates a new registry.
func NewRegistry() *Registry {
	r := &Registry{
		providers: make(map[string]Provider),
		models:    make(map[string]Model),
		resolver:  make(map[string][]string),
	}
	return r
}

// Register adds a provider to the registry.
func (r *Registry) Register(p Provider) {
	// Fetch models before acquiring the lock to avoid blocking concurrent
	// registry operations during a potentially slow network call.
	models, err := p.Models(context.Background())
	if err != nil {
		slog.Warn("registry: failed to fetch models", "provider", p.ID(), "error", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.ID()] = p

	for _, m := range models {
		r.models[m.ID] = m
		r.addResolver(m.ID, p.ID())
	}
}

func (r *Registry) addResolver(modelID, providerID string) {
	ids := r.resolver[modelID]
	for _, id := range ids {
		if id == providerID {
			return
		}
	}
	r.resolver[modelID] = append(ids, providerID)
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

// ResolveModel finds which provider(s) can handle the given model ID.
// Returns a SmartResolver if multiple providers are available.
func (r *Registry) ResolveModel(modelID string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providerIDs, ok := r.resolver[modelID]
	if !ok || len(providerIDs) == 0 {
		return nil, fmt.Errorf("model not found: %s", modelID)
	}

	if len(providerIDs) == 1 {
		provider, ok := r.providers[providerIDs[0]]
		if !ok {
			return nil, fmt.Errorf(
				"provider %s not registered for model %s",
				providerIDs[0],
				modelID,
			)
		}
		return provider, nil
	}

	// Multiple providers available, return a SmartResolver
	var providers []Provider
	for _, id := range providerIDs {
		if p, ok := r.providers[id]; ok {
			providers = append(providers, p)
		}
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no registered providers found for model %s", modelID)
	}

	if len(providers) == 1 {
		return providers[0], nil
	}

	return NewSmartResolver(StrategyPriority, providers...), nil
}
