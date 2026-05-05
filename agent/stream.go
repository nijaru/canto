package agent

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tracing"
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
	cfg := a.stepConfig()
	defer func() { appendStepCompleted(ctx, s, cfg.ID, res, err) }()

	prepared, err := prepareStep(ctx, s, cfg)
	if err != nil {
		return
	}
	ctx = prepared.Context

	stream, err := prepared.Provider.Stream(ctx, prepared.Request)
	if err != nil {
		return
	}
	defer stream.Close()

	var acc llm.StreamAccumulator

	for {
		if err = ctx.Err(); err != nil {
			return
		}
		chunk, ok := stream.Next()
		if !ok {
			break
		}
		if err = ctx.Err(); err != nil {
			return
		}
		acc.Add(chunk)
		if chunkFn != nil {
			chunkFn(chunk)
		}
	}
	if err = stream.Err(); err != nil {
		err = fmt.Errorf("stream: %w", err)
		return
	}
	if err = ctx.Err(); err != nil {
		return
	}

	resp := acc.Response()

	assistantMessageID, appended, err := appendAssistantResponse(
		ctx,
		s,
		prepared.Provider.ID(),
		prepared.Request,
		resp,
	)
	if err != nil {
		return
	}
	if !appended {
		res.Usage = resp.Usage
		return
	}

	if err = ctx.Err(); err != nil {
		return
	}
	handoffTargets := getHandoffTargets(a.tools)
	res, err = runTools(
		ctx,
		s,
		resp.Calls,
		a.tools,
		a.hooks,
		a.approvals,
		handoffTargets,
		a.maxParallelTools,
		assistantMessageID,
	)
	res.Usage = resp.Usage // Restore usage as RunTools only returns results/handoff

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
	ctx, sessionSpan := tracing.StartSession(ctx, a.ID(), s.ID(), a.model)
	defer func() { tracing.EndSession(sessionSpan, err) }()
	ctx, turnSpan := tracing.StartTurn(ctx, a.ID(), s.ID(), a.model)
	defer func() { tracing.EndTurn(turnSpan, err) }()

	state := turnState{}
	defer func() {
		data := session.TurnCompletedData{
			AgentID:        a.ID(),
			Steps:          state.steps,
			Usage:          state.totalUsage,
			TurnStopReason: string(state.stopReason),
		}
		if err != nil {
			data.Error = err.Error()
		}
		_ = s.Append(context.WithoutCancel(ctx), session.NewTurnCompletedEvent(s.ID(), data))
	}()

	if err := s.Append(ctx, session.NewTurnStartedEvent(s.ID(), session.TurnStartedData{
		AgentID: a.ID(),
	})); err != nil {
		return StepResult{}, err
	}

	if a.maxSteps > 0 {
		for state.steps < a.maxSteps {
			if err := ctx.Err(); err != nil {
				return StepResult{}, err
			}

			res, err = a.StreamStep(ctx, s, chunkFn)
			if err != nil {
				outcome := state.handleStepError(
					ctx,
					s,
					a.ID(),
					a.provider,
					a.maxEscalations,
					err,
				)
				if outcome.retry {
					err = nil
					continue
				}
				if outcome.err != nil {
					err = outcome.err
					return
				}
				res = outcome.result
				err = nil
				if outcome.stop {
					break
				}
				return
			}
			outcome := state.handleStepResult(s, res, a.maxSteps)
			res = outcome.result
			if outcome.stop {
				break
			}
		}
	} else {
		state.stopReason = TurnStopMaxTurnsHit
	}

	res = finalizeTurnResult(s, state, res)

	if a.hooks != nil {
		a.hooks.Run(ctx, hook.EventStop, hook.SessionMeta{ID: s.ID()}, nil)
	}

	return
}
