package ollama

import (
	"context"
	"os"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	sashaoai "github.com/sashabaranov/go-openai"
)

// Provider implements the llm.Provider interface for Ollama.
// It uses Ollama's OpenAI-compatible API endpoint.
type Provider struct {
	openai.Base
}

// New creates an Ollama provider pointing at the default local endpoint.
// Use NewProvider for custom base URL or catwalk configuration.
func New() *Provider {
	return NewProvider(catwalk.Provider{ID: "ollama", APIEndpoint: "http://localhost:11434/v1"})
}

// NewProvider creates a new Ollama provider from a catwalk configuration.
func NewProvider(cfg catwalk.Provider) *Provider {
	apiKey := cfg.APIKey
	if apiKey == "" || apiKey == "$OLLAMA_API_KEY" {
		apiKey = os.Getenv("OLLAMA_API_KEY")
		if apiKey == "" {
			apiKey = "ollama" // Dummy key since Ollama doesn't require one by default
		}
	}

	config := sashaoai.DefaultConfig(apiKey)
	if cfg.APIEndpoint != "" {
		config.BaseURL = cfg.APIEndpoint
	} else {
		// Default to local Ollama with OpenAI compatibility path
		config.BaseURL = "http://localhost:11434/v1"
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
