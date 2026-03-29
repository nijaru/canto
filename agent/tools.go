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

	results := make([]toolResult, len(calls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)

	for i, call := range calls {
		wg.Add(1)
		go func(i int, call llm.Call) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[i] = toolResult{
						call: call,
						err:  fmt.Errorf("tool %q panicked: %v", call.Function.Name, r),
					}
				}
			}()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = toolResult{call: call, err: ctx.Err()}
				return
			}

			results[i] = executeToolWithHooks(ctx, s, call, r, h, approvals)
		}(i, call)
	}
	wg.Wait()

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

func executeToolWithHooks(
	ctx context.Context,
	s *session.Session,
	call llm.Call,
	r *tool.Registry,
	h *hook.Runner,
	approvals *approval.Manager,
) toolResult {
	var output string

	if err := s.Append(ctx, session.NewEvent(s.ID(), session.ToolStarted, map[string]any{
		"tool": call.Function.Name,
		"args": call.Function.Arguments,
		"id":   call.ID,
	})); err != nil {
		return toolResult{err: err}
	}

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
			return toolResult{
				call: call,
				err:  fmt.Errorf("hook blocked tool %q: %w", call.Function.Name, err),
			}
		}
		for _, res := range hookResults {
			if res.Output != "" {
				output += fmt.Sprintf(
					"<hook_context name=%q>\n%s\n</hook_context>\n",
					"PreToolUse",
					res.Output,
				)
			}
		}
	}

	if r != nil {
		t, ok := r.Get(call.Function.Name)
		if !ok {
			output = fmt.Sprintf("Error: tool %q not found", call.Function.Name)
		} else {
			if approvals != nil {
				if gated, ok := t.(tool.ApprovalTool); ok {
					req, needsApproval, err := gated.ApprovalRequirement(call.Function.Arguments)
					if err != nil {
						return toolResult{
							call: call,
							err:  fmt.Errorf("approval requirement for %q: %w", call.Function.Name, err),
						}
					}
					if needsApproval {
						res, err := approvals.Request(ctx, s, call.Function.Name, call.Function.Arguments, req)
						if err != nil {
							return toolResult{call: call, err: err}
						}
						if denyErr := res.Error(); denyErr != nil {
							return toolResult{call: call, err: denyErr}
						}
					}
				}
			}

			var execErr error
			if st, ok := t.(tool.StreamingTool); ok {
				output, execErr = st.ExecuteStreaming(ctx, call.Function.Arguments, func(delta string) error {
					return s.Append(ctx, session.NewEvent(s.ID(), session.ToolOutputDelta, map[string]any{
						"tool":  call.Function.Name,
						"id":    call.ID,
						"delta": delta,
					}))
				})
			} else {
				output, execErr = t.Execute(ctx, call.Function.Arguments)
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
						slog.Warn(
							"PostToolUseFailure hook failed",
							"tool", call.Function.Name,
							"error", hookErr,
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
						slog.Warn("PostToolUse hook failed", "tool", call.Function.Name, "error", hookErr)
					}
				}
			}
		}
	} else {
		output = fmt.Sprintf("Error: no tool registry configured; cannot execute %q", call.Function.Name)
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
