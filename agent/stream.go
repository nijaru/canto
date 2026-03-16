package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Streamer is implemented by agents that support token-by-token streaming.
// Runner and other orchestrators check for this interface via type assertion
// and use StreamTurn when present; non-streaming Turn is the fallback.
type Streamer interface {
	StreamTurn(
		ctx context.Context,
		sess *session.Session,
		chunkFn func(*llm.Chunk),
	) (StepResult, error)
}

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

	// Execute tool calls in parallel and append results to the session.
	return a.runTools(ctx, s, calls)
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
