package gemini

import (
	"context"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
)

// Provider implements the llm.Provider interface for Gemini via its OpenAI-compatible endpoint.
type Provider struct {
	openai.Base
}

// NewProvider creates a new Gemini provider from a catwalk configuration.
func NewProvider(cfg catwalk.Provider) *Provider {
	p := openai.NewCompatibleProvider(cfg, openai.CompatibleSpec{
		ID:                 "gemini",
		DefaultAPIEndpoint: "https://generativelanguage.googleapis.com/v1beta/openai/",
		APIKeyEnvVars:      []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
	})
	return &Provider{Base: p.Base}
}

func (p *Provider) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return p.Base.Generate(ctx, req)
}

func (p *Provider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return p.Base.Stream(ctx, req)
}
