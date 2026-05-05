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
	if caps := p.Capabilities("o4-mini"); caps.Reasoning.Kind != llm.ReasoningKindEffort {
		t.Fatal("expected OpenAI reasoning model capability defaults")
	} else if !caps.SupportsReasoningEffort("high") || !caps.SupportsReasoningEffort("none") {
		t.Fatalf("unexpected OpenAI reasoning capabilities: %#v", caps.Reasoning)
	}
}

func TestCompatibleProviderDefaultsToNoReasoningCaps(t *testing.T) {
	p := NewCompatibleProvider(llm.ProviderConfig{ID: "local-api"}, CompatibleSpec{
		ID:                 "local-api",
		DefaultAPIEndpoint: "http://localhost:8080/v1",
	})

	if caps := p.Capabilities("o4-mini"); caps.Reasoning.Kind != llm.ReasoningKindNone ||
		caps.SupportsReasoningEffort("high") {
		t.Fatalf("compatible provider caps = %#v, want no reasoning by default", caps)
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
