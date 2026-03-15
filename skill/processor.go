package skill

import (
	"context"
	"fmt"
	"strings"

	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// ListProcessor injects a summary list of all available skills.
func ListProcessor(reg *Registry) ccontext.ContextProcessor {
	return ccontext.ProcessorFunc(
		func(ctx context.Context, sess *session.Session, req *llm.LLMRequest) error {
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
			return ccontext.InstructionProcessor(instructions).Process(ctx, sess, req)
		},
	)
}
