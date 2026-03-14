package skill

import (
	"os"
	"path/filepath"
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
	os.WriteFile(path, []byte(content), 0644)

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

func TestRegistry(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "skills", "test")
	os.MkdirAll(skillDir, 0755)

	content := `---
name: registry-test
description: Testing registry discovery.
---
Instructions here.
`
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644)

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
