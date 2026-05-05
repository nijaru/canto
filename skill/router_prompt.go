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

type ListPromptOptions struct {
	Router Router
	Query  string
	Limit  int
}

func ListPromptWithOptions(
	reg *agentskills.Registry,
	opts ListPromptOptions,
) prompt.RequestProcessor {
	if reg == nil {
		return prompt.RequestProcessorFunc(
			func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
				return nil
			},
		)
	}
	return prompt.RequestProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
			metas, err := selectSkillMetadata(ctx, reg, sess, opts)
			if err != nil {
				return err
			}
			if len(metas) == 0 {
				return nil
			}
			var sb strings.Builder
			sb.WriteString("Available Skills (use read_skill for full details):\n")
			for _, skill := range metas {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", skill.Name, skill.Description))
			}
			return prompt.Instructions(sb.String()).ApplyRequest(ctx, p, model, sess, req)
		},
	)
}

func selectSkillMetadata(
	ctx context.Context,
	reg *agentskills.Registry,
	sess *session.Session,
	opts ListPromptOptions,
) ([]SkillMetadata, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" && sess != nil {
		messages, err := sess.EffectiveMessages()
		if err != nil {
			return nil, err
		}
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Content != "" {
				query = messages[i].Content
				break
			}
		}
	}
	if opts.Router != nil {
		return opts.Router.SelectRelevant(ctx, query, opts.Limit)
	}
	return listMetadata(reg.List(), opts.Limit), nil
}
