package openai

import (
	"context"
	"os"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/sashabaranov/go-openai"
)

// Provider implements the llm.Provider interface for OpenAI.
type Provider struct {
	Base
}

// New creates an OpenAI provider with the given API key.
// Use NewProvider for full catwalk configuration control.
func New(apiKey string) *Provider {
	return &Provider{
		Base: Base{
			Client: openai.NewClient(apiKey),
			Config: catwalk.Provider{ID: "openai", APIKey: apiKey},
		},
	}
}

// NewProvider creates a new OpenAI provider from a catwalk configuration.
func NewProvider(cfg catwalk.Provider) *Provider {
	apiKey := cfg.APIKey
	if apiKey == "$OPENAI_API_KEY" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	config := openai.DefaultConfig(apiKey)
	if cfg.APIEndpoint != "" {
		config.BaseURL = cfg.APIEndpoint
	}

	return &Provider{
		Base: Base{
			Client: openai.NewClientWithConfig(config),
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
