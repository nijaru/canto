package agent

import (
	"context"
	"iter"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/tracing"
)

type stepConfig struct {
	ID               string
	Model            string
	Provider         llm.Provider
	Builder          *prompt.Builder
	Tools            *tool.Registry
	Hooks            *hook.Runner
	Approvals        *approval.Gate
	MaxParallelTools int
}

type modeler interface {
	Model() string
}

func agentModel(a Agent) string {
	if m, ok := a.(modeler); ok {
		return m.Model()
	}
	return ""
}

// Step executes a single turn of the agentic loop and returns its result.
func (a *BaseAgent) Step(ctx context.Context, s *session.Session) (StepResult, error) {
	return runStep(ctx, s, a.stepConfig())
}

func runStep(ctx context.Context, s *session.Session, cfg stepConfig) (res StepResult, err error) {
	defer func() { appendStepCompleted(ctx, s, cfg.ID, res, err) }()

	prepared, err := prepareStep(ctx, s, cfg)
	if err != nil {
		return
	}
	ctx = prepared.Context

	resp, err := prepared.Provider.Generate(ctx, prepared.Request)
	if err != nil {
		return
	}
	if err = ctx.Err(); err != nil {
		return
	}
	res.Usage = resp.Usage

	assistantMessageID, appended, err := appendAssistantResponse(
		ctx,
		s,
		prepared.Provider.ID(),
		prepared.Request,
		*resp,
	)
	if err != nil || !appended {
		return
	}

	if err = ctx.Err(); err != nil {
		return
	}
	handoffTargets := getHandoffTargets(cfg.Tools)
	res, err = runTools(
		ctx,
		s,
		resp.Calls,
		cfg.Tools,
		cfg.Hooks,
		cfg.Approvals,
		handoffTargets,
		cfg.MaxParallelTools,
		assistantMessageID,
	)
	res.Usage = resp.Usage // Restore usage as RunTools only returns results/handoff

	return
}

// Turn executes one or more steps until the agent finishes or a handoff is requested.
func (a *BaseAgent) Turn(ctx context.Context, s *session.Session) (StepResult, error) {
	return RunTurn(ctx, a, s, a.maxSteps, a.provider, a.maxEscalations)
}

// Run executes the agent loop as an iterator over per-step results.
// Callers can stop early by breaking the range loop; the session turn start/end
// events are still managed internally.
func (a *BaseAgent) Run(ctx context.Context, s *session.Session) iter.Seq2[StepResult, error] {
	return Run(ctx, a, s, a.maxSteps, a.provider, a.maxEscalations)
}

// Run executes the agent loop as an iterator over per-step results.
// It is the low-level generator form of RunTurn.
func Run(
	ctx context.Context,
	a Agent,
	s *session.Session,
	maxSteps int,
	provider llm.Provider,
	maxEscalations int,
) iter.Seq2[StepResult, error] {
	return func(yield func(StepResult, error) bool) {
		model := agentModel(a)
		var runErr error
		ctx, sessionSpan := tracing.StartSession(ctx, a.ID(), s.ID(), model)
		defer func() { tracing.EndSession(sessionSpan, runErr) }()
		ctx, turnSpan := tracing.StartTurn(ctx, a.ID(), s.ID(), model)
		defer func() { tracing.EndTurn(turnSpan, runErr) }()

		state := turnState{}
		defer func() {
			data := session.TurnCompletedData{
				AgentID:        a.ID(),
				Steps:          state.steps,
				Usage:          state.totalUsage,
				TurnStopReason: string(state.stopReason),
			}
			if runErr != nil {
				data.Error = runErr.Error()
			}
			_ = s.Append(context.WithoutCancel(ctx), session.NewTurnCompletedEvent(s.ID(), data))
		}()

		if err := s.Append(ctx, session.NewTurnStartedEvent(s.ID(), session.TurnStartedData{
			AgentID: a.ID(),
		})); err != nil {
			runErr = err
			yield(StepResult{}, err)
			return
		}

		if maxSteps <= 0 {
			state.stopReason = TurnStopMaxTurnsHit
			yield(StepResult{TurnStopReason: state.stopReason}, nil)
			return
		}

		for state.steps < maxSteps {
			if err := ctx.Err(); err != nil {
				runErr = err
				yield(StepResult{}, err)
				return
			}

			res, err := a.Step(ctx, s)
			if err != nil {
				outcome := state.handleStepError(
					ctx,
					s,
					a.ID(),
					provider,
					maxEscalations,
					err,
				)
				if outcome.retry {
					continue
				}
				if outcome.err != nil {
					runErr = outcome.err
					yield(outcome.result, outcome.yieldErr)
					return
				}
				yield(outcome.yieldResult, nil)
				return
			}
			outcome := state.handleStepResult(s, res, maxSteps)
			if !yield(outcome.yieldResult, nil) {
				return
			}
			if outcome.stop {
				return
			}
		}
	}
}

// RunTurn executes one or more steps until the agent finishes (no pending tool
// calls) or a handoff is requested, or maxSteps is reached.
// It can run any Agent implementation that satisfies the interface.
func RunTurn(
	ctx context.Context,
	a Agent,
	s *session.Session,
	maxSteps int,
	provider llm.Provider,
	maxEscalations int,
) (res StepResult, err error) {
	var steps int
	var totalUsage llm.Usage
	for stepRes, stepErr := range Run(ctx, a, s, maxSteps, provider, maxEscalations) {
		if stepErr != nil {
			res = stepRes
			res.Usage = totalUsage
			err = stepErr
			return
		}
		if stepRes.TurnStopReason == TurnStopBudgetExhausted {
			res = stepRes
			break
		}
		res = stepRes
		steps++
		totalUsage.InputTokens += stepRes.Usage.InputTokens
		totalUsage.OutputTokens += stepRes.Usage.OutputTokens
		totalUsage.TotalTokens += stepRes.Usage.TotalTokens
		totalUsage.CacheReadTokens += stepRes.Usage.CacheReadTokens
		totalUsage.CacheCreationTokens += stepRes.Usage.CacheCreationTokens
		totalUsage.Cost += stepRes.Usage.Cost
	}
	res.Usage = totalUsage
	if steps > 0 {
		if msg, ok := s.LastAssistantMessage(); ok {
			res.Content = msg.Content
		}
	}
	return
}
