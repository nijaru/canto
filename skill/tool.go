package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nijaru/canto/llm"
)

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
