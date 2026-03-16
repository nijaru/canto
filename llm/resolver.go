package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/sashabaranov/go-openai"
)

// IsRateLimit returns true if the error is a rate limit error (429).
func IsRateLimit(err error) bool {
	if err == nil {
		return false
	}

	// OpenAI
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatusCode == http.StatusTooManyRequests {
		return true
	}

	// Anthropic
	var anthropicErr *sdk.Error
	if errors.As(err, &anthropicErr) && anthropicErr.StatusCode == http.StatusTooManyRequests {
		return true
	}

	// Generic HTTP status check if provider wraps it
	type statusCoder interface {
		StatusCode() int
	}
	if sc, ok := err.(statusCoder); ok && sc.StatusCode() == http.StatusTooManyRequests {
		return true
	}

	return false
}

// Strategy defines how a SmartResolver picks providers.
type Strategy string

const (
	StrategyPriority   Strategy = "priority"    // Try in order, stick to first healthy
	StrategyRoundRobin Strategy = "round-robin" // Rotate keys for load balancing
)

// managedProvider wraps a Provider with health tracking.
type managedProvider struct {
	provider Provider
	cooling  time.Time
	failures int
}

// SmartResolver implements sophisticated key rotation and fallback.
// It tracks provider health and implements exponential backoff for cooling providers.
type SmartResolver struct {
	mu        sync.RWMutex
	providers []*managedProvider
	strategy  Strategy
	lastIdx   uint32
}

// NewSmartResolver creates a new smart resolver.
func NewSmartResolver(strategy Strategy, providers ...Provider) *SmartResolver {
	managed := make([]*managedProvider, len(providers))
	for i, p := range providers {
		managed[i] = &managedProvider{provider: p}
	}
	return &SmartResolver{
		providers: managed,
		strategy:  strategy,
	}
}

func (r *SmartResolver) ID() string {
	if len(r.providers) == 0 {
		return "smart"
	}
	return fmt.Sprintf("smart(%s)", r.providers[0].provider.ID())
}

func (r *SmartResolver) Generate(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	healthy := r.getHealthy()
	if len(healthy) == 0 {
		return nil, fmt.Errorf("all providers are cooling down")
	}

	var lastErr error
	for _, p := range healthy {
		resp, err := p.provider.Generate(ctx, req)
		if err == nil {
			r.markSuccess(p)
			return resp, nil
		}

		if IsRateLimit(err) {
			r.markCooling(p)
			continue
		}

		lastErr = err
		// Terminal error (not a rate limit), stop here
		return nil, lastErr
	}

	return nil, fmt.Errorf("all healthy providers exhausted or rate limited")
}

func (r *SmartResolver) Stream(ctx context.Context, req *LLMRequest) (Stream, error) {
	healthy := r.getHealthy()
	if len(healthy) == 0 {
		return nil, fmt.Errorf("all providers are cooling down")
	}

	var lastErr error
	for _, p := range healthy {
		s, err := p.provider.Stream(ctx, req)
		if err == nil {
			r.markSuccess(p)
			return s, nil
		}

		if IsRateLimit(err) {
			r.markCooling(p)
			continue
		}

		lastErr = err
		return nil, lastErr
	}

	return nil, fmt.Errorf("all healthy providers exhausted or rate limited")
}

func (r *SmartResolver) Models(ctx context.Context) ([]catwalk.Model, error) {
	seen := make(map[string]bool)
	var all []catwalk.Model
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		models, err := p.provider.Models(ctx)
		if err != nil {
			continue
		}
		for _, m := range models {
			if !seen[m.ID] {
				seen[m.ID] = true
				all = append(all, m)
			}
		}
	}
	return all, nil
}

func (r *SmartResolver) CountTokens(
	ctx context.Context,
	model string,
	messages []Message,
) (int, error) {
	healthy := r.getHealthy()
	if len(healthy) == 0 {
		// Fallback to first provider if none are healthy for counting
		r.mu.RLock()
		defer r.mu.RUnlock()
		if len(r.providers) > 0 {
			return r.providers[0].provider.CountTokens(ctx, model, messages)
		}
		return 0, fmt.Errorf("no providers available")
	}
	return healthy[0].provider.CountTokens(ctx, model, messages)
}

func (r *SmartResolver) Cost(ctx context.Context, model string, usage Usage) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		// Try to find the provider that actually has this model configuration
		models, err := p.provider.Models(ctx)
		if err != nil {
			continue
		}
		for _, m := range models {
			if string(m.ID) == model {
				return p.provider.Cost(ctx, model, usage)
			}
		}
	}
	// Fallback to first provider if model not found in any list
	if len(r.providers) > 0 {
		return r.providers[0].provider.Cost(ctx, model, usage)
	}
	return 0
}

// Capabilities returns the capabilities of the first healthy provider's
// view of the given model. Falls back to DefaultCapabilities if no providers
// are available.
func (r *SmartResolver) Capabilities(model string) Capabilities {
	providers := r.getHealthy()
	if len(providers) == 0 {
		return DefaultCapabilities()
	}
	return providers[0].provider.Capabilities(model)
}

func (r *SmartResolver) getHealthy() []*managedProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	var healthy []*managedProvider
	for _, p := range r.providers {
		if p.cooling.Before(now) {
			healthy = append(healthy, p)
		}
	}

	if len(healthy) > 1 && r.strategy == StrategyRoundRobin {
		idx := int(atomic.AddUint32(&r.lastIdx, 1) % uint32(len(healthy)))
		res := make([]*managedProvider, len(healthy))
		for i := 0; i < len(healthy); i++ {
			res[i] = healthy[(idx+i)%len(healthy)]
		}
		return res
	}

	return healthy
}

func (r *SmartResolver) markCooling(p *managedProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.failures++
	if p.failures > 10 {
		p.failures = 10 // Cap to prevent overflow (2^10 = 1024)
	}
	// Exponential backoff: 2^n seconds
	backoff := time.Duration(1<<uint(p.failures)) * time.Second
	if backoff > 5*time.Minute {
		backoff = 5 * time.Minute
	}
	p.cooling = time.Now().Add(backoff)
}

func (r *SmartResolver) markSuccess(p *managedProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.failures = 0
	p.cooling = time.Time{}
}

// FailoverProvider tries a list of providers in sequence until one succeeds.
type FailoverProvider struct {
	providers []Provider
}

// NewFailoverProvider creates a new failover provider.
func NewFailoverProvider(providers ...Provider) *FailoverProvider {
	return &FailoverProvider{providers: providers}
}

func (p *FailoverProvider) ID() string {
	if len(p.providers) == 0 {
		return "failover"
	}
	return fmt.Sprintf("failover(%s)", p.providers[0].ID())
}

func (p *FailoverProvider) Generate(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	var lastErr error
	for _, sub := range p.providers {
		resp, err := sub.Generate(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("failover failed: %w", lastErr)
}

func (p *FailoverProvider) Stream(ctx context.Context, req *LLMRequest) (Stream, error) {
	var lastErr error
	for _, sub := range p.providers {
		s, err := sub.Stream(ctx, req)
		if err == nil {
			return s, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("failover failed to start stream: %w", lastErr)
}

func (p *FailoverProvider) Models(ctx context.Context) ([]catwalk.Model, error) {
	seen := make(map[string]bool)
	var all []catwalk.Model
	for _, sub := range p.providers {
		models, err := sub.Models(ctx)
		if err != nil {
			continue
		}
		for _, m := range models {
			if !seen[m.ID] {
				seen[m.ID] = true
				all = append(all, m)
			}
		}
	}
	return all, nil
}

func (p *FailoverProvider) CountTokens(
	ctx context.Context,
	model string,
	messages []Message,
) (int, error) {
	if len(p.providers) == 0 {
		return 0, fmt.Errorf("no providers configured")
	}
	return p.providers[0].CountTokens(ctx, model, messages)
}

func (p *FailoverProvider) Cost(ctx context.Context, model string, usage Usage) float64 {
	for _, sub := range p.providers {
		models, err := sub.Models(ctx)
		if err != nil {
			continue
		}
		for _, m := range models {
			if string(m.ID) == model {
				return sub.Cost(ctx, model, usage)
			}
		}
	}
	if len(p.providers) > 0 {
		return p.providers[0].Cost(ctx, model, usage)
	}
	return 0
}
