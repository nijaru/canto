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

// ListProcessor injects a summary list of all available skills.
func ListProcessor(reg *agentskills.Registry) ccontext.Processor {
	if reg == nil {
		return ccontext.ProcessorFunc(
			func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
				return nil
			},
		)
	}
	return ccontext.ProcessorFunc(
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
			return ccontext.Instructions(instructions).Process(ctx, p, model, sess, req)
		},
	)
}
