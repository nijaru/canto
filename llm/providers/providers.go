package providers

import (
	"fmt"
	"strings"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/anthropic"
	"github.com/nijaru/canto/llm/providers/gemini"
	"github.com/nijaru/canto/llm/providers/ollama"
	openaipkg "github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/llm/providers/openrouter"
)

type Family string

const (
	FamilyAnthropic        Family = "anthropic"
	FamilyOpenAICompatible Family = "openai-compatible"
	FamilyOpenRouter       Family = "openrouter"
	FamilyGemini           Family = "gemini"
	FamilyOllama           Family = "ollama"
)

type Definition struct {
	ID              string
	Family          Family
	DefaultEnvVar   string
	AlternateEnvVars []string
	DefaultEndpoint string
	DefaultHeaders  map[string]string
	Aliases         []string
}

type Option func(*catwalk.Provider)

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

func All() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}

func Lookup(id string) (Definition, bool) {
	needle := normalize(id)
	for _, def := range definitions {
		if normalize(def.ID) == needle {
			return def, true
		}
		for _, alias := range def.Aliases {
			if normalize(alias) == needle {
				return def, true
			}
		}
	}
	return Definition{}, false
}

func New(id string, opts ...Option) (llm.Provider, error) {
	def, ok := Lookup(id)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", id)
	}
	return newFromDefinition(def, opts...)
}

func NewOpenAICompatible(def Definition, opts ...Option) (llm.Provider, error) {
	if def.ID == "" {
		return nil, fmt.Errorf("provider id is required")
	}
	if def.Family == "" {
		def.Family = FamilyOpenAICompatible
	}
	if def.Family != FamilyOpenAICompatible {
		return nil, fmt.Errorf("provider %q is not openai-compatible", def.ID)
	}
	cfg := buildConfig(def, opts)
	return openaipkg.NewCompatibleProvider(cfg, openaipkg.CompatibleSpec{
		ID:                 catwalk.InferenceProvider(def.ID),
		DefaultAPIEndpoint: def.DefaultEndpoint,
		APIKeyEnvVars:      envVars(def),
		DefaultHeaders:     def.DefaultHeaders,
		ModelCaps:          openaipkg.DefaultModelCaps(),
	}), nil
}

func newFromDefinition(def Definition, opts ...Option) (llm.Provider, error) {
	cfg := buildConfig(def, opts)
	switch def.Family {
	case FamilyAnthropic:
		return anthropic.NewProvider(cfg), nil
	case FamilyOpenAICompatible:
		return openaipkg.NewCompatibleProvider(cfg, openaipkg.CompatibleSpec{
			ID:                 catwalk.InferenceProvider(def.ID),
			DefaultAPIEndpoint: def.DefaultEndpoint,
			APIKeyEnvVars:      envVars(def),
			DefaultHeaders:     def.DefaultHeaders,
			ModelCaps:          openaipkg.DefaultModelCaps(),
		}), nil
	case FamilyOpenRouter:
		return openrouter.NewProvider(cfg), nil
	case FamilyGemini:
		return gemini.NewProvider(cfg), nil
	case FamilyOllama:
		return ollama.NewProvider(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported provider family %q", def.Family)
	}
}

func buildConfig(def Definition, opts []Option) catwalk.Provider {
	cfg := catwalk.Provider{
		ID:             catwalk.InferenceProvider(def.ID),
		APIEndpoint:    def.DefaultEndpoint,
		DefaultHeaders: cloneHeaders(def.DefaultHeaders),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func envVars(def Definition) []string {
	var out []string
	if def.DefaultEnvVar != "" {
		out = append(out, def.DefaultEnvVar)
	}
	out = append(out, def.AlternateEnvVars...)
	return out
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
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

var definitions = []Definition{
	{ID: "anthropic", Family: FamilyAnthropic, DefaultEnvVar: "ANTHROPIC_API_KEY"},
	{ID: "openai", Family: FamilyOpenAICompatible, DefaultEnvVar: "OPENAI_API_KEY", DefaultEndpoint: "https://api.openai.com/v1"},
	{ID: "openrouter", Family: FamilyOpenRouter, DefaultEnvVar: "OPENROUTER_API_KEY", DefaultEndpoint: "https://openrouter.ai/api/v1"},
	{ID: "gemini", Family: FamilyGemini, DefaultEnvVar: "GEMINI_API_KEY", AlternateEnvVars: []string{"GOOGLE_API_KEY"}, DefaultEndpoint: "https://generativelanguage.googleapis.com/v1beta/openai/"},
	{ID: "ollama", Family: FamilyOllama, DefaultEnvVar: "OLLAMA_API_KEY", DefaultEndpoint: "http://localhost:11434/v1"},
	{ID: "zai", Family: FamilyOpenAICompatible, DefaultEnvVar: "ZAI_API_KEY", DefaultEndpoint: "https://api.z.ai/api/coding/paas/v4", Aliases: []string{"z-ai"}},
	{ID: "deepseek", Family: FamilyOpenAICompatible, DefaultEnvVar: "DEEPSEEK_API_KEY", DefaultEndpoint: "https://api.deepseek.com/v1"},
	{ID: "together", Family: FamilyOpenAICompatible, DefaultEnvVar: "TOGETHER_API_KEY", DefaultEndpoint: "https://api.together.xyz/v1"},
	{ID: "groq", Family: FamilyOpenAICompatible, DefaultEnvVar: "GROQ_API_KEY", DefaultEndpoint: "https://api.groq.com/openai/v1"},
	{ID: "fireworks", Family: FamilyOpenAICompatible, DefaultEnvVar: "FIREWORKS_API_KEY", DefaultEndpoint: "https://api.fireworks.ai/inference/v1"},
	{ID: "mistral", Family: FamilyOpenAICompatible, DefaultEnvVar: "MISTRAL_API_KEY", DefaultEndpoint: "https://api.mistral.ai/v1"},
	{ID: "xai", Family: FamilyOpenAICompatible, DefaultEnvVar: "XAI_API_KEY", DefaultEndpoint: "https://api.x.ai/v1"},
	{ID: "moonshot", Family: FamilyOpenAICompatible, DefaultEnvVar: "MOONSHOT_API_KEY", DefaultEndpoint: "https://api.moonshot.ai/v1"},
	{ID: "cerebras", Family: FamilyOpenAICompatible, DefaultEnvVar: "CEREBRAS_API_KEY", DefaultEndpoint: "https://api.cerebras.ai/v1"},
	{ID: "huggingface", Family: FamilyOpenAICompatible, DefaultEnvVar: "HF_TOKEN", DefaultEndpoint: "https://router.huggingface.co/v1"},
}
