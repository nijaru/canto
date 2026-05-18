package openai

import (
	"net/http"
	"net/http/httptest"
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

func TestGeneratePreservesReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"reasoning_content": "thinking through it",
					"content": "done"
				},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7}
		}`))
	}))
	defer server.Close()

	p := NewCompatibleProvider(llm.ProviderConfig{
		ID:          "local-api",
		APIEndpoint: server.URL + "/v1",
		APIKey:      "test",
	}, CompatibleSpec{ID: "local-api"})

	resp, err := p.Generate(t.Context(), &llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("content = %q, want done", resp.Content)
	}
	if resp.Reasoning != "thinking through it" {
		t.Fatalf("reasoning = %q, want thinking through it", resp.Reasoning)
	}
}

func TestStreamPreservesReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(
			[]byte(
				`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"thinking "}}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"through it"}}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"done"}}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}

data: [DONE]

`,
			),
		)
	}))
	defer server.Close()

	p := NewCompatibleProvider(llm.ProviderConfig{
		ID:          "local-api",
		APIEndpoint: server.URL + "/v1",
		APIKey:      "test",
	}, CompatibleSpec{ID: "local-api"})

	stream, err := p.Stream(t.Context(), &llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	resp, err := llm.GenerateFromStream(stream)
	if err != nil {
		t.Fatalf("GenerateFromStream: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("content = %q, want done", resp.Content)
	}
	if resp.Reasoning != "thinking through it" {
		t.Fatalf("reasoning = %q, want thinking through it", resp.Reasoning)
	}
	if resp.Usage.TotalTokens != 7 {
		t.Fatalf("total tokens = %d, want 7", resp.Usage.TotalTokens)
	}
}

func TestIsContextOverflowMessage(t *testing.T) {
	if !isContextOverflowMessage("This model's context window has too many TOKENS") {
		t.Fatal("expected mixed-case context/token message to match")
	}
	if isContextOverflowMessage("temporary server overload") {
		t.Fatal("expected unrelated message not to match")
	}
}
