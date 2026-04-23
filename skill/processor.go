package skill

import (
	"context"
	"fmt"
	"strings"

	agentskills "github.com/nijaru/agentskills"
	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
	"github.com/nijaru/canto/session"
)

// ListPrompt injects a summary list of all available skills.
func ListPrompt(reg *agentskills.Registry) prompt.RequestProcessor {
	return ListPromptWithOptions(reg, ListPromptOptions{})
}

// PreloadPrompt injects the full instructions for a selected skill set.
func PreloadPrompt(skills ...*agentskills.Skill) prompt.RequestProcessor {
	preloaded := make([]*agentskills.Skill, 0, len(skills))
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		preloaded = append(preloaded, skill)
	}
	return prompt.RequestProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
			if len(preloaded) == 0 {
				return nil
			}
			var sb strings.Builder
			sb.WriteString("Preloaded Skills:\n")
			for _, skill := range preloaded {
				sb.WriteString(fmt.Sprintf("\n# Skill: %s\n", skill.Name))
				if skill.Description != "" {
					sb.WriteString(skill.Description)
					sb.WriteByte('\n')
				}
				sb.WriteString(skill.Instructions)
				if !strings.HasSuffix(skill.Instructions, "\n") {
					sb.WriteByte('\n')
				}
			}
			return prompt.Instructions(sb.String()).ApplyRequest(ctx, p, model, sess, req)
		},
	)
}
