package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/tracing"
)

// preflightTools runs sequential validation for all tool calls: registry lookup,
// PreToolUse hooks, and approval checks. This runs in source order so that hooks
// can depend on the results of sibling validations.
func preflightTools(
	ctx context.Context,
	s *session.Session,
	calls []llm.Call,
	r *tool.Registry,
	h *hook.Runner,
	approvals *approval.Gate,
	assistantMessageID string,
) []preflightResult {
	results := make([]preflightResult, len(calls))
	fence := tool.ACRFence{}

	for i, call := range calls {
		results[i].call = call

		// PreToolUse hooks — run sequentially so they can inspect sibling state.
		var metadata tool.Metadata
		if r != nil {
			metadata, _ = r.Metadata(call.Function.Name)
		}
		var hookOutput string
		if h != nil {
			hookResults, err := h.Run(
				ctx,
				hook.EventPreToolUse,
				hook.SessionMeta{ID: s.ID()},
				toolHookData(call, metadata, nil),
			)
			hookOutput = hookContextOutput(hook.EventPreToolUse, hookResults)
			applyPreToolHookData(&call, hookResults)
			results[i].call = call
			if err != nil {
				if isPreflightAbort(err) {
					results[i].err = err
					continue
				}
				setPreflightCompletionError(&results[i], hookOutput, err.Error())
				continue
			}
		}

		// Registry lookup.
		if r == nil {
			setPreflightCompletionError(
				&results[i],
				hookOutput,
				fmt.Sprintf("no tool registry configured; cannot execute %q", call.Function.Name),
			)
			continue
		}
		t, ok := r.Get(call.Function.Name)
		if !ok && call.Function.Name == tool.SearchToolName {
			t = tool.NewSearchTool(r)
			ok = true
		}
		if !ok {
			setPreflightCompletionError(
				&results[i],
				hookOutput,
				fmt.Sprintf("tool %q not found", call.Function.Name),
			)
			continue
		}
		metadata = tool.MetadataFor(t)
		t = tracing.WrapTool(t)
		results[i].tool = t
		metadata = tool.MetadataFor(t)
		results[i].metadata = metadata

		// Approval check.
		if approvals != nil {
			if gated, ok := t.(approval.RequirementProvider); ok {
				req, needsApproval, err := gated.ApprovalRequirement(call.Function.Arguments)
				if err != nil {
					setPreflightCompletionError(
						&results[i],
						hookOutput,
						fmt.Sprintf("approval requirement for %q: %v", call.Function.Name, err),
					)
					continue
				}
				if needsApproval {
					res, err := approvals.Request(
						ctx,
						s,
						call.Function.Name,
						call.Function.Arguments,
						req,
					)
					if err != nil {
						if isPreflightAbort(err) {
							results[i].err = err
							continue
						}
						setPreflightCompletionError(&results[i], hookOutput, err.Error())
						continue
					}
					if denyErr := res.Error(); denyErr != nil {
						setPreflightCompletionError(&results[i], hookOutput, denyErr.Error())
						continue
					}
				}
			}
		}

		idempotencyKey := toolIdempotencyKey(s.ID(), assistantMessageID, call, i)
		results[i].idempotencyKey = idempotencyKey
		decision, err := fence.Validate(s, idempotencyKey)
		if err != nil {
			setPreflightCompletionError(&results[i], hookOutput, err.Error())
			continue
		}
		if decision.Action == tool.ReplayReuse {
			results[i].output = hookOutput + decision.Output
			results[i].skipExecute = true
			continue
		}

		results[i].output = hookOutput
	}

	return results
}

func setPreflightCompletionError(res *preflightResult, hookOutput, errorText string) {
	res.output = hookOutput + fmt.Sprintf("Error: %s", errorText)
	res.completionError = errorText
	res.skipExecute = true
}

func isPreflightAbort(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
