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

// StreamStep executes a single turn of the agentic loop using streaming.
// chunkFn is called for each content chunk as it arrives; pass nil to ignore.
// Tool calls are assembled from the stream and executed after completion.
func (a *BaseAgent) StreamStep(
	ctx context.Context,
	s *session.Session,
	chunkFn func(*llm.Chunk),
) (StepResult, error) {
	req := &llm.LLMRequest{
		Model: a.Model,
	}

	if err := a.Builder.Build(ctx, a.Provider, a.Model, s, req); err != nil {
		return StepResult{}, err
	}

	stream, err := a.Provider.Stream(ctx, req)
	if err != nil {
		return StepResult{}, err
	}
	defer stream.Close()

	// Assemble the complete response from chunks.
	// Tool calls are accumulated by ID — each delta updates the same entry.
	var contentBuilder strings.Builder
	assembledCalls := make(map[string]llm.ToolCall) // keyed by call ID
	callOrder := make([]string, 0)                  // preserve insertion order

	for {
		chunk, ok := stream.Next()
		if !ok {
			break
		}
		if chunk.Content != "" {
			contentBuilder.WriteString(chunk.Content)
		}
		for _, call := range chunk.Calls {
			if call.ID != "" {
				if _, exists := assembledCalls[call.ID]; !exists {
					callOrder = append(callOrder, call.ID)
				}
				assembledCalls[call.ID] = call
			}
		}
		if chunkFn != nil {
			chunkFn(chunk)
		}
	}
	if err := stream.Err(); err != nil {
		return StepResult{}, fmt.Errorf("stream: %w", err)
	}

	// Reconstruct ordered calls slice.
	calls := make([]llm.ToolCall, 0, len(callOrder))
	for _, id := range callOrder {
		calls = append(calls, assembledCalls[id])
	}

	// Record assistant message.
	msg := llm.Message{
		Role:    llm.RoleAssistant,
		Content: contentBuilder.String(),
		Calls:   calls,
	}
	e := session.NewEvent(s.ID(), session.EventTypeMessageAdded, msg)
	s.Append(e)

	// Collect handoff targets.
	var handoffTargets []string
	if a.Tools != nil {
		for _, spec := range a.Tools.Specs() {
			if after, ok := strings.CutPrefix(spec.Name, "transfer_to_"); ok {
				handoffTargets = append(handoffTargets, after)
			}
		}
	}

	// Execute tools in parallel.
	type toolResult struct {
		call   llm.ToolCall
		output string
		err    error
	}
	results := make([]toolResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(i int, call llm.ToolCall) {
			defer wg.Done()
			var output string

			if a.Hooks != nil {
				hookResults, err := a.Hooks.Run(
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

			if a.Tools != nil {
				var execErr error
				toolOutput, execErr := a.Tools.Execute(
					ctx,
					call.Function.Name,
					call.Function.Arguments,
				)
				output += toolOutput
				if execErr != nil {
					output = fmt.Sprintf("%s\nError: %s", output, execErr)
					if a.Hooks != nil {
						_, hookErr := a.Hooks.Run(
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
					if a.Hooks != nil {
						_, hookErr := a.Hooks.Run(ctx, hook.EventPostToolUse, hook.SessionMeta{ID: s.ID()}, map[string]any{
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

	h := extractHandoff(s, handoffTargets)
	return StepResult{Handoff: h}, nil
}

// StreamTurn executes one or more streaming steps until the agent finishes,
// a handoff is requested, or MaxSteps is reached.
// chunkFn receives content chunks from each step; pass nil to ignore.
func (a *BaseAgent) StreamTurn(
	ctx context.Context,
	s *session.Session,
	chunkFn func(*llm.Chunk),
) (StepResult, error) {
	steps := 0
	var result StepResult
	for steps < a.MaxSteps {
		var err error
		result, err = a.StreamStep(ctx, s, chunkFn)
		if err != nil {
			return StepResult{}, err
		}
		steps++

		if result.Handoff != nil {
			return result, nil
		}

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
		return StepResult{}, fmt.Errorf("%w (%d)", ErrMaxSteps, a.MaxSteps)
	}

	msgs := s.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && len(msgs[i].Calls) == 0 {
			result.Content = msgs[i].Content
			break
		}
	}

	if a.Hooks != nil {
		a.Hooks.Run(ctx, hook.EventStop, hook.SessionMeta{ID: s.ID()}, nil)
	}

	return result, nil
}
