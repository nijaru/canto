package openai

import (
	"context"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
)

// Provider implements the llm.Provider interface for OpenAI.
type Provider struct {
	Base
}

// New creates an OpenAI provider with the given API key.
// Use NewProvider for full catwalk configuration control.
func New(apiKey string) *Provider {
	return NewProvider(catwalk.Provider{ID: "openai", APIKey: apiKey})
}

// NewProvider creates a new OpenAI provider from a catwalk configuration.
func NewProvider(cfg catwalk.Provider) *Provider {
	return NewCompatibleProvider(cfg, CompatibleSpec{
		ID:                 "openai",
		DefaultAPIEndpoint: "https://api.openai.com/v1",
		APIKeyEnvVars:      []string{"OPENAI_API_KEY"},
		ModelCaps:          DefaultModelCaps(),
	})
}

func (p *Provider) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return p.Base.Generate(ctx, req)
}

func (p *Provider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return p.Base.Stream(ctx, req)
}
