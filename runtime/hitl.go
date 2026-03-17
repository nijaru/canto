package runtime

import (
	"context"
	"github.com/go-json-experiment/json"
	"fmt"

	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// InputGate coordinates between an agent waiting for human input and the
// human providing it. Single-flight: one pending request at a time.
//
// Typical usage:
//
//	gate := runtime.NewInputGate()
//	reg.Register(gate.Tool(sess))
//	// in the UI goroutine:
//	gate.Provide("yes, proceed")
type InputGate struct {
	pending chan struct{} // slot; limits to one active Request
	ch      chan string
}

// NewInputGate creates an InputGate ready to use.
func NewInputGate() *InputGate {
	return &InputGate{
		pending: make(chan struct{}, 1),
		ch:      make(chan string, 1),
	}
}

// Request blocks until Provide is called with the human's response, or ctx
// is cancelled. It records the exchange as EventTypeExternalInput events.
//
// Only one Request may be active at a time; a second concurrent Request
// blocks until the first is resolved.
func (g *InputGate) Request(
	ctx context.Context,
	sess *session.Session,
	prompt string,
) (string, error) {
	select {
	case g.pending <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-g.pending }()

	if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.EventTypeExternalInput, map[string]any{
		"prompt": prompt,
		"status": "pending",
	})); err != nil {
		return "", err
	}

	select {
	case input := <-g.ch:
		if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.EventTypeExternalInput, map[string]any{
			"prompt": prompt,
			"input":  input,
			"status": "received",
		})); err != nil {
			return "", err
		}
		return input, nil
	case <-ctx.Done():
		// Drain g.ch so a value from a concurrent Provide call doesn't
		// sit in the buffer and corrupt the next Request.
		select {
		case <-g.ch:
		default:
		}
		return "", ctx.Err()
	}
}

// Provide delivers human input to an active Request call.
// Blocks until a Request is waiting or ctx is cancelled.
// Returns false if ctx was cancelled before the input was delivered.
func (g *InputGate) Provide(ctx context.Context, input string) bool {
	select {
	case g.ch <- input:
		return true
	case <-ctx.Done():
		return false
	}
}

// Tool returns a tool.Tool that the agent can call to request human input.
// The tool blocks the agent step until Provide is called.
func (g *InputGate) Tool(sess *session.Session) tool.Tool {
	return tool.Func(
		"request_human_input",
		"Ask the human operator a question and wait for their response before proceeding.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "The question or prompt to present to the human.",
				},
			},
			"required": []string{"prompt"},
		},
		func(ctx context.Context, args string) (string, error) {
			var input struct {
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				return "", fmt.Errorf("request_human_input: invalid args: %w", err)
			}
			return g.Request(ctx, sess, input.Prompt)
		},
	)
}
