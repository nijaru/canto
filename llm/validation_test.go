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
