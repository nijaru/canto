package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

type toolResult struct {
	call   llm.Call
	output string
	err    error
}

// preflightResult holds the outcome of sequential validation for one tool call.
type preflightResult struct {
	call        llm.Call
	output      string // non-empty if preflight produced output (error or hook context)
	err         error  // non-nil if preflight blocked execution
	skipExecute bool   // true if execution should be skipped (preflight handled it)
}

func getHandoffTargets(r *tool.Registry) []string {
	if r == nil {
		return nil
	}
	var targets []string
	for _, spec := range r.Specs() {
		if after, ok := strings.CutPrefix(spec.Name, "transfer_to_"); ok {
			targets = append(targets, after)
		}
	}
	return targets
}

func runTools(
	ctx context.Context,
	s *session.Session,
	calls []llm.Call,
	r *tool.Registry,
	h *hook.Runner,
	approvals *approval.Manager,
	handoffTargets []string,
	maxParallel int,
) (StepResult, error) {
	if maxParallel <= 0 {
		maxParallel = 10
	}

	// Phase 1: sequential preflight — validate, run PreToolUse hooks, check approvals.
	preflight := preflightTools(ctx, s, calls, r, h, approvals)

	// Phase 2: concurrent execution — fire all approved tools in parallel.
	results := executeTools(ctx, s, preflight, r, h, maxParallel)

	var toolMsgs []llm.Message
	for _, r := range results {
		if r.err != nil {
			return StepResult{}, r.err
		}
		toolMsg := llm.Message{
			Role:    llm.RoleTool,
			Content: r.output,
			ToolID:  r.call.ID,
			Name:    r.call.Function.Name,
		}
		toolMsgs = append(toolMsgs, toolMsg)
		if err := s.Append(ctx, session.NewEvent(s.ID(), session.MessageAdded, toolMsg)); err != nil {
			return StepResult{}, err
		}
	}

	handoff := extractHandoff(s, handoffTargets)
	return StepResult{
		Handoff:     handoff,
		ToolResults: toolMsgs,
	}, nil
}

// preflightTools runs sequential validation for all tool calls: registry lookup,
// PreToolUse hooks, and approval checks. This runs in source order so that hooks
// can depend on the results of sibling validations.
func preflightTools(
	ctx context.Context,
	s *session.Session,
	calls []llm.Call,
	r *tool.Registry,
	h *hook.Runner,
	approvals *approval.Manager,
) []preflightResult {
	results := make([]preflightResult, len(calls))

	for i, call := range calls {
		results[i].call = call

		if err := s.Append(ctx, session.NewToolStartedEvent(s.ID(), session.ToolStartedData{
			Tool:      call.Function.Name,
			Arguments: call.Function.Arguments,
			ID:        call.ID,
		})); err != nil {
			results[i].err = err
			results[i].skipExecute = true
			continue
		}

		// PreToolUse hooks — run sequentially so they can inspect sibling state.
		var hookOutput string
		if h != nil {
			hookResults, err := h.Run(
				ctx,
				hook.EventPreToolUse,
				hook.SessionMeta{ID: s.ID()},
				map[string]any{
					"tool": call.Function.Name,
					"args": call.Function.Arguments,
				},
			)
			if err != nil {
				results[i].err = fmt.Errorf("hook blocked tool %q: %w", call.Function.Name, err)
				results[i].skipExecute = true
				continue
			}
			for _, res := range hookResults {
				if res.Output != "" {
					hookOutput += fmt.Sprintf(
						"<hook_context name=%q>\n%s\n</hook_context>\n",
						"PreToolUse",
						res.Output,
					)
				}
			}
		}

		// Registry lookup.
		if r == nil {
			results[i].output = fmt.Sprintf(
				"Error: no tool registry configured; cannot execute %q",
				call.Function.Name,
			)
			results[i].skipExecute = true
			continue
		}
		t, ok := r.Get(call.Function.Name)
		if !ok {
			results[i].output = fmt.Sprintf("Error: tool %q not found", call.Function.Name)
			results[i].skipExecute = true
			continue
		}

		// Approval check.
		if approvals != nil {
			if gated, ok := t.(tool.ApprovalTool); ok {
				req, needsApproval, err := gated.ApprovalRequirement(call.Function.Arguments)
				if err != nil {
					results[i].err = fmt.Errorf(
						"approval requirement for %q: %w",
						call.Function.Name,
						err,
					)
					results[i].skipExecute = true
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
						results[i].err = err
						results[i].skipExecute = true
						continue
					}
					if denyErr := res.Error(); denyErr != nil {
						results[i].err = denyErr
						results[i].skipExecute = true
						continue
					}
				}
			}
		}

		results[i].output = hookOutput
	}

	return results
}

// executeTools runs approved tool calls concurrently with a semaphore.
// PreToolUse hooks and approvals have already been resolved in preflightTools.
func executeTools(
	ctx context.Context,
	s *session.Session,
	preflight []preflightResult,
	r *tool.Registry,
	h *hook.Runner,
	maxParallel int,
) []toolResult {
	results := make([]toolResult, len(preflight))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)

	for i, pf := range preflight {
		// If preflight blocked or skipped execution, carry the result forward.
		if pf.skipExecute || pf.err != nil {
			results[i] = toolResult{call: pf.call, output: pf.output, err: pf.err}
			continue
		}

		wg.Add(1)
		go func(i int, pf preflightResult) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[i] = toolResult{
						call: pf.call,
						err:  fmt.Errorf("tool %q panicked: %v", pf.call.Function.Name, r),
					}
				}
			}()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = toolResult{call: pf.call, err: ctx.Err()}
				return
			}

			results[i] = executeTool(ctx, s, pf, r, h)
		}(i, pf)
	}
	wg.Wait()

	return results
}

// executeTool runs a single tool and its PostToolUse hooks. Preflight
// validation has already completed; this only performs I/O.
func executeTool(
	ctx context.Context,
	s *session.Session,
	pf preflightResult,
	r *tool.Registry,
	h *hook.Runner,
) toolResult {
	call := pf.call
	output := pf.output // hook context from preflight

	t, ok := r.Get(call.Function.Name)
	if !ok {
		// Should not happen — preflight already checked — but guard defensively.
		output = fmt.Sprintf("Error: tool %q not found", call.Function.Name)
		return toolResult{call: call, output: output}
	}

	var execErr error
	if st, ok := t.(tool.StreamingTool); ok {
		for delta, err := range st.ExecuteStreaming(ctx, call.Function.Arguments) {
			if err != nil {
				execErr = err
				break
			}
			output += delta
			_ = s.Append(
				ctx,
				session.NewEvent(s.ID(), session.ToolOutputDelta, map[string]any{
					"tool":  call.Function.Name,
					"id":    call.ID,
					"delta": delta,
				}),
			)
		}
	} else {
		var execOutput string
		execOutput, execErr = t.Execute(ctx, call.Function.Arguments)
		output += execOutput
	}

	if execErr != nil {
		output = fmt.Sprintf("%s\nError: %s", output, execErr)
		if h != nil {
			_, hookErr := h.Run(
				ctx,
				hook.EventPostToolUseFailure,
				hook.SessionMeta{ID: s.ID()},
				map[string]any{
					"tool":  call.Function.Name,
					"error": execErr.Error(),
				},
			)
			if hookErr != nil {
				slog.LogAttrs(
					ctx,
					slog.LevelWarn,
					"PostToolUseFailure hook failed",
					slog.String("tool", call.Function.Name),
					slog.Any("error", hookErr),
				)
			}
		}
	} else {
		if h != nil {
			_, hookErr := h.Run(
				ctx,
				hook.EventPostToolUse,
				hook.SessionMeta{ID: s.ID()},
				map[string]any{
					"tool":   call.Function.Name,
					"output": output,
				},
			)
			if hookErr != nil {
				slog.LogAttrs(
					ctx,
					slog.LevelWarn,
					"PostToolUse hook failed",
					slog.String("tool", call.Function.Name),
					slog.Any("error", hookErr),
				)
			}
		}
	}

	res := toolResult{call: call, output: output}
	if err := s.Append(ctx, session.NewToolCompletedEvent(s.ID(), session.ToolCompletedData{
		Tool:   call.Function.Name,
		ID:     call.ID,
		Output: output,
	})); err != nil {
		res.err = err
	}
	return res
}

// executeToolWithHooks is kept for backward compatibility with callers that
// bypass the two-phase model. New code should use preflightTools + executeTools.
func executeToolWithHooks(
	ctx context.Context,
	s *session.Session,
	call llm.Call,
	r *tool.Registry,
	h *hook.Runner,
	approvals *approval.Manager,
) toolResult {
	pfResults := preflightTools(ctx, s, []llm.Call{call}, r, h, approvals)
	execResults := executeTools(ctx, s, pfResults, r, h, 1)
	return execResults[0]
}
