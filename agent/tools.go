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
	// Dispatch all calls concurrently. Results land in a fixed-size slice so
	// tool messages are appended to the session in deterministic call order.
	results := make([]toolResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(i int, call llm.ToolCall) {
			defer wg.Done()
			var output string

			if err := s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeToolExecutionStarted, map[string]any{
				"tool": call.Function.Name,
				"args": call.Function.Arguments,
				"id":   call.ID,
			})); err != nil {
				results[i] = toolResult{err: err}
				return
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
					results[i] = toolResult{
						call: call,
						err:  fmt.Errorf("hook blocked tool %q: %w", call.Function.Name, err),
					}
					return
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
				toolOutput, execErr := r.Execute(
					ctx,
					call.Function.Name,
					call.Function.Arguments,
				)
				output += toolOutput
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
								"output": toolOutput,
							},
						)
						if hookErr != nil {
							slog.Warn("PostToolUse hook failed", "tool", call.Function.Name, "error", hookErr)
						}
					}
				}
			} else {
				output = fmt.Sprintf("Error: no tool registry configured; cannot execute %q", call.Function.Name)
			}
			results[i] = toolResult{call: call, output: output}

			if err := s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeToolExecutionCompleted, map[string]any{
				"tool":   call.Function.Name,
				"id":     call.ID,
				"output": output,
			})); err != nil {
				results[i].err = err
			}
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
