package openrouter

import (
	"testing"

	"github.com/nijaru/canto/llm"
)

func TestNewProviderDefaults(t *testing.T) {
	p := NewProvider(llm.ProviderConfig{})

	if got, want := p.ID(), "openrouter"; got != want {
		t.Fatalf("ID = %q, want %q", got, want)
	}
	if got, want := p.Config.APIEndpoint, "https://openrouter.ai/api/v1"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestNewProviderRespectsConfig(t *testing.T) {
	p := NewProvider(llm.ProviderConfig{
		ID:          "openrouter-custom",
		APIEndpoint: "https://example.test/openrouter",
	})

	if got, want := p.ID(), "openrouter-custom"; got != want {
		t.Fatalf("ID = %q, want %q", got, want)
	}
	if got, want := p.Config.APIEndpoint, "https://example.test/openrouter"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}
