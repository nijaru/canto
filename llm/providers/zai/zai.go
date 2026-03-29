package zai

import (
	"context"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
)

const defaultAPIEndpoint = "https://api.z.ai/api/coding/paas/v4"

// Provider implements the llm.Provider interface for Z.ai's coding endpoint.
type Provider struct {
	openai.Base
}

// New creates a Z.ai provider that targets the coding endpoint.
func New(apiKey string) *Provider {
	return NewProvider(catwalk.Provider{
		ID:          "zai",
		APIKey:      apiKey,
		APIEndpoint: defaultAPIEndpoint,
	})
}

// NewProvider creates a new Z.ai provider from a catwalk configuration.
func NewProvider(cfg catwalk.Provider) *Provider {
	p := openai.NewCompatibleProvider(cfg, openai.CompatibleSpec{
		ID:                 "zai",
		DefaultAPIEndpoint: defaultAPIEndpoint,
		APIKeyEnvVars:      []string{"ZAI_API_KEY"},
		ModelCaps:          openai.DefaultModelCaps(),
	})
	return &Provider{Base: p.Base}
}

func (p *Provider) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return p.Base.Generate(ctx, req)
}

func (p *Provider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return p.Base.Stream(ctx, req)
}
