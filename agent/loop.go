package agent

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Step executes a single turn of the agentic loop.
func (a *Agent) Step(ctx context.Context, s *session.Session) error {
	req := &llm.LLMRequest{
		Model: a.Model,
	}

	// Build context
	if err := a.Builder.Build(ctx, s, req); err != nil {
		return err
	}

	// Always ensure agent's primary instructions are included if not handled by builder
	// (Optional: move this to a dedicated processor in NewAgent)
	
	resp, err := a.Provider.Generate(ctx, req)
	if err != nil {
		return err
	}

	// Record assistant response
	msg := llm.Message{
		Role:    llm.RoleAssistant,
		Content: resp.Content,
		Calls:   resp.Calls,
	}
	s.Append(session.NewEvent(s.ID(), session.EventTypeMessageAdded, msg))

	// Execute tools and append results
	for _, call := range resp.Calls {
		var output string
		if a.Tools != nil {
			var err error
			output, err = a.Tools.Execute(ctx, call.Function.Name, call.Function.Arguments)
			if err != nil {
				output = fmt.Sprintf("Error: %s", err)
			}
		} else {
			output = fmt.Sprintf("Error: no tool registry configured; cannot execute %q", call.Function.Name)
		}

		toolMsg := llm.Message{
			Role:    llm.RoleTool,
			Content: output,
			ToolID:  call.ID,
			Name:    call.Function.Name,
		}
		s.Append(session.NewEvent(s.ID(), session.EventTypeMessageAdded, toolMsg))
	}

	return nil
}

// Turn executes one or more steps until the agent finishes its response
// or makes tool calls.
func (a *Agent) Turn(ctx context.Context, s *session.Session) error {
	steps := 0
	for steps < a.MaxSteps {
		err := a.Step(ctx, s)
		if err != nil {
			return err
		}
		steps++

		// Check if the last message in the session is a tool response.
		// If it is, we should call the model again to process the results.
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
		return fmt.Errorf("maximum tool calling steps reached (%d)", a.MaxSteps)
	}

	return nil
}
