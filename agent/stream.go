package agent

import (
	"context"
	"fmt"
	"strings"

	ccontext "github.com/nijaru/canto/context"
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
) (res StepResult, err error) {
	defer func() {
		data := session.StepCompletedData{
			AgentID: a.ID(),
			Usage:   res.Usage,
		}
		if err != nil {
			data.Error = err.Error()
		}
		_ = s.Append(ctx, session.NewStepCompletedEvent(s.ID(), data))
	}()

	req := &llm.Request{
		Model: a.model,
	}

	if err = a.builder.Build(ctx, a.provider, a.model, s, req); err != nil {
		return
	}

	cacheFingerprint, err := ccontext.FingerprintPromptCache(s, req)
	if err != nil {
		return StepResult{}, err
	}
	if err := s.Append(ctx, session.NewStepStartedEvent(s.ID(), session.StepStartedData{
		AgentID: a.ID(),
		Model:   a.model,
		PromptCache: session.PromptCacheData{
			PrefixHash:     cacheFingerprint.PrefixHash,
			ToolSchemaHash: cacheFingerprint.ToolSchemaHash,
		},
	})); err != nil {
		return StepResult{}, err
	}

	stream, err := a.provider.Stream(ctx, req)
	if err != nil {
		return
	}
	defer stream.Close()

	// Assemble the complete response from chunks.
	// Tool calls are accumulated by ID — each delta updates the same entry.
	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder
	var usage llm.Usage
	var thinkingBlocks []llm.ThinkingBlock
	assembledCalls := make(map[string]llm.Call) // keyed by call ID
	callOrder := make([]string, 0)              // preserve insertion order

	for {
		chunk, ok := stream.Next()
		if !ok {
			break
		}
		if chunk.Content != "" {
			contentBuilder.WriteString(chunk.Content)
		}
		if chunk.Reasoning != "" {
			reasoningBuilder.WriteString(chunk.Reasoning)
		}
		for _, block := range chunk.ThinkingBlocks {
			if len(thinkingBlocks) == 0 || block.Signature != "" {
				thinkingBlocks = append(thinkingBlocks, block)
			} else {
				last := &thinkingBlocks[len(thinkingBlocks)-1]
				last.Thinking += block.Thinking
			}
		}
		if chunk.Usage != nil {
			usage.InputTokens += chunk.Usage.InputTokens
			usage.OutputTokens += chunk.Usage.OutputTokens
			usage.TotalTokens += chunk.Usage.TotalTokens
			usage.Cost += chunk.Usage.Cost
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
	if err = stream.Err(); err != nil {
		err = fmt.Errorf("stream: %w", err)
		return
	}

	// Reconstruct ordered calls slice.
	calls := make([]llm.Call, 0, len(callOrder))
	for _, id := range callOrder {
		calls = append(calls, assembledCalls[id])
	}

	// Record assistant message.
	msg := llm.Message{
		Role:           llm.RoleAssistant,
		Content:        contentBuilder.String(),
		Reasoning:      reasoningBuilder.String(),
		ThinkingBlocks: thinkingBlocks,
		Calls:          calls,
	}
	e := session.NewEvent(s.ID(), session.MessageAdded, msg)
	e.Cost = usage.Cost
	if err = s.Append(ctx, e); err != nil {
		return
	}
	llm.RecordUsage(ctx, req.Model, usage)

	// Execute tool calls in parallel and append results to the session.
	handoffTargets := getHandoffTargets(a.tools)
	res, err = runTools(
		ctx,
		s,
		calls,
		a.tools,
		a.hooks,
		a.approvals,
		handoffTargets,
		a.maxParallelTools,
	)
	res.Usage = usage // Restore usage as RunTools only returns results/handoff

	return
}

// StreamTurn executes one or more streaming steps until the agent finishes,
// a handoff is requested, or MaxSteps is reached.
// chunkFn receives content chunks from each step; pass nil to ignore.
func (a *BaseAgent) StreamTurn(
	ctx context.Context,
	s *session.Session,
	chunkFn func(*llm.Chunk),
) (res StepResult, err error) {
	if err := s.Append(ctx, session.NewTurnStartedEvent(s.ID(), session.TurnStartedData{
		AgentID: a.ID(),
	})); err != nil {
		return StepResult{}, err
	}

	var steps int
	var totalUsage llm.Usage
	defer func() {
		data := session.TurnCompletedData{
			AgentID: a.ID(),
			Steps:   steps,
			Usage:   totalUsage,
		}
		if err != nil {
			data.Error = err.Error()
		}
		_ = s.Append(ctx, session.NewTurnCompletedEvent(s.ID(), data))
	}()

	for steps < a.maxSteps {
		res, err = a.StreamStep(ctx, s, chunkFn)
		if err != nil {
			return
		}
		steps++
		totalUsage.InputTokens += res.Usage.InputTokens
		totalUsage.OutputTokens += res.Usage.OutputTokens
		totalUsage.TotalTokens += res.Usage.TotalTokens
		totalUsage.Cost += res.Usage.Cost

		if res.Handoff != nil {
			break
		}

		last, ok := s.LastMessage()
		if !ok || last.Role != llm.RoleTool {
			break
		}
	}

	if steps >= a.maxSteps {
		res.Usage = totalUsage
		err = fmt.Errorf("%w (%d)", ErrMaxSteps, a.maxSteps)
		return
	}
	res.Usage = totalUsage

	if msg, ok := s.LastAssistantMessage(); ok {
		res.Content = msg.Content
	}

	if a.hooks != nil {
		a.hooks.Run(ctx, hook.EventStop, hook.SessionMeta{ID: s.ID()}, nil)
	}

	return
}
