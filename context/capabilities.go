package context

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Capabilities adapts Request fields to match the model's
// capability constraints. It must run last in the request-shaping chain, after
// all other request processors have populated the request.
//
// Handles:
//   - SystemRole != RoleSystem: rewrites system messages to the declared role.
//     RoleUser: injects an "Instructions:" prefix (models with no system role).
//     Any other role (e.g. RoleDeveloper): converts role directly, no prefix.
//   - Temperature=false: zeroes the temperature field.
func Capabilities() RequestProcessor {
	return RequestProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, _ *session.Session, req *llm.Request) error {
			if p == nil {
				return nil
			}
			caps := p.Capabilities(model)

			if caps.SystemRole != llm.RoleSystem {
				rewriteSystemMessages(req, caps.SystemRole)
			}
			if !caps.Temperature {
				req.Temperature = 0
			}
			// Strip reasoning from history for models that don't support extended
			// thinking. Reasoning is preserved in the session log for observability;
			// it is only filtered here at request-build time.
			if !caps.Thinking {
				for i := range req.Messages {
					req.Messages[i].Reasoning = ""
					req.Messages[i].ThinkingBlocks = nil
				}
			}
			return nil
		},
	)
}

// rewriteSystemMessages converts system-role messages to targetRole.
// When targetRole is RoleUser, content is prefixed with "Instructions:\n" so
// the model can distinguish instructions from conversational user turns.
// For other target roles the content is passed through unchanged.
func rewriteSystemMessages(req *llm.Request, targetRole llm.Role) {
	for i, m := range req.Messages {
		if m.Role != llm.RoleSystem {
			continue
		}
		content := m.Content
		if targetRole == llm.RoleUser {
			content = fmt.Sprintf("Instructions:\n%s", content)
		}
		req.Messages[i] = llm.Message{Role: targetRole, Content: content}
	}
}
