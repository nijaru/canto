package skill

import (
	"strings"
	"testing"

	agentskills "github.com/nijaru/agentskills"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func TestLexicalRouter_UsesBodySignal(t *testing.T) {
	reg := agentskills.NewRegistry()
	reg.Register(&agentskills.Skill{
		Name:         "build-docs",
		Description:  "Documentation helper",
		Instructions: "Use this skill when generating API references and markdown docs.",
	})
	reg.Register(&agentskills.Skill{
		Name:         "build-tests",
		Description:  "Documentation helper",
		Instructions: "Use this skill when repairing flaky tests and debugging CI failures.",
	})

	router := &LexicalRouter{Registry: reg}
	results, err := router.SelectRelevant(t.Context(), "fix the flaky CI tests", 1)
	if err != nil {
		t.Fatalf("SelectRelevant: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SelectRelevant returned %d results, want 1", len(results))
	}
	if results[0].Name != "build-tests" {
		t.Fatalf("selected %q, want build-tests", results[0].Name)
	}
}

func TestListPromptWithOptions_SubsetsSkills(t *testing.T) {
	reg := agentskills.NewRegistry()
	reg.Register(&agentskills.Skill{
		Name:         "debug-go",
		Description:  "Debug Go builds",
		Instructions: "Inspect Go compiler errors and test failures.",
	})
	reg.Register(&agentskills.Skill{
		Name:         "design-ui",
		Description:  "Design visual interfaces",
		Instructions: "Use this for layout, color, and typography decisions.",
	})

	req := &llm.Request{}
	sess := session.New("skill-router")
	if err := sess.Append(t.Context(), session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "the Go test suite is failing in CI",
	})); err != nil {
		t.Fatalf("AppendText: %v", err)
	}
	err := ListPromptWithOptions(reg, ListPromptOptions{
		Router: &LexicalRouter{Registry: reg},
		Limit:  1,
	}).ApplyRequest(t.Context(), nil, "", sess, req)
	if err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}
	if len(req.Messages) == 0 {
		t.Fatal("expected injected system message")
	}
	content := req.Messages[0].Content
	if !strings.Contains(content, "- debug-go: Debug Go builds") {
		t.Fatalf("expected routed skill in prompt: %q", content)
	}
	if strings.Contains(content, "design-ui") {
		t.Fatalf("unexpected unrelated skill in prompt: %q", content)
	}
}
