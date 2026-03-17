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
func RunStep(ctx context.Context, s *session.Session, cfg StepConfig) (StepResult, error) {
	if err := s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeStepStarted, map[string]any{
		"agent_id": cfg.ID,
		"model":    cfg.Model,
	})); err != nil {
		return StepResult{}, err
	}

	req := &llm.LLMRequest{
		Model: cfg.Model,
	}

	// Build context
	if err := cfg.Builder.Build(ctx, cfg.Provider, cfg.Model, s, req); err != nil {
		return StepResult{}, err
	}

	resp, err := cfg.Provider.Generate(ctx, req)
	if err != nil {
		return StepResult{}, err
	}

	// Record assistant response with cost from the provider.
	msg := llm.Message{
		Role:    llm.RoleAssistant,
		Content: resp.Content,
		Calls:   resp.Calls,
	}
	e := session.NewEvent(s.ID(), session.EventTypeMessageAdded, msg)
	e.Cost = resp.Usage.Cost
	if err := s.Append(ctx, e); err != nil {
		return StepResult{}, err
	}
	llm.RecordUsage(ctx, req.Model, resp.Usage)

	// Execute tool calls in parallel and append results to the session.
	handoffTargets := getHandoffTargets(cfg.Tools)
	res, err := RunTools(ctx, s, resp.Calls, cfg.Tools, cfg.Hooks, handoffTargets)
	if err != nil {
		return res, err
	}
	res.Usage = resp.Usage

	if err := s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeStepCompleted, map[string]any{
		"agent_id": cfg.ID,
		"usage":    resp.Usage,
	})); err != nil {
		return res, err
	}

	return res, nil
}

// Turn executes one or more steps until the agent finishes or a handoff is requested.
func (a *BaseAgent) Turn(ctx context.Context, s *session.Session) (StepResult, error) {
	return RunTurn(ctx, a, s, a.MaxSteps)
}

// RunTurn executes one or more steps until the agent finishes (no pending tool
// calls) or a handoff is requested, or maxSteps is reached.
// It can run any Agent implementation that satisfies the interface.
func RunTurn(ctx context.Context, a Agent, s *session.Session, maxSteps int) (StepResult, error) {
	if err := s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeTurnStarted, map[string]any{
		"agent_id": a.ID(),
	})); err != nil {
		return StepResult{}, err
	}

	steps := 0
	var result StepResult
	var totalUsage llm.Usage
	for steps < maxSteps {
		var err error
		result, err = a.Step(ctx, s)
		if err != nil {
			return StepResult{}, err
		}
		steps++
		totalUsage.InputTokens += result.Usage.InputTokens
		totalUsage.OutputTokens += result.Usage.OutputTokens
		totalUsage.TotalTokens += result.Usage.TotalTokens
		totalUsage.Cost += result.Usage.Cost

		// If a handoff was requested, stop immediately so the caller can route.
		if result.Handoff != nil {
			break
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

	if steps >= maxSteps {
		return StepResult{}, fmt.Errorf("%w (%d)", ErrMaxSteps, maxSteps)
	}
	result.Usage = totalUsage

	// Populate Content from the last assistant message without tool calls.
	msgs := s.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && len(msgs[i].Calls) == 0 {
			result.Content = msgs[i].Content
			break
		}
	}

	// We don't have direct access to BaseAgent.Hooks here, but custom agents
	// can handle their own EventStop if they want. BaseAgent does this in its
	// Turn implementation if needed, but for now we keep RunTurn purely interface-based.

	if err := s.Append(ctx, session.NewEvent(s.ID(), session.EventTypeTurnCompleted, map[string]any{
		"agent_id": a.ID(),
		"steps":    steps,
	})); err != nil {
		return result, err
	}

	return result, nil
}
