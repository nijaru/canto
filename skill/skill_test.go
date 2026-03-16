package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoader(t *testing.T) {
	content := `---
name: test-skill
description: A test skill for testing.
allowed-tools: [bash, read_file]
---
# Instructions
Use this skill for testing purposes.
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "SKILL.md")
	os.WriteFile(path, []byte(content), 0o644)

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if s.Name != "test-skill" {
		t.Errorf("expected name test-skill, got %s", s.Name)
	}
	if s.Description != "A test skill for testing." {
		t.Errorf("expected description, got %s", s.Description)
	}
	if len(s.AllowedTools) != 2 || s.AllowedTools[0] != "bash" {
		t.Errorf("expected allowed-tools [bash, read_file], got %v", s.AllowedTools)
	}
	if s.Instructions != "# Instructions\nUse this skill for testing purposes." {
		t.Errorf("expected instructions, got %s", s.Instructions)
	}
}

func TestRegisterDeregister(t *testing.T) {
	reg := NewRegistry()
	s := &Skill{Name: "my-skill", Description: "desc"}
	reg.Register(s)
	got, ok := reg.Get("my-skill")
	if !ok || got != s {
		t.Fatal("skill not found after Register")
	}
	reg.Deregister("my-skill")
	if _, ok := reg.Get("my-skill"); ok {
		t.Fatal("skill still present after Deregister")
	}
	reg.Deregister("my-skill") // no-op
}

func TestManageSkillTool(t *testing.T) {
	tmp := t.TempDir()
	reg := NewRegistry()
	tool := &ManageSkillTool{Registry: reg, Path: tmp}

	content := "---\nname: hello\ndescription: hello skill\n---\nDo things.\n"

	// create
	out, err := tool.Execute(
		nil,
		`{"action":"create","name":"hello","content":"`+escapeJSON(content)+`"}`,
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := reg.Get("hello"); !ok {
		t.Fatal("skill not registered after create")
	}
	t.Log(out)

	// update
	content2 := "---\nname: hello\ndescription: updated\n---\nUpdated.\n"
	if _, err := tool.Execute(nil, `{"action":"update","name":"hello","content":"`+escapeJSON(content2)+`"}`); err != nil {
		t.Fatalf("update: %v", err)
	}
	s, _ := reg.Get("hello")
	if s.Description != "updated" {
		t.Errorf("description not updated: %s", s.Description)
	}

	// delete
	if _, err := tool.Execute(nil, `{"action":"delete","name":"hello"}`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := reg.Get("hello"); ok {
		t.Fatal("skill still present after delete")
	}
}

func TestValidateSkillName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"hello", true},
		{"my-skill", true},
		{"a1b2", true},
		{"a", true},
		{"", false},
		{"A-skill", false},    // uppercase
		{"-start", false},     // starts with hyphen
		{"end-", false},       // ends with hyphen
		{"no--double", false}, // double hyphen
		{"has space", false},  // space
	}
	for _, c := range cases {
		err := validateSkillName(c.name)
		if c.ok && err != nil {
			t.Errorf("name=%q: unexpected error: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("name=%q: expected error", c.name)
		}
	}
}

// escapeJSON escapes a string for embedding in a JSON string literal.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func TestRegistry(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "skills", "test")
	os.MkdirAll(skillDir, 0o755)

	content := `---
name: registry-test
description: Testing registry discovery.
---
Instructions here.
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644)

	reg := NewRegistry(tmp)
	err := reg.Discover()
	if err != nil {
		t.Fatal(err)
	}

	skills := reg.List()
	if len(skills) != 1 {
		t.Errorf("expected 1 skill, got %d", len(skills))
	}

	s, ok := reg.Get("registry-test")
	if !ok {
		t.Fatal("skill not found in registry")
	}
	if s.Name != "registry-test" {
		t.Errorf("expected name registry-test, got %s", s.Name)
	}
}
