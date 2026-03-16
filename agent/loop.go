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
)

// Step executes a single turn of the agentic loop and returns its result.
// If any tool call produces a Handoff payload targeting a known peer agent,
// the result's Handoff field is set so callers can route accordingly.
func (a *BaseAgent) Step(ctx context.Context, s *session.Session) (StepResult, error) {
	req := &llm.LLMRequest{
		Model: a.Model,
	}

	// Build context
	if err := a.Builder.Build(ctx, a.Provider, a.Model, s, req); err != nil {
		return StepResult{}, err
	}

	resp, err := a.Provider.Generate(ctx, req)
	if err != nil {
		return StepResult{}, err
	}

	// Record assistant response
	msg := llm.Message{
		Role:    llm.RoleAssistant,
		Content: resp.Content,
		Calls:   resp.Calls,
	}
	s.Append(session.NewEvent(s.ID(), session.EventTypeMessageAdded, msg))

	// Execute tools and append results.
	// Collect the target agent IDs for any registered handoff tools
	// (named "transfer_to_{agentID}") so extractHandoff can match them.
	var handoffTargets []string
	if a.Tools != nil {
		for _, spec := range a.Tools.Specs() {
			if after, ok := strings.CutPrefix(spec.Name, "transfer_to_"); ok {
				handoffTargets = append(handoffTargets, after)
			}
		}
	}
	// Execute tools in parallel — frontier models return multiple calls per turn
	// and expect them dispatched concurrently. Results are collected into a
	// fixed-size slice indexed by call position so messages are appended in
	// a deterministic order after all goroutines complete.
	type toolResult struct {
		call   llm.ToolCall
		output string
		err    error
	}
	results := make([]toolResult, len(resp.Calls))
	var wg sync.WaitGroup
	for i, call := range resp.Calls {
		wg.Add(1)
		go func(i int, call llm.ToolCall) {
			defer wg.Done()
			var output string

			if a.Hooks != nil {
				hookResults, err := a.Hooks.Run(ctx, hook.EventPreToolUse, s, map[string]any{
					"tool": call.Function.Name,
					"args": call.Function.Arguments,
				})
				if err != nil {
					results[i] = toolResult{call: call, err: fmt.Errorf("hook blocked tool %q: %w", call.Function.Name, err)}
					return
				}

				// Inject hook output into context as system hint for this tool call if provided
				for _, res := range hookResults {
					if res.Output != "" {
						output += fmt.Sprintf("<hook_context name=%q>\n%s\n</hook_context>\n", "PreToolUse", res.Output)
					}
				}
			}

			if a.Tools != nil {
				var execErr error
				toolOutput, execErr := a.Tools.Execute(ctx, call.Function.Name, call.Function.Arguments)
				output += toolOutput
				if execErr != nil {
					output = fmt.Sprintf("%s\nError: %s", output, execErr)
					if a.Hooks != nil {
						_, hookErr := a.Hooks.Run(ctx, hook.EventPostToolUseFailure, s, map[string]any{
							"tool":  call.Function.Name,
							"error": execErr.Error(),
						})
						if hookErr != nil {
							slog.Warn("PostToolUseFailure hook failed", "tool", call.Function.Name, "error", hookErr)
						}
					}
				} else {
					if a.Hooks != nil {
						_, hookErr := a.Hooks.Run(ctx, hook.EventPostToolUse, s, map[string]any{
							"tool":   call.Function.Name,
							"output": toolOutput,
						})
						if hookErr != nil {
							slog.Warn("PostToolUse hook failed", "tool", call.Function.Name, "error", hookErr)
						}
					}
				}
			} else {
				output = fmt.Sprintf("Error: no tool registry configured; cannot execute %q", call.Function.Name)
			}
			results[i] = toolResult{call: call, output: output}
		}(i, call)
	}
	wg.Wait()

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
		s.Append(session.NewEvent(s.ID(), session.EventTypeMessageAdded, toolMsg))
	}

	// Check for a handoff in the tool results just appended.
	h := extractHandoff(s, handoffTargets)
	return StepResult{Handoff: h}, nil
}

// Turn executes one or more steps until the agent finishes (no pending tool
// calls) or a handoff is requested, or MaxSteps is reached.
// The returned StepResult reflects the final step's outcome.
func (a *BaseAgent) Turn(ctx context.Context, s *session.Session) (StepResult, error) {
	steps := 0
	var result StepResult
	for steps < a.MaxSteps {
		var err error
		result, err = a.Step(ctx, s)
		if err != nil {
			return StepResult{}, err
		}
		steps++

		// If a handoff was requested, stop immediately so the caller can route.
		if result.Handoff != nil {
			return result, nil
		}

		// Continue only if the last message is a tool result (model must
		// process it). Any other role means the agent has finished.
		messages := s.Messages()
		if len(messages) == 0 {
			break
		}
		last := messages[len(messages)-1]
		if last.Role != llm.RoleTool {
			break
		}
	}

	if steps >= a.MaxSteps {
		return StepResult{}, fmt.Errorf("maximum tool calling steps reached (%d)", a.MaxSteps)
	}

	if a.Hooks != nil {
		a.Hooks.Run(ctx, hook.EventStop, s, nil)
	}

	return result, nil
}
