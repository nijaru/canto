package context

import (
	"context"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func TestFingerprintPromptCacheIgnoresHistorySuffix(t *testing.T) {
	sess := session.New("cache")
	if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "hello",
	})); err != nil {
		t.Fatalf("append history: %v", err)
	}

	req1 := &llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "system"},
			{Role: llm.RoleUser, Content: "hello"},
		},
		Tools: []*llm.Spec{{Name: "alpha", Description: "A"}},
	}
	req2 := &llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "system"},
			{Role: llm.RoleUser, Content: "different history"},
		},
		Tools: []*llm.Spec{{Name: "alpha", Description: "A"}},
	}

	fp1, err := FingerprintPromptCache(sess, req1)
	if err != nil {
		t.Fatalf("fingerprint req1: %v", err)
	}
	fp2, err := FingerprintPromptCache(sess, req2)
	if err != nil {
		t.Fatalf("fingerprint req2: %v", err)
	}
	if fp1 != fp2 {
		t.Fatalf("fingerprint changed across history-only mutation: %v vs %v", fp1, fp2)
	}
}

func TestFingerprintPromptCacheChangesOnPrefixOrToolSchema(t *testing.T) {
	sess := session.New("cache-change")
	if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "hello",
	})); err != nil {
		t.Fatalf("append history: %v", err)
	}

	base := &llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "system"},
			{Role: llm.RoleUser, Content: "hello"},
		},
		Tools: []*llm.Spec{{Name: "alpha", Description: "A"}},
	}
	changedPrefix := &llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "updated system"},
			{Role: llm.RoleUser, Content: "hello"},
		},
		Tools: []*llm.Spec{{Name: "alpha", Description: "A"}},
	}
	changedTools := &llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "system"},
			{Role: llm.RoleUser, Content: "hello"},
		},
		Tools: []*llm.Spec{{Name: "alpha", Description: "B"}},
	}

	baseFP, err := FingerprintPromptCache(sess, base)
	if err != nil {
		t.Fatalf("fingerprint base: %v", err)
	}
	prefixFP, err := FingerprintPromptCache(sess, changedPrefix)
	if err != nil {
		t.Fatalf("fingerprint changed prefix: %v", err)
	}
	toolsFP, err := FingerprintPromptCache(sess, changedTools)
	if err != nil {
		t.Fatalf("fingerprint changed tools: %v", err)
	}
	if baseFP == prefixFP {
		t.Fatal("expected prefix hash to change when system prompt changes")
	}
	if baseFP == toolsFP {
		t.Fatal("expected tool schema hash to change when tool schema changes")
	}
}

func TestCacheAligner(t *testing.T) {
	req := &llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "1"},
			{Role: llm.RoleUser, Content: "2"},
			{Role: llm.RoleAssistant, Content: "3"},
			{Role: llm.RoleUser, Content: "4"},
		},
		Tools: []*llm.Spec{
			{Name: "tool1"},
			{Name: "tool2"},
			{Name: "tool3"},
		},
	}

	aligner := CacheAligner(2)
	err := aligner.ApplyRequest(context.Background(), nil, "", nil, req)
	if err != nil {
		t.Fatalf("CacheAligner error: %v", err)
	}

	for i, m := range req.Messages {
		if i == 0 || i == 2 || i == 3 {
			if m.CacheControl == nil || m.CacheControl.Type != "ephemeral" {
				t.Errorf("expected message %d to have ephemeral cache control", i)
			}
		} else {
			if m.CacheControl != nil {
				t.Errorf("expected message %d to NOT have cache control, got %v", i, m.CacheControl)
			}
		}
	}

	for i, tool := range req.Tools {
		if i == 2 {
			if tool.CacheControl == nil || tool.CacheControl.Type != "ephemeral" {
				t.Errorf("expected last tool to have ephemeral cache control")
			}
		} else {
			if tool.CacheControl != nil {
				t.Errorf("expected tool %d to NOT have cache control", i)
			}
		}
	}
}
