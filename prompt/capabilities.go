package prompt

import (
	"context"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Capabilities adapts Request fields to match the model's capability
// constraints inside the prompt pipeline. Built-in providers already prepare
// provider-specific request copies at send time, so most callers should not add
// this processor. It is for custom providers that expect callers to perform
// capability adaptation before Provider.Generate or Provider.Stream.
//
// Deprecated: implement capability preparation in the provider with
// llm.PrepareRequestForCapabilities so resolver/failover paths do not mutate a
// neutral request before the final provider is selected.
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
	return capabilitiesProcessor{}
}

type capabilitiesProcessor struct{}

func (c capabilitiesProcessor) ApplyRequest(
	ctx context.Context,
	p llm.Provider,
	model string,
	_ *session.Session,
	req *llm.Request,
) error {
	if err := llm.ValidateRequest(req); err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	llm.TransformRequestForCapabilities(req, p.Capabilities(model))
	if err := llm.ValidateRequest(req); err != nil {
		return err
	}
	return nil
}
