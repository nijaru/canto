package llm

import "testing"

func TestTransformRequestForCapabilitiesFlattensThinkingNormalizesIDsAndSynthesizesResults(
	t *testing.T,
) {
	req := &Request{
		Temperature: 0.7,
		Messages: []Message{
			{Role: RoleSystem, Content: "follow instructions"},
			{
				Role:      RoleAssistant,
				Content:   "done",
				Reasoning: "because",
				ThinkingBlocks: []ThinkingBlock{
					{Type: "thinking", Thinking: "step one"},
					{Type: "redacted_thinking", Signature: "sig"},
				},
				Calls: []Call{{
					ID:   "tool|abc 123",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "search", Arguments: "{}"},
				}},
			},
		},
	}

	TransformRequestForCapabilities(req, Capabilities{
		SystemRole:  RoleUser,
		Temperature: false,
	})

	if got := req.Temperature; got != 0 {
		t.Fatalf("temperature = %v, want 0", got)
	}
	if got := req.Messages[0].Role; got != RoleUser {
		t.Fatalf("system role = %q, want user", got)
	}
	if got := req.Messages[0].Content; got != "Instructions:\nfollow instructions" {
		t.Fatalf("rewritten instructions = %q", got)
	}

	assistant := req.Messages[1]
	if assistant.Reasoning != "" || len(assistant.ThinkingBlocks) != 0 {
		t.Fatalf("assistant thinking fields not cleared: %#v", assistant)
	}
	if assistant.Calls[0].ID != "tool_abc_123" {
		t.Fatalf("normalized call id = %q", assistant.Calls[0].ID)
	}
	if assistant.Content == "" || assistant.Content == "done" {
		t.Fatalf("assistant content did not preserve thinking text: %q", assistant.Content)
	}
	if contains(assistant.Content, "redacted_thinking") {
		t.Fatalf("redacted thinking should be dropped, got %q", assistant.Content)
	}

	if len(req.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(req.Messages))
	}
	tool := req.Messages[2]
	if tool.Role != RoleTool {
		t.Fatalf("synthetic message role = %q, want tool", tool.Role)
	}
	if tool.ToolID != "tool_abc_123" {
		t.Fatalf("synthetic tool id = %q", tool.ToolID)
	}
	if tool.Content != missingToolResultContent {
		t.Fatalf("synthetic tool content = %q", tool.Content)
	}
}

func TestTransformRequestForCapabilitiesKeepsMatchedToolResultsWithoutDuplicates(t *testing.T) {
	req := &Request{
		Messages: []Message{
			{
				Role: RoleAssistant,
				Calls: []Call{{
					ID:   "call 1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "read_file", Arguments: `{"path":"README.md"}`},
				}},
			},
			{Role: RoleTool, ToolID: "call 1", Content: "contents"},
			{Role: RoleUser, Content: "continue"},
		},
	}

	TransformRequestForCapabilities(req, DefaultCapabilities())

	if len(req.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(req.Messages))
	}
	if got := req.Messages[0].Calls[0].ID; got != "call_1" {
		t.Fatalf("normalized call id = %q", got)
	}
	if got := req.Messages[1].ToolID; got != "call_1" {
		t.Fatalf("normalized tool id = %q", got)
	}
}

func TestTransformRequestForCapabilitiesDropsUnsupportedReasoningControls(t *testing.T) {
	req := &Request{
		ReasoningEffort: "high",
		ThinkingBudget:  4096,
		Messages:        []Message{{Role: RoleUser, Content: "hello"}},
	}

	TransformRequestForCapabilities(req, DefaultCapabilities())

	if req.ReasoningEffort != "" {
		t.Fatalf("reasoning effort = %q, want empty", req.ReasoningEffort)
	}
	if req.ThinkingBudget != 0 {
		t.Fatalf("thinking budget = %d, want 0", req.ThinkingBudget)
	}
}

func TestTransformRequestForCapabilitiesKeepsSupportedReasoningEffort(t *testing.T) {
	req := &Request{
		ReasoningEffort: "high",
		Messages:        []Message{{Role: RoleUser, Content: "hello"}},
	}

	TransformRequestForCapabilities(req, Capabilities{
		Reasoning: ReasoningCapabilities{
			Kind:       ReasoningKindEffort,
			Efforts:    []string{"low", "medium", "high"},
			CanDisable: true,
		},
	})

	if req.ReasoningEffort != "high" {
		t.Fatalf("reasoning effort = %q, want high", req.ReasoningEffort)
	}
}

func TestTransformRequestForCapabilitiesDropsUnsupportedReasoningEffortValue(t *testing.T) {
	req := &Request{
		ReasoningEffort: "xhigh",
		Messages:        []Message{{Role: RoleUser, Content: "hello"}},
	}

	TransformRequestForCapabilities(req, Capabilities{
		Reasoning: ReasoningCapabilities{
			Kind:    ReasoningKindEffort,
			Efforts: []string{"low", "medium", "high"},
		},
	})

	if req.ReasoningEffort != "" {
		t.Fatalf("reasoning effort = %q, want empty", req.ReasoningEffort)
	}
}

func TestTransformRequestForCapabilitiesKeepsSupportedThinkingBudget(t *testing.T) {
	req := &Request{
		ThinkingBudget: 4096,
		Messages:       []Message{{Role: RoleUser, Content: "hello"}},
	}

	TransformRequestForCapabilities(req, Capabilities{
		Reasoning: ReasoningCapabilities{
			Kind:            ReasoningKindBudget,
			BudgetMinTokens: 1024,
			BudgetMaxTokens: 8192,
		},
	})

	if req.ThinkingBudget != 4096 {
		t.Fatalf("thinking budget = %d, want 4096", req.ThinkingBudget)
	}
}

func TestTransformRequestForCapabilitiesDropsOutOfRangeThinkingBudget(t *testing.T) {
	req := &Request{
		ThinkingBudget: 512,
		Messages:       []Message{{Role: RoleUser, Content: "hello"}},
	}

	TransformRequestForCapabilities(req, Capabilities{
		Reasoning: ReasoningCapabilities{
			Kind:            ReasoningKindBudget,
			BudgetMinTokens: 1024,
		},
	})

	if req.ThinkingBudget != 0 {
		t.Fatalf("thinking budget = %d, want 0", req.ThinkingBudget)
	}
}

func TestTransformRequestForCapabilitiesSynthesizesBeforeNextAssistantMessage(t *testing.T) {
	req := &Request{
		Messages: []Message{
			{
				Role: RoleAssistant,
				Calls: []Call{{
					ID:   "call-a",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "search", Arguments: "{}"},
				}},
			},
			{Role: RoleAssistant, Content: "next turn"},
		},
	}

	TransformRequestForCapabilities(req, DefaultCapabilities())

	if len(req.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(req.Messages))
	}
	if req.Messages[1].Role != RoleTool {
		t.Fatalf("expected synthetic tool result before next assistant, got %#v", req.Messages)
	}
	if req.Messages[2].Role != RoleAssistant || req.Messages[2].Content != "next turn" {
		t.Fatalf("assistant ordering changed: %#v", req.Messages)
	}
}

func TestTransformRequestForCapabilitiesAdjustsCachePrefixForSyntheticToolResults(t *testing.T) {
	req := &Request{
		CachePrefixMessages: 2,
		Messages: []Message{
			{Role: RoleSystem, Content: "system"},
			{
				Role: RoleAssistant,
				Calls: []Call{{
					ID:   "call-a",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "search", Arguments: "{}"},
				}},
			},
			{Role: RoleUser, Content: "continue"},
		},
	}

	TransformRequestForCapabilities(req, DefaultCapabilities())

	if req.CachePrefixMessages != 3 {
		t.Fatalf("cache prefix messages = %d, want 3", req.CachePrefixMessages)
	}
	if len(req.Messages) != 4 || req.Messages[2].Role != RoleTool {
		t.Fatalf("expected synthetic tool result inside prefix, got %#v", req.Messages)
	}
	if req.Messages[3].Content != "continue" {
		t.Fatalf("expected user suffix after prefix, got %#v", req.Messages)
	}
}

func TestTransformRequestForCapabilitiesKeepsCachePrefixForSuffixToolResults(t *testing.T) {
	req := &Request{
		CachePrefixMessages: 1,
		Messages: []Message{
			{Role: RoleSystem, Content: "system"},
			{
				Role: RoleAssistant,
				Calls: []Call{{
					ID:   "call-a",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "search", Arguments: "{}"},
				}},
			},
			{Role: RoleUser, Content: "continue"},
		},
	}

	TransformRequestForCapabilities(req, DefaultCapabilities())

	if req.CachePrefixMessages != 1 {
		t.Fatalf("cache prefix messages = %d, want 1", req.CachePrefixMessages)
	}
	if len(req.Messages) != 4 || req.Messages[2].Role != RoleTool {
		t.Fatalf("expected synthetic tool result in suffix, got %#v", req.Messages)
	}
}

func TestPrepareRequestForCapabilitiesLeavesOriginalReusable(t *testing.T) {
	req := &Request{
		Temperature: 0.7,
		Messages: []Message{
			{Role: RoleSystem, Content: "system"},
			{
				Role:      RoleAssistant,
				Content:   "answer",
				Reasoning: "because",
				ThinkingBlocks: []ThinkingBlock{
					{Type: "thinking", Thinking: "step"},
				},
				Calls: []Call{{
					ID:   "call 1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "search", Arguments: "{}"},
				}},
			},
		},
	}

	openAIReady, err := PrepareRequestForCapabilities(req, Capabilities{
		SystemRole:  RoleDeveloper,
		Temperature: false,
	})
	if err != nil {
		t.Fatalf("prepare openai: %v", err)
	}
	anthropicReady, err := PrepareRequestForCapabilities(req, Capabilities{
		SystemRole:  RoleSystem,
		Temperature: true,
		Reasoning:   ReasoningCapabilities{Kind: ReasoningKindBudget},
	})
	if err != nil {
		t.Fatalf("prepare anthropic: %v", err)
	}

	if req.Messages[0].Role != RoleSystem ||
		req.Messages[1].Reasoning == "" ||
		len(req.Messages[1].ThinkingBlocks) != 1 {
		t.Fatalf("original request was mutated: %#v", req.Messages)
	}
	if openAIReady.Messages[0].Role != RoleDeveloper ||
		openAIReady.Temperature != 0 ||
		len(openAIReady.Messages[1].ThinkingBlocks) != 0 {
		t.Fatalf("unexpected openai prepared request: %#v", openAIReady)
	}
	if anthropicReady.Messages[0].Role != RoleSystem ||
		anthropicReady.Temperature != 0.7 ||
		len(anthropicReady.Messages[1].ThinkingBlocks) != 1 {
		t.Fatalf("unexpected anthropic prepared request: %#v", anthropicReady)
	}
}

func TestPrepareRequestForCapabilitiesRejectsLatePrivilegedBeforeRewrite(t *testing.T) {
	req := &Request{
		Messages: []Message{
			{Role: RoleUser, Content: "hello"},
			{Role: RoleSystem, Content: "late system"},
		},
	}

	if _, err := PrepareRequestForCapabilities(req, Capabilities{
		SystemRole:  RoleUser,
		Temperature: true,
	}); err == nil {
		t.Fatal("expected late privileged message to be rejected before rewrite")
	}
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && stringsContains(s, substr))
}

func stringsContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
