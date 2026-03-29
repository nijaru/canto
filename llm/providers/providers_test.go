package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nijaru/canto/llm"
)

func TestNewOpenAICompatible_CustomProvider(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(
			w,
			`{"id":"resp_1","object":"chat.completion","created":1,"model":"custom-1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		)
	}))
	defer server.Close()

	p, err := NewOpenAICompatible(OpenAICompatibleConfig{
		ID:            "custom-gateway",
		APIKey:        "secret",
		Endpoint:      server.URL,
		APIKeyEnvVars: []string{"CUSTOM_GATEWAY_API_KEY"},
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatible: %v", err)
	}

	resp, err := p.Generate(context.Background(), &llm.Request{
		Model: "custom-1",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if authHeader != "Bearer secret" {
		t.Fatalf("Authorization = %q, want Bearer secret", authHeader)
	}
	if resp.Content != "ok" {
		t.Fatalf("Content = %q, want ok", resp.Content)
	}
}

func TestNewOpenAICompatible_RequiresID(t *testing.T) {
	_, err := NewOpenAICompatible(OpenAICompatibleConfig{})
	if err == nil {
		t.Fatal("expected missing ID to fail")
	}
}
