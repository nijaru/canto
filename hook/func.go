package hook

import (
	"context"
	"fmt"
)

// funcHandler runs an in-process Go function, avoiding subprocess overhead.
type funcHandler struct {
	name   string
	events []Event
	fn     func(ctx context.Context, payload *Payload) *Result
}

// FromFunc creates an in-process hook from a function.
func FromFunc(
	name string,
	events []Event,
	fn func(ctx context.Context, payload *Payload) *Result,
) *funcHandler {
	return &funcHandler{name: name, events: events, fn: fn}
}

func (h *funcHandler) Name() string { return h.name }

func (h *funcHandler) Events() []Event { return h.events }

func (h *funcHandler) Execute(ctx context.Context, payload *Payload) *Result {
	if h.fn == nil {
		return &Result{
			Action: ActionBlock,
			Error:  fmt.Errorf("hook %s has no function", h.name),
		}
	}
	return h.fn(ctx, payload)
}
