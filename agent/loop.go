package agent

import (
	"context"
	"fmt"

	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// StepConfig defines the components needed to execute a single agent step.
type StepConfig struct {
	ID       string
	Model    string
	Provider llm.Provider
	Builder  *ccontext.Builder
	Tools    *tool.Registry
	Hooks    *hook.Runner
}

// Step executes a single turn of the agentic loop and returns its result.
func (a *BaseAgent) Step(ctx context.Context, s *session.Session) (StepResult, error) {
	return RunStep(ctx, s, StepConfig{
		ID:       a.agentID,
		Model:    a.Model,
		Provider: a.Provider,
		Builder:  a.Builder,
		Tools:    a.Tools,
		Hooks:    a.Hooks,
	})
}

// RunStep executes a single step of the agentic loop using the provided config.
// It builds the context, generates a response, and executes any requested tools.
func RunStep(ctx context.Context, s *session.Session, cfg StepConfig) (res StepResult, err error) {
	if err := s.Append(ctx, session.NewEvent(s.ID(), session.StepStarted, map[string]any{
		"agent_id": cfg.ID,
		"model":    cfg.Model,
	})); err != nil {
		return StepResult{}, err
	}

	defer func() {
		data := map[string]any{
			"agent_id": cfg.ID,
			"usage":    res.Usage,
		}
		if err != nil {
			data["error"] = err.Error()
		}
		_ = s.Append(ctx, session.NewEvent(s.ID(), session.StepCompleted, data))
	}()

	req := &llm.Request{
		Model: cfg.Model,
	}

	// Build context
	if err = cfg.Builder.Build(ctx, cfg.Provider, cfg.Model, s, req); err != nil {
		return
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
	res, err = RunTools(ctx, s, resp.Calls, cfg.Tools, cfg.Hooks, handoffTargets)
	res.Usage = resp.Usage // Restore usage as RunTools only returns results/handoff

	return
}

// Turn executes one or more steps until the agent finishes or a handoff is requested.
func (a *BaseAgent) Turn(ctx context.Context, s *session.Session) (StepResult, error) {
	return RunTurn(ctx, a, s, a.MaxSteps)
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
	if err := s.Append(ctx, session.NewEvent(s.ID(), session.TurnStarted, map[string]any{
		"agent_id": a.ID(),
	})); err != nil {
		return StepResult{}, err
	}

	var steps int
	var totalUsage llm.Usage
	defer func() {
		data := map[string]any{
			"agent_id": a.ID(),
			"steps":    steps,
			"usage":    totalUsage,
		}
		if err != nil {
			data["error"] = err.Error()
		}
		_ = s.Append(ctx, session.NewEvent(s.ID(), session.TurnCompleted, data))
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

		// Continue only if the last message is a tool result (model must
		// process it). Any other role means the agent has finished.
		last, ok := s.LastMessage()
		if !ok || last.Role != llm.RoleTool {
			break
		}
	}

	if steps >= maxSteps {
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
