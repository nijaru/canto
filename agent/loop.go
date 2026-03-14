package agent

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Step executes a single turn of the agentic loop.
func (a *Agent) Step(ctx context.Context, s *session.Session) error {
	messages := s.Messages()

	// Prepend instructions if not present
	// For simplicity, always prepend instructions as a system message
	// A more sophisticated context processor would handle this in Layer 3
	fullMessages := append([]llm.Message{
		{Role: llm.RoleSystem, Content: a.Instructions},
	}, messages...)

	req := &llm.LLMRequest{
		Model:    a.Model,
		Messages: fullMessages,
	}
	if a.Tools != nil {
		req.Tools = a.Tools.Specs()
	}

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
		output, err := a.Tools.Execute(ctx, call.Function.Name, call.Function.Arguments)
		if err != nil {
			output = fmt.Sprintf("Error: %s", err)
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
	for {
		err := a.Step(ctx, s)
		if err != nil {
			return err
		}

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
	return nil
}
