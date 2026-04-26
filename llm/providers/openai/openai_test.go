package openai

import (
	"testing"

	"github.com/nijaru/canto/llm"
)

func TestNewProviderDefaults(t *testing.T) {
	p := NewProvider(llm.ProviderConfig{})

	if got, want := p.ID(), "openai"; got != want {
		t.Fatalf("ID = %q, want %q", got, want)
	}
	if got, want := p.Config.APIEndpoint, "https://api.openai.com/v1"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	if caps := p.Capabilities("o4-mini"); !caps.ReasoningEffort {
		t.Fatal("expected OpenAI reasoning model capability defaults")
	}
}

func TestNewProviderRespectsConfig(t *testing.T) {
	models := []llm.Model{{ID: "custom"}}
	p := NewProvider(llm.ProviderConfig{
		ID:          "openai-custom",
		APIEndpoint: "https://example.test/v1",
		Models:      models,
	})

	if got, want := p.ID(), "openai-custom"; got != want {
		t.Fatalf("ID = %q, want %q", got, want)
	}
	if got, want := p.Config.APIEndpoint, "https://example.test/v1"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	gotModels, err := p.Models(t.Context())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(gotModels) != 1 || gotModels[0].ID != "custom" {
		t.Fatalf("models = %#v, want custom", gotModels)
	}
}
