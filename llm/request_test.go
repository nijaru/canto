package llm

import "testing"

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
