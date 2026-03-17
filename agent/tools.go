package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

type toolResult struct {
	call   llm.ToolCall
	output string
	err    error
}

// runTools executes tool calls in parallel, appends their results to the
// session, and returns a StepResult with any detected handoff.
// It is the shared implementation used by both Step and StreamStep.
func (a *BaseAgent) runTools(
	ctx context.Context,
	s *session.Session,
	calls []llm.ToolCall,
) (StepResult, error) {
	handoffTargets := getHandoffTargets(a.Tools)
	return RunTools(ctx, s, calls, a.Tools, a.Hooks, handoffTargets)
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

// RunTools executes tool calls in parallel and persists results to the session.
func RunTools(
	ctx context.Context,
	s *session.Session,
	calls []llm.ToolCall,
	r *tool.Registry,
	h *hook.Runner,
	handoffTargets []string,
) (StepResult, error) {
	results := make([]toolResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(i int, call llm.ToolCall) {
			defer wg.Done()
			results[i] = executeToolWithHooks(ctx, s, call, r, h)
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
		if err := s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeMessageAdded, toolMsg)); err != nil {
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
	call llm.ToolCall,
	r *tool.Registry,
	h *hook.Runner,
) toolResult {
	var output string

	if err := s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeToolExecutionStarted, map[string]any{
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
			var execErr error
			if st, ok := t.(tool.StreamingTool); ok {
				output, execErr = st.ExecuteStreaming(ctx, call.Function.Arguments, func(delta string) error {
					return s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeToolOutputDelta, map[string]any{
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
	if err := s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeToolExecutionCompleted, map[string]any{
		"tool":   call.Function.Name,
		"id":     call.ID,
		"output": output,
	})); err != nil {
		res.err = err
	}
	return res
}
