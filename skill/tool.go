package skill

import (
	"context"
	"github.com/go-json-experiment/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nijaru/canto/llm"
)

var skillNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ReadSkillTool allows an agent to read the full content of a skill.
type ReadSkillTool struct {
	Registry *Registry
}

func (t *ReadSkillTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "read_skill",
		Description: "Read the full instructions and methodology of a specific skill.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The name of the skill to read.",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (t *ReadSkillTool) Execute(ctx context.Context, args string) (string, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", err
	}

	s, ok := t.Registry.Get(input.Name)
	if !ok {
		return "", fmt.Errorf("skill %s not found", input.Name)
	}

	return fmt.Sprintf("# Skill: %s\n\n%s", s.Name, s.Instructions), nil
}

// ManageSkillTool allows an agent to create, update, or delete SKILL.md files
// at runtime, enabling closed-loop skill refinement.
type ManageSkillTool struct {
	Registry *Registry
	// Path is the root directory where skill subdirectories are written.
	// Each skill is stored at {Path}/{name}/SKILL.md.
	Path string
}

func (t *ManageSkillTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "manage_skill",
		Description: "Create, update, or delete a skill definition (SKILL.md). Use action=create or action=update to write skill content, action=delete to remove it.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"create", "update", "delete"},
					"description": "Action to perform: create, update, or delete.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Skill name: lowercase alphanumeric and hyphens, max 64 chars (e.g. my-skill).",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Full SKILL.md content including YAML frontmatter. Required for create and update.",
				},
			},
			"required": []string{"action", "name"},
		},
	}
}

func (t *ManageSkillTool) Execute(_ context.Context, args string) (string, error) {
	var input struct {
		Action  string `json:"action"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	if err := validateSkillName(input.Name); err != nil {
		return "", err
	}

	switch input.Action {
	case "create", "update":
		if input.Content == "" {
			return "", fmt.Errorf("content is required for action %q", input.Action)
		}
		return t.write(input.Name, input.Content)
	case "delete":
		return t.delete(input.Name)
	default:
		return "", fmt.Errorf("unknown action %q: must be create, update, or delete", input.Action)
	}
}

func (t *ManageSkillTool) write(name, content string) (string, error) {
	dir := filepath.Join(t.Path, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("manage_skill: mkdir: %w", err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("manage_skill: write: %w", err)
	}
	s, err := Load(path)
	if err != nil {
		return "", fmt.Errorf("manage_skill: parse: %w", err)
	}
	t.Registry.Register(s)
	return fmt.Sprintf("skill %q written to %s", name, path), nil
}

func (t *ManageSkillTool) delete(name string) (string, error) {
	path := filepath.Join(t.Path, name, "SKILL.md")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("manage_skill: delete: %w", err)
	}
	t.Registry.Deregister(name)
	return fmt.Sprintf("skill %q deleted", name), nil
}

func validateSkillName(name string) error {
	if len(name) == 0 || len(name) > 64 {
		return fmt.Errorf("skill name must be 1–64 characters, got %d", len(name))
	}
	if !skillNameRe.MatchString(name) {
		return fmt.Errorf("skill name %q must match ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$", name)
	}
	if strings.Contains(name, "--") {
		return fmt.Errorf("skill name %q must not contain consecutive hyphens", name)
	}
	return nil
}
