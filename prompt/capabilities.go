package prompt

import (
	"context"

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
//   - Thinking=false: preserves unsupported thinking as text instead of
//     dropping it, so conversations can be replayed across providers.
//   - Tool IDs: normalizes tool-call IDs deterministically so tool results
//     stay aligned when conversations move between providers.
func Capabilities() RequestProcessor {
	return RequestProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, _ *session.Session, req *llm.Request) error {
			if p == nil {
				return nil
			}
			llm.TransformRequestForCapabilities(req, p.Capabilities(model))
			return nil
		},
	)
}
