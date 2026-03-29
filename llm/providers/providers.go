package providers

import (
	"fmt"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/anthropic"
	"github.com/nijaru/canto/llm/providers/gemini"
	"github.com/nijaru/canto/llm/providers/ollama"
	openaipkg "github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/llm/providers/openrouter"
)

type Option func(*catwalk.Provider)

type OpenAICompatibleConfig struct {
	ID            string
	Endpoint      string
	APIKeyEnvVars []string
	Headers       map[string]string
	Models        []catwalk.Model
	ModelCaps     map[string]llm.Capabilities
	DefaultAPIKey string
}

func WithAPIKey(apiKey string) Option {
	return func(cfg *catwalk.Provider) {
		cfg.APIKey = apiKey
	}
}

func WithEndpoint(endpoint string) Option {
	return func(cfg *catwalk.Provider) {
		cfg.APIEndpoint = endpoint
	}
}

func WithHeader(key, value string) Option {
	return func(cfg *catwalk.Provider) {
		if cfg.DefaultHeaders == nil {
			cfg.DefaultHeaders = make(map[string]string)
		}
		cfg.DefaultHeaders[key] = value
	}
}

func WithModels(models ...catwalk.Model) Option {
	return func(cfg *catwalk.Provider) {
		cfg.Models = append([]catwalk.Model(nil), models...)
	}
}

func NewAnthropic(opts ...Option) llm.Provider {
	return anthropic.NewProvider(buildConfig("anthropic", opts))
}

func NewOpenAI(opts ...Option) llm.Provider {
	return openaipkg.NewProvider(buildConfig("openai", opts))
}

func NewOpenRouter(opts ...Option) llm.Provider {
	return openrouter.NewProvider(buildConfig("openrouter", opts))
}

func NewGemini(opts ...Option) llm.Provider {
	return gemini.NewProvider(buildConfig("gemini", opts))
}

func NewOllama(opts ...Option) llm.Provider {
	return ollama.NewProvider(buildConfig("ollama", opts))
}

func NewOpenAICompatible(
	config OpenAICompatibleConfig,
	opts ...Option,
) (llm.Provider, error) {
	if config.ID == "" {
		return nil, fmt.Errorf("provider id is required")
	}

	cfg := catwalk.Provider{
		ID:             catwalk.InferenceProvider(config.ID),
		APIEndpoint:    config.Endpoint,
		DefaultHeaders: cloneHeaders(config.Headers),
		Models:         append([]catwalk.Model(nil), config.Models...),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return openaipkg.NewCompatibleProvider(cfg, openaipkg.CompatibleSpec{
		ID:                 catwalk.InferenceProvider(config.ID),
		DefaultAPIEndpoint: config.Endpoint,
		APIKeyEnvVars:      append([]string(nil), config.APIKeyEnvVars...),
		DefaultHeaders:     cloneHeaders(config.Headers),
		ModelCaps:          config.ModelCaps,
		DefaultAPIKey:      config.DefaultAPIKey,
	}), nil
}

func buildConfig(id string, opts []Option) catwalk.Provider {
	cfg := catwalk.Provider{ID: catwalk.InferenceProvider(id)}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func cloneHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
