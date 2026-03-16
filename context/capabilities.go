package context

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// CapabilitiesProcessor adapts LLMRequest fields to match the model's
// capability constraints. It must run last in the processor chain, after
// all other processors have populated the request.
//
// Currently handles:
//   - SystemPrompt=false: converts system-role messages to user messages
//     with an "Instructions: ..." prefix. Required for o1/o3 models.
//   - Temperature=false: zeroes the temperature field.
func CapabilitiesProcessor() ContextProcessor {
	return ProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, _ *session.Session, req *llm.LLMRequest) error {
			if p == nil {
				return nil
			}
			caps := p.Capabilities(model)

			if !caps.SystemPrompt {
				convertSystemMessages(req)
			}
			if !caps.Temperature {
				req.Temperature = 0
			}
			return nil
		},
	)
}

// convertSystemMessages rewrites system-role messages as user messages with
// an "Instructions: ..." prefix, preserving ordering. Used for models that
// do not accept the system role (e.g., o1, o3).
func convertSystemMessages(req *llm.LLMRequest) {
	for i, m := range req.Messages {
		if m.Role == llm.RoleSystem {
			req.Messages[i] = llm.Message{
				Role:    llm.RoleUser,
				Content: fmt.Sprintf("Instructions:\n%s", m.Content),
			}
		}
	}
}
