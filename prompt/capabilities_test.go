package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
	xtesting "github.com/nijaru/canto/x/testing"
)

func TestCapabilitiesPreservesThinkingAndNormalizesToolIDs(t *testing.T) {
	req := &llm.Request{
		Messages: []llm.Message{
			{
				Role:      llm.RoleAssistant,
				Content:   "done",
				Reasoning: "because",
				ThinkingBlocks: []llm.ThinkingBlock{
					{Type: "thinking", Thinking: "step one"},
				},
			},
			{
				Role:    llm.RoleTool,
				Content: "result",
				ToolID:  "tool|abc 123",
			},
		},
	}
	call := llm.Call{
		ID:   "tool|abc 123",
		Type: "function",
	}
	call.Function.Name = "search"
	call.Function.Arguments = "{}"
	req.Messages[0].Calls = []llm.Call{call}

	provider := &capabilityProvider{
		FauxProvider: xtesting.NewFauxProvider("test"),
		caps: llm.Capabilities{
			SystemRole: llm.RoleSystem,
		},
	}

	if err := Capabilities().ApplyRequest(context.Background(), provider, "model", nil, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}

	got := req.Messages[0]
	if got.Reasoning != "" {
		t.Fatalf("expected reasoning to be cleared, got %q", got.Reasoning)
	}
	if len(got.ThinkingBlocks) != 0 {
		t.Fatalf("expected thinking blocks to be cleared, got %#v", got.ThinkingBlocks)
	}
	if !strings.Contains(got.Content, "done") {
		t.Fatalf("expected content to preserve assistant text, got %q", got.Content)
	}
	if !strings.Contains(got.Content, "<thinking>") {
		t.Fatalf("expected content to preserve thinking as text, got %q", got.Content)
	}
	if !strings.Contains(got.Content, "step one") {
		t.Fatalf("expected content to include thinking block text, got %q", got.Content)
	}

	if got := req.Messages[0].Calls[0].ID; got != "tool_abc_123" {
		t.Fatalf("expected normalized call id, got %q", got)
	}
	if got := req.Messages[1].ToolID; got != "tool_abc_123" {
		t.Fatalf("expected normalized tool id, got %q", got)
	}
}

type capabilityProvider struct {
	*xtesting.FauxProvider
	caps llm.Capabilities
}

func (p *capabilityProvider) Capabilities(string) llm.Capabilities {
	return p.caps
}
