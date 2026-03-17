package openrouter

import (
	"context"
	"net/http"
	"os"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	sashaoai "github.com/sashabaranov/go-openai"
)

type headerTransport struct {
	http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.RoundTripper.RoundTrip(req)
}

// Provider implements the llm.Provider interface for OpenRouter.
type Provider struct {
	openai.Base
}

// NewProvider creates a new OpenRouter provider from a catwalk configuration.
func NewProvider(cfg catwalk.Provider) *Provider {
	apiKey := cfg.APIKey
	if apiKey == "$OPENROUTER_API_KEY" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}

	config := sashaoai.DefaultConfig(apiKey)
	if cfg.APIEndpoint != "" {
		config.BaseURL = cfg.APIEndpoint
	} else {
		config.BaseURL = "https://openrouter.ai/api/v1"
	}

	if len(cfg.DefaultHeaders) > 0 {
		transport := http.DefaultTransport
		config.HTTPClient = &http.Client{
			Transport: &headerTransport{
				RoundTripper: transport,
				headers:      cfg.DefaultHeaders,
			},
		}
	}

	return &Provider{
		Base: openai.Base{
			Client: sashaoai.NewClientWithConfig(config),
			Config: cfg,
		},
	}
}

func (p *Provider) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return p.Base.Generate(ctx, req)
}

func (p *Provider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return p.Base.Stream(ctx, req)
}
