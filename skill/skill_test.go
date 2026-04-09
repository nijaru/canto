package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentskills "github.com/nijaru/agentskills"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func TestReadSkillTool(t *testing.T) {
	reg := agentskills.NewRegistry()
	reg.Register(&agentskills.Skill{
		Name:         "hello",
		Description:  "hello skill",
		Instructions: "Use this skill for greetings.",
	})

	tool := &ReadSkillTool{Registry: reg}
	out, err := tool.Execute(t.Context(), `{"name":"hello"}`)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(out, "# Skill: hello") {
		t.Fatalf("read output missing heading: %q", out)
	}
	if !strings.Contains(out, "Use this skill for greetings.") {
		t.Fatalf("read output missing instructions: %q", out)
	}
}

func TestManageSkillTool(t *testing.T) {
	tmp := t.TempDir()
	reg := agentskills.NewRegistry()
	tool := &ManageSkillTool{Registry: reg, Path: tmp}

	content := "---\nname: hello\ndescription: hello skill\n---\nDo things.\n"

	// create
	out, err := tool.Execute(
		t.Context(),
		`{"action":"create","name":"hello","content":"`+escapeJSON(content)+`"}`,
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := reg.Get("hello"); !ok {
		t.Fatal("skill not registered after create")
	}
	if _, err := os.Stat(filepath.Join(tmp, "hello", "SKILL.md")); err != nil {
		t.Fatalf("skill file missing after create: %v", err)
	}
	t.Log(out)

	// update
	content2 := "---\nname: hello\ndescription: updated\n---\nUpdated.\n"
	if _, err := tool.Execute(
		t.Context(),
		`{"action":"update","name":"hello","content":"`+escapeJSON(content2)+`"}`,
	); err != nil {
		t.Fatalf("update: %v", err)
	}
	s, _ := reg.Get("hello")
	if s.Description != "updated" {
		t.Errorf("description not updated: %s", s.Description)
	}

	// delete
	if _, err := tool.Execute(t.Context(), `{"action":"delete","name":"hello"}`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := reg.Get("hello"); ok {
		t.Fatal("skill still present after delete")
	}
	if _, err := os.Stat(filepath.Join(tmp, "hello", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("skill file still present after delete: %v", err)
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"hello", true},
		{"my-skill", true},
		{"a1b2", true},
		{"a", true},
		{"", false},
		{"A-skill", false},
		{"-start", false},
		{"end-", false},
		{"no--double", false},
		{"has space", false},
	}
	for _, c := range cases {
		err := agentskills.ValidateName(c.name)
		if c.ok && err != nil {
			t.Errorf("name=%q: unexpected error: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("name=%q: expected error", c.name)
		}
	}
}

func TestListPrompt(t *testing.T) {
	reg := agentskills.NewRegistry()
	reg.Register(&agentskills.Skill{Name: "zeta", Description: "last"})
	reg.Register(&agentskills.Skill{Name: "alpha", Description: "first"})

	req := &llm.Request{}
	if err := ListPrompt(reg).ApplyRequest(t.Context(), nil, "", &session.Session{}, req); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(req.Messages) == 0 {
		t.Fatal("expected injected system message")
	}
	if req.Messages[0].Role != llm.RoleSystem {
		t.Fatalf("first message role = %s, want system", req.Messages[0].Role)
	}
	if !strings.Contains(req.Messages[0].Content, "Available Skills") {
		t.Fatalf("missing skills header: %q", req.Messages[0].Content)
	}
	if !strings.Contains(req.Messages[0].Content, "- alpha: first") ||
		!strings.Contains(req.Messages[0].Content, "- zeta: last") {
		t.Fatalf("missing skill summaries: %q", req.Messages[0].Content)
	}
}

func TestPreloadPrompt(t *testing.T) {
	req := &llm.Request{}
	err := PreloadPrompt(
		&agentskills.Skill{
			Name:         "debug",
			Description:  "Debugging workflow",
			Instructions: "Follow the debugger checklist.",
		},
	).ApplyRequest(t.Context(), nil, "", &session.Session{}, req)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(req.Messages) == 0 {
		t.Fatal("expected injected system message")
	}
	if !strings.Contains(req.Messages[0].Content, "Preloaded Skills:") {
		t.Fatalf("missing preloaded skill header: %q", req.Messages[0].Content)
	}
	if !strings.Contains(req.Messages[0].Content, "# Skill: debug") {
		t.Fatalf("missing skill heading: %q", req.Messages[0].Content)
	}
	if !strings.Contains(req.Messages[0].Content, "Follow the debugger checklist.") {
		t.Fatalf("missing skill instructions: %q", req.Messages[0].Content)
	}
}

// escapeJSON escapes a string for embedding in a JSON string literal.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
