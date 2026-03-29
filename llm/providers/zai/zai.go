package zai

import (
	"context"
	"os"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	sashaoai "github.com/sashabaranov/go-openai"
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
	apiKey := cfg.APIKey
	if apiKey == "$ZAI_API_KEY" {
		apiKey = os.Getenv("ZAI_API_KEY")
	}

	if cfg.ID == "" {
		cfg.ID = "zai"
	}
	if cfg.APIEndpoint == "" {
		cfg.APIEndpoint = defaultAPIEndpoint
	}

	config := sashaoai.DefaultConfig(apiKey)
	config.BaseURL = cfg.APIEndpoint

	return &Provider{
		Base: openai.Base{
			Client:    sashaoai.NewClientWithConfig(config),
			Config:    cfg,
			ModelCaps: openai.DefaultModelCaps(),
		},
	}
}

func (p *Provider) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return p.Base.Generate(ctx, req)
}

func (p *Provider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return p.Base.Stream(ctx, req)
}
