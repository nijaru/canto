package agent

import (
	"context"
	"iter"

	"github.com/nijaru/canto/approval"
	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
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

	// Build context
	if err = cfg.Builder.Build(ctx, cfg.Provider, cfg.Model, s, req); err != nil {
		return
	}

	cacheFingerprint, err := ccontext.FingerprintPromptCache(s, req)
	if err != nil {
		return StepResult{}, err
	}
	if err := s.Append(ctx, session.NewStepStartedEvent(s.ID(), session.StepStartedData{
		AgentID: cfg.ID,
		Model:   cfg.Model,
		PromptCache: session.PromptCacheData{
			PrefixHash:     cacheFingerprint.PrefixHash,
			ToolSchemaHash: cacheFingerprint.ToolSchemaHash,
		},
	})); err != nil {
		return StepResult{}, err
	}

	resp, err := cfg.Provider.Generate(ctx, req)
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
	llm.RecordUsage(ctx, req.Model, resp.Usage)

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
	)
	res.Usage = resp.Usage // Restore usage as RunTools only returns results/handoff

	return
}

// Turn executes one or more steps until the agent finishes or a handoff is requested.
func (a *BaseAgent) Turn(ctx context.Context, s *session.Session) (StepResult, error) {
	return RunTurn(ctx, a, s, a.maxSteps)
}

// Run executes the agent loop as an iterator over per-step results.
// Callers can stop early by breaking the range loop; the session turn start/end
// events are still managed internally.
func (a *BaseAgent) Run(ctx context.Context, s *session.Session) iter.Seq2[StepResult, error] {
	return Run(ctx, a, s, a.maxSteps)
}

// Run executes the agent loop as an iterator over per-step results.
// It is the low-level generator form of RunTurn.
func Run(
	ctx context.Context,
	a Agent,
	s *session.Session,
	maxSteps int,
) iter.Seq2[StepResult, error] {
	return func(yield func(StepResult, error) bool) {
		if err := s.Append(ctx, session.NewTurnStartedEvent(s.ID(), session.TurnStartedData{
			AgentID: a.ID(),
		})); err != nil {
			yield(StepResult{}, err)
			return
		}

		var steps int
		var totalUsage llm.Usage
		var stopReason TurnStopReason
		var runErr error
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
				runErr = err
				yield(res, err)
				return
			}
			steps++
			totalUsage.InputTokens += res.Usage.InputTokens
			totalUsage.OutputTokens += res.Usage.OutputTokens
			totalUsage.TotalTokens += res.Usage.TotalTokens
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
) (res StepResult, err error) {
	var steps int
	var totalUsage llm.Usage
	for stepRes, stepErr := range Run(ctx, a, s, maxSteps) {
		if stepErr != nil {
			res = stepRes
			res.Usage = totalUsage
			err = stepErr
			return
		}
		res = stepRes
		steps++
		totalUsage.InputTokens += stepRes.Usage.InputTokens
		totalUsage.OutputTokens += stepRes.Usage.OutputTokens
		totalUsage.TotalTokens += stepRes.Usage.TotalTokens
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
