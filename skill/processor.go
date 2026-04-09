package skill

import (
	"context"
	"fmt"
	"strings"

	agentskills "github.com/nijaru/agentskills"
	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// ListPrompt injects a summary list of all available skills.
func ListPrompt(reg *agentskills.Registry) ccontext.RequestProcessor {
	if reg == nil {
		return ccontext.RequestProcessorFunc(
			func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
				return nil
			},
		)
	}
	return ccontext.RequestProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
			skills := reg.List()
			if len(skills) == 0 {
				return nil
			}

			var sb strings.Builder
			sb.WriteString("Available Skills (use read_skill for full details):\n")
			for _, s := range skills {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
			}

			instructions := sb.String()
			return ccontext.Instructions(instructions).ApplyRequest(ctx, p, model, sess, req)
		},
	)
}

// PreloadPrompt injects the full instructions for a selected skill set.
func PreloadPrompt(skills ...*agentskills.Skill) ccontext.RequestProcessor {
	preloaded := make([]*agentskills.Skill, 0, len(skills))
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		preloaded = append(preloaded, skill)
	}
	return ccontext.RequestProcessorFunc(
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
			return ccontext.Instructions(sb.String()).ApplyRequest(ctx, p, model, sess, req)
		},
	)
}
