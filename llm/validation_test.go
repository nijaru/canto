package llm

import "testing"

func TestValidateRequestAllowsLeadingPrivilegedMessages(t *testing.T) {
	req := &Request{
		Messages: []Message{
			{Role: RoleSystem, Content: "system"},
			{Role: RoleDeveloper, Content: "developer"},
			{Role: RoleUser, Content: "hello"},
		},
	}

	if err := ValidateRequest(req); err != nil {
		t.Fatalf("ValidateRequest: %v", err)
	}
}

func TestValidateRequestRejectsMidConversationPrivilegedMessages(t *testing.T) {
	req := &Request{
		Messages: []Message{
			{Role: RoleUser, Content: "hello"},
			{Role: RoleDeveloper, Content: "late developer"},
		},
	}

	if err := ValidateRequest(req); err == nil {
		t.Fatal("expected mid-conversation privileged message error")
	}
}

func TestValidateRequestRejectsInvalidRole(t *testing.T) {
	req := &Request{
		Messages: []Message{{Role: Role(""), Content: "hello"}},
	}

	if err := ValidateRequest(req); err == nil {
		t.Fatal("expected invalid role error")
	}
}

func TestValidateRequestRejectsEmptyAssistant(t *testing.T) {
	req := &Request{
		Messages: []Message{{Role: RoleAssistant}},
	}

	if err := ValidateRequest(req); err == nil {
		t.Fatal("expected empty assistant error")
	}
}

func TestValidateRequestAllowsToolCallsMissingResultsBeforeTransform(t *testing.T) {
	req := &Request{
		Messages: []Message{{
			Role: RoleAssistant,
			Calls: []Call{{
				ID:   "call-1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "search", Arguments: `{}`},
			}},
		}},
	}

	if err := ValidateRequest(req); err != nil {
		t.Fatalf("ValidateRequest: %v", err)
	}
}

func TestValidateRequestRejectsUnmatchedToolResult(t *testing.T) {
	req := &Request{
		Messages: []Message{{Role: RoleTool, ToolID: "call-1", Content: "orphan"}},
	}

	if err := ValidateRequest(req); err == nil {
		t.Fatal("expected unmatched tool result error")
	}
}
