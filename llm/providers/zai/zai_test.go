package zai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
)

func TestNewProvider_Defaults(t *testing.T) {
	p := NewProvider(catwalk.Provider{})

	if got, want := p.ID(), "zai"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
	if got, want := p.Config.APIEndpoint, defaultAPIEndpoint; got != want {
		t.Fatalf("APIEndpoint = %q, want %q", got, want)
	}
}

func TestNewProvider_UsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "test-zai-key")

	var authHeader string
	var requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		requestPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !strings.Contains(string(body), `"model":"glm-5"`) {
			t.Fatalf("request body missing model: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","object":"chat.completion","created":1,"model":"glm-5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	}))
	defer server.Close()

	p := NewProvider(catwalk.Provider{
		ID:          "zai",
		APIKey:      "$ZAI_API_KEY",
		APIEndpoint: server.URL,
	})

	resp, err := p.Generate(context.Background(), &llm.Request{
		Model: "glm-5",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if authHeader != "Bearer test-zai-key" {
		t.Fatalf("Authorization header = %q, want Bearer test-zai-key", authHeader)
	}
	if requestPath != "/chat/completions" {
		t.Fatalf("request path = %q, want /chat/completions", requestPath)
	}
	if resp.Content != "ok" {
		t.Fatalf("Content = %q, want ok", resp.Content)
	}
}
