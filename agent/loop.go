package agent

import (
	"context"
	"fmt"

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

// RunTurn executes one or more steps until the agent finishes (no pending tool
// calls) or a handoff is requested, or maxSteps is reached.
// It can run any Agent implementation that satisfies the interface.
func RunTurn(
	ctx context.Context,
	a Agent,
	s *session.Session,
	maxSteps int,
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

	for steps < maxSteps {
		res, err = a.Step(ctx, s)
		if err != nil {
			return
		}
		steps++
		totalUsage.InputTokens += res.Usage.InputTokens
		totalUsage.OutputTokens += res.Usage.OutputTokens
		totalUsage.TotalTokens += res.Usage.TotalTokens
		totalUsage.Cost += res.Usage.Cost

		// If a handoff was requested, stop immediately so the caller can route.
		if res.Handoff != nil {
			break
		}

		// If the session is waiting for external input (e.g. approval), stop.
		if s.IsWaiting() {
			break
		}

		// Continue only if the last message is a tool result (model must
		// process it). Any other role means the agent has finished.
		last, ok := s.LastMessage()
		if !ok || last.Role != llm.RoleTool {
			break
		}
	}

	if steps >= maxSteps {
		res.Usage = totalUsage
		err = fmt.Errorf("%w (%d)", ErrMaxSteps, maxSteps)
		return
	}
	res.Usage = totalUsage

	// Populate Content from the last assistant message without tool calls.
	if msg, ok := s.LastAssistantMessage(); ok {
		res.Content = msg.Content
	}

	return
}
