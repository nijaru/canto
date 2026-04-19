package agent

import (
	"context"
	"errors"
	"iter"

	"github.com/nijaru/canto/approval"
	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/tracing"
)

type stepConfig struct {
	ID               string
	Model            string
	Provider         llm.Provider
	Builder          *ccontext.Builder
	Tools            *tool.Registry
	Hooks            *hook.Runner
	Approvals        *approval.Manager
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
	return runStep(ctx, s, stepConfig{
		ID:               a.agentID,
		Model:            a.model,
		Provider:         a.provider,
		Builder:          a.builder,
		Tools:            a.tools,
		Hooks:            a.hooks,
		Approvals:        a.approvals,
		MaxParallelTools: a.maxParallelTools,
	})
}

func runStep(ctx context.Context, s *session.Session, cfg stepConfig) (res StepResult, err error) {
	defer func() {
		data := session.StepCompletedData{
			AgentID: cfg.ID,
			Usage:   res.Usage,
		}
		if err != nil {
			data.Error = err.Error()
		}
		_ = s.Append(ctx, session.NewStepCompletedEvent(s.ID(), data))
	}()

	req := &llm.Request{
		Model: cfg.Model,
	}
	provider := tracing.WrapProvider(cfg.Provider)

	// Build context
	buildCtx, buildSpan := tracing.StartContext(ctx, cfg.ID, s.ID(), cfg.Model)
	if err = cfg.Builder.Build(buildCtx, provider, cfg.Model, s, req); err != nil {
		tracing.EndContext(buildSpan, err)
		return
	}
	tracing.EndContext(buildSpan, nil)
	ctx = buildCtx

	cacheFingerprint, err := ccontext.FingerprintPromptCache(s, req)
	if err != nil {
		return StepResult{}, err
	}
	stepStarted := session.NewStepStartedEvent(s.ID(), session.StepStartedData{
		AgentID: cfg.ID,
		Model:   cfg.Model,
		PromptCache: session.PromptCacheData{
			PrefixHash:     cacheFingerprint.PrefixHash,
			ToolSchemaHash: cacheFingerprint.ToolSchemaHash,
		},
	})
	if err := s.Append(ctx, stepStarted); err != nil {
		return StepResult{}, err
	}

	resp, err := provider.Generate(ctx, req)
	if err != nil {
		return
	}
	res.Usage = resp.Usage

	// Record assistant response with cost from the provider.
	msg := llm.Message{
		Role:      llm.RoleAssistant,
		Content:   resp.Content,
		Reasoning: resp.Reasoning,
		Calls:     resp.Calls,
	}
	e := session.NewEvent(s.ID(), session.MessageAdded, msg)
	e.Cost = resp.Usage.Cost
	if err = s.Append(ctx, e); err != nil {
		return
	}
	llm.RecordUsage(ctx, provider.ID(), req.Model, resp.Usage)

	// Execute tool calls in parallel and append results to the session.
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
		e.ID.String(),
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

		var steps int
		var escalations int
		var totalUsage llm.Usage
		var stopReason TurnStopReason
		defer func() {
			data := session.TurnCompletedData{
				AgentID:        a.ID(),
				Steps:          steps,
				Usage:          totalUsage,
				TurnStopReason: string(stopReason),
			}
			if runErr != nil {
				data.Error = runErr.Error()
			}
			_ = s.Append(ctx, session.NewTurnCompletedEvent(s.ID(), data))
		}()

		if err := s.Append(ctx, session.NewTurnStartedEvent(s.ID(), session.TurnStartedData{
			AgentID: a.ID(),
		})); err != nil {
			runErr = err
			yield(StepResult{}, err)
			return
		}

		if maxSteps <= 0 {
			stopReason = TurnStopMaxTurnsHit
			yield(StepResult{TurnStopReason: stopReason}, nil)
			return
		}

		for steps < maxSteps {
			if err := ctx.Err(); err != nil {
				runErr = err
				yield(StepResult{}, err)
				return
			}

			res, err := a.Step(ctx, s)
			if err != nil {
				var budgetErr *governor.BudgetExceededError
				if errors.As(err, &budgetErr) {
					stopReason = TurnStopBudgetExhausted
					yield(StepResult{TurnStopReason: stopReason, Usage: totalUsage}, nil)
					return
				}
				escalation := classifyStepError(err, provider)
				if escalation != nil && escalation.recoverable && escalations < maxEscalations {
					escalations++
					if appendErr := appendWithheldToolMessage(ctx, s, escalation); appendErr != nil {
						runErr = appendErr
						yield(StepResult{}, appendErr)
						return
					}
					if recordErr := recordEscalationRetry(ctx, s, a.ID(), escalations, escalation); recordErr != nil {
						runErr = recordErr
						yield(StepResult{}, recordErr)
						return
					}
					continue
				}
				if escalation != nil && escalation.recoverable {
					runErr = hardEscalationError(escalation, escalations)
					yield(StepResult{}, runErr)
					return
				}
				runErr = err
				yield(res, err)
				return
			}
			escalations = 0
			steps++
			totalUsage.InputTokens += res.Usage.InputTokens
			totalUsage.OutputTokens += res.Usage.OutputTokens
			totalUsage.TotalTokens += res.Usage.TotalTokens
			totalUsage.CacheReadTokens += res.Usage.CacheReadTokens
			totalUsage.CacheCreationTokens += res.Usage.CacheCreationTokens
			totalUsage.Cost += res.Usage.Cost
			stopReason = turnStopReasonForTurn(res, s, steps, maxSteps)
			res.TurnStopReason = stopReason
			if !yield(res, nil) {
				return
			}

			if stopReason != "" {
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

	// Populate Content from the last assistant message without tool calls.
	if steps > 0 {
		if msg, ok := s.LastAssistantMessage(); ok {
			res.Content = msg.Content
		}
	}

	return
}
