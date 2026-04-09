package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-json-experiment/json"

	agentskills "github.com/nijaru/agentskills"
	"github.com/nijaru/canto/llm"
)

// ReadSkillTool allows an agent to read the full content of a skill.
type ReadSkillTool struct {
	Registry *agentskills.Registry
}

func (t *ReadSkillTool) Spec() llm.Spec {
	return llm.Spec{
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
	if t.Registry == nil {
		return "", fmt.Errorf("skill registry is nil")
	}
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
	Registry *agentskills.Registry
	// Path is the root directory where skill subdirectories are written.
	// Each skill is stored at {Path}/{name}/SKILL.md.
	Path string
}

func (t *ManageSkillTool) Spec() llm.Spec {
	return llm.Spec{
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
	if t.Registry == nil {
		return "", fmt.Errorf("skill registry is nil")
	}
	if t.Path == "" {
		return "", fmt.Errorf("skill path is empty")
	}
	var input struct {
		Action  string `json:"action"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	if err := agentskills.ValidateName(input.Name); err != nil {
		return "", err
	}

	switch input.Action {
	case "create", "update":
		if input.Content == "" {
			return "", fmt.Errorf("content is required for action %q", input.Action)
		}
		return t.write(input.Action, input.Name, input.Content)
	case "delete":
		return t.delete(input.Name)
	default:
		return "", fmt.Errorf("unknown action %q: must be create, update, or delete", input.Action)
	}
}

func (t *ManageSkillTool) write(action, name, content string) (string, error) {
	skill, err := validateSkillContent(name, content)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(t.Path, 0o755); err != nil {
		return "", fmt.Errorf("manage_skill: mkdir root: %w", err)
	}
	root, err := os.OpenRoot(t.Path)
	if err != nil {
		return "", fmt.Errorf("manage_skill: open root: %w", err)
	}
	defer root.Close()

	exists, err := root.Stat(name)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("manage_skill: stat: %w", err)
	}
	switch action {
	case "create":
		if err == nil && exists.IsDir() {
			return "", fmt.Errorf("skill %q already exists", name)
		}
	case "update":
		if os.IsNotExist(err) {
			return "", fmt.Errorf("skill %q does not exist", name)
		}
	}

	if err := root.MkdirAll(name, 0o755); err != nil {
		return "", fmt.Errorf("manage_skill: mkdir: %w", err)
	}
	rel := filepath.Join(name, "SKILL.md")
	if err := root.WriteFile(rel, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("manage_skill: write: %w", err)
	}
	path := filepath.Join(t.Path, rel)
	t.Registry.Register(skill)
	return fmt.Sprintf("skill %q written to %s", name, path), nil
}

func (t *ManageSkillTool) delete(name string) (string, error) {
	if _, err := os.Stat(t.Path); os.IsNotExist(err) {
		t.Registry.Deregister(name)
		return fmt.Sprintf("skill %q deleted", name), nil
	}
	root, err := os.OpenRoot(t.Path)
	if err != nil {
		return "", fmt.Errorf("manage_skill: open root: %w", err)
	}
	defer root.Close()

	if err := os.RemoveAll(filepath.Join(t.Path, name)); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("manage_skill: delete: %w", err)
	}
	t.Registry.Deregister(name)
	return fmt.Sprintf("skill %q deleted", name), nil
}

func validateSkillContent(name, content string) (*agentskills.Skill, error) {
	tmp, err := os.CreateTemp("", "canto-skill-*.md")
	if err != nil {
		return nil, fmt.Errorf("manage_skill: temp file: %w", err)
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("manage_skill: temp write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("manage_skill: temp close: %w", err)
	}
	skill, err := agentskills.Load(path)
	if err != nil {
		return nil, fmt.Errorf("manage_skill: parse: %w", err)
	}
	if skill.Name != name {
		return nil, fmt.Errorf(
			"manage_skill: content name %q does not match requested name %q",
			skill.Name,
			name,
		)
	}
	if err := skill.Validate(); err != nil {
		return nil, fmt.Errorf("manage_skill: validate: %w", err)
	}
	return skill, nil
}
