package agent

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Step executes a single turn of the agentic loop and returns its result.
// If any tool call produces a Handoff payload targeting a known peer agent,
// the result's Handoff field is set so callers can route accordingly.
func (a *BaseAgent) Step(ctx context.Context, s *session.Session) (StepResult, error) {
	s.Append(session.NewEvent(s.ID(), session.EventTypeStepStarted, map[string]any{
		"agent_id": a.agentID,
		"model":    a.Model,
	}))

	req := &llm.LLMRequest{
		Model: a.Model,
	}

	// Build context
	if err := a.Builder.Build(ctx, a.Provider, a.Model, s, req); err != nil {
		return StepResult{}, err
	}

	resp, err := a.Provider.Generate(ctx, req)
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
	s.Append(e)
	llm.RecordUsage(ctx, req.Model, resp.Usage)

	// Execute tool calls in parallel and append results to the session.
	res, err := a.runTools(ctx, s, resp.Calls)
	if err != nil {
		return res, err
	}

	s.Append(session.NewEvent(s.ID(), session.EventTypeStepCompleted, map[string]any{
		"agent_id": a.agentID,
		"usage":    resp.Usage,
	}))

	return res, nil
}

// Turn executes one or more steps until the agent finishes (no pending tool
// calls) or a handoff is requested, or MaxSteps is reached.
// The returned StepResult reflects the final step's outcome.
func (a *BaseAgent) Turn(ctx context.Context, s *session.Session) (StepResult, error) {
	s.Append(session.NewEvent(s.ID(), session.EventTypeTurnStarted, map[string]any{
		"agent_id": a.agentID,
	}))

	steps := 0
	var result StepResult
	for steps < a.MaxSteps {
		var err error
		result, err = a.Step(ctx, s)
		if err != nil {
			return StepResult{}, err
		}
		steps++

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

	if steps >= a.MaxSteps {
		return StepResult{}, fmt.Errorf("%w (%d)", ErrMaxSteps, a.MaxSteps)
	}

	// Populate Content from the last assistant message without tool calls.
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

	s.Append(session.NewEvent(s.ID(), session.EventTypeTurnCompleted, map[string]any{
		"agent_id": a.agentID,
		"steps":    steps,
	}))

	return result, nil
}
