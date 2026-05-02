package llm

import "testing"

func TestRequestCloneKeepsOriginalNeutral(t *testing.T) {
	req := &Request{
		Temperature:         0.7,
		CachePrefixMessages: 1,
		Messages: []Message{{
			Role:           RoleSystem,
			Content:        "system",
			ThinkingBlocks: []ThinkingBlock{{Type: "thinking", Thinking: "secret"}},
			Calls: []Call{{
				ID:   "call 1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "read", Arguments: "{}"},
			}},
			CacheControl: &CacheControl{Type: "ephemeral"},
		}},
		Tools: []*Spec{{
			Name: "read",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type": "string",
						"enum": []string{"README.md", "AGENTS.md"},
					},
					"mode": []any{
						map[string]any{"const": "full"},
						"summary",
					},
				},
			},
			CacheControl: &CacheControl{Type: "ephemeral"},
		}},
		ResponseFormat: &ResponseFormat{
			Type: ResponseFormatJSONSchema,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ok": map[string]any{"type": "boolean"},
				},
			},
		},
	}

	clone := req.Clone()
	clone.Messages[0].Role = RoleUser
	clone.Messages[0].ThinkingBlocks[0].Thinking = "changed"
	clone.Messages[0].Calls[0].ID = "changed"
	clone.Messages[0].CacheControl.Type = "changed"
	clone.Tools[0].CacheControl.Type = "changed"
	toolParams := clone.Tools[0].Parameters.(map[string]any)
	toolProps := toolParams["properties"].(map[string]any)
	pathSchema := toolProps["path"].(map[string]any)
	pathSchema["type"] = "integer"
	enum := pathSchema["enum"].([]string)
	enum[0] = "changed.md"
	modeOptions := toolProps["mode"].([]any)
	modeConst := modeOptions[0].(map[string]any)
	modeConst["const"] = "changed"
	formatProps := clone.ResponseFormat.Schema["properties"].(map[string]any)
	formatOK := formatProps["ok"].(map[string]any)
	formatOK["type"] = "string"
	clone.ResponseFormat.Schema["type"] = "array"

	originalToolParams := req.Tools[0].Parameters.(map[string]any)
	originalToolProps := originalToolParams["properties"].(map[string]any)
	originalPathSchema := originalToolProps["path"].(map[string]any)
	originalEnum := originalPathSchema["enum"].([]string)
	originalModeConst := originalToolProps["mode"].([]any)[0].(map[string]any)
	originalFormatOK := req.ResponseFormat.Schema["properties"].(map[string]any)["ok"].(map[string]any)
	if req.Messages[0].Role != RoleSystem ||
		req.Messages[0].ThinkingBlocks[0].Thinking != "secret" ||
		req.Messages[0].Calls[0].ID != "call 1" ||
		req.Messages[0].CacheControl.Type != "ephemeral" ||
		req.Tools[0].CacheControl.Type != "ephemeral" ||
		originalPathSchema["type"] != "string" ||
		originalEnum[0] != "README.md" ||
		originalModeConst["const"] != "full" ||
		originalFormatOK["type"] != "boolean" ||
		req.ResponseFormat.Schema["type"] != "object" {
		t.Fatalf("clone mutation leaked into original: %#v", req)
	}
}

func TestRequestPrependMessageExtendsCachePrefix(t *testing.T) {
	req := &Request{
		CachePrefixMessages: 1,
		Messages: []Message{
			{Role: RoleUser, Content: "stable context"},
			{Role: RoleUser, Content: "history"},
		},
	}

	req.PrependMessage(Message{Role: RoleSystem, Content: "system"})

	if req.CachePrefixMessages != 2 {
		t.Fatalf("cache prefix messages = %d, want 2", req.CachePrefixMessages)
	}
	if req.Messages[0].Role != RoleSystem ||
		req.Messages[1].Content != "stable context" ||
		req.Messages[2].Content != "history" {
		t.Fatalf("unexpected messages: %#v", req.Messages)
	}
}

func TestRequestInsertAfterCachePrefixPreservesBoundary(t *testing.T) {
	req := &Request{
		CachePrefixMessages: 2,
		Messages: []Message{
			{Role: RoleSystem, Content: "system"},
			{Role: RoleUser, Content: "stable context"},
			{Role: RoleUser, Content: "history"},
		},
	}

	req.InsertAfterCachePrefix(Message{Role: RoleUser, Content: "dynamic context"})

	if req.CachePrefixMessages != 2 {
		t.Fatalf("cache prefix messages = %d, want 2", req.CachePrefixMessages)
	}
	if req.Messages[1].Content != "stable context" ||
		req.Messages[2].Content != "dynamic context" ||
		req.Messages[3].Content != "history" {
		t.Fatalf("unexpected messages: %#v", req.Messages)
	}
}

func TestRequestInsertMessageBeforePrefixExtendsBoundary(t *testing.T) {
	req := &Request{
		CachePrefixMessages: 2,
		Messages: []Message{
			{Role: RoleSystem, Content: "system"},
			{Role: RoleUser, Content: "stable context"},
			{Role: RoleUser, Content: "history"},
		},
	}

	req.InsertMessage(1, Message{Role: RoleUser, Content: "more stable context"})

	if req.CachePrefixMessages != 3 {
		t.Fatalf("cache prefix messages = %d, want 3", req.CachePrefixMessages)
	}
}

func TestRequestInsertPrefixMessageAtBoundaryExtendsBoundary(t *testing.T) {
	req := &Request{
		CachePrefixMessages: 2,
		Messages: []Message{
			{Role: RoleSystem, Content: "system"},
			{Role: RoleAssistant, Content: "stable assistant"},
			{Role: RoleUser, Content: "history"},
		},
	}

	req.InsertPrefixMessage(2, Message{Role: RoleTool, Content: "stable tool result"})

	if req.CachePrefixMessages != 3 {
		t.Fatalf("cache prefix messages = %d, want 3", req.CachePrefixMessages)
	}
	if req.Messages[2].Role != RoleTool || req.Messages[3].Content != "history" {
		t.Fatalf("unexpected messages: %#v", req.Messages)
	}
}
