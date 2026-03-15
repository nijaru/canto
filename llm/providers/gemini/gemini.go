package gemini

import (
	"context"
	"os"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	sashaoai "github.com/sashabaranov/go-openai"
)

// Provider implements the llm.Provider interface for Gemini via its OpenAI-compatible endpoint.
type Provider struct {
	openai.Base
}

// NewProvider creates a new Gemini provider from a catwalk configuration.
func NewProvider(cfg catwalk.Provider) *Provider {
	apiKey := cfg.APIKey
	if apiKey == "$GEMINI_API_KEY" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}

	config := sashaoai.DefaultConfig(apiKey)
	if cfg.APIEndpoint != "" {
		config.BaseURL = cfg.APIEndpoint
	} else {
		config.BaseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"
	}

	return &Provider{
		Base: openai.Base{
			Client: sashaoai.NewClientWithConfig(config),
			Config: cfg,
		},
	}
}

func (p *Provider) Generate(ctx context.Context, req *llm.LLMRequest) (*llm.LLMResponse, error) {
	return p.Base.Generate(ctx, req)
}

func (p *Provider) Stream(ctx context.Context, req *llm.LLMRequest) (llm.Stream, error) {
	return p.Base.Stream(ctx, req)
}
