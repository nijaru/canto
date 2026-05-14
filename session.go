package canto

import (
	"context"
	"fmt"
	"sync"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Session is a host-facing handle for one durable conversation in a Harness.
type Session struct {
	harness *Harness
	id      string
}

// ID returns the durable session ID.
func (s *Session) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

// Prompt appends a user message and executes one agent turn.
func (s *Session) Prompt(ctx context.Context, message string) (agent.StepResult, error) {
	if s == nil || s.harness == nil || s.harness.Runner == nil {
		return agent.StepResult{}, fmt.Errorf("canto harness: nil runner")
	}
	return s.harness.Runner.Send(ctx, s.id, message)
}

// RunEventType identifies the source and meaning of a streamed harness event.
type RunEventType string

const (
	RunEventChunk   RunEventType = "chunk"
	RunEventSession RunEventType = "session"
	RunEventResult  RunEventType = "result"
	RunEventError   RunEventType = "error"
)

// RunEvent is one item in a host-facing streamed turn.
type RunEvent struct {
	Type   RunEventType
	Chunk  llm.Chunk
	Event  session.Event
	Result agent.StepResult
	Err    error
}

// PromptStream appends a user message and emits one ordered stream containing
// model chunks, live durable session events, and the terminal result/error.
func (s *Session) PromptStream(
	ctx context.Context,
	message string,
) (<-chan RunEvent, error) {
	if s == nil || s.harness == nil || s.harness.Runner == nil {
		return nil, fmt.Errorf("canto harness: nil runner")
	}

	baseline, err := s.harness.Runner.Events(ctx, s.id)
	if err != nil {
		return nil, err
	}

	sub, err := s.Events(ctx)
	if err != nil {
		return nil, err
	}

	out := make(chan RunEvent)
	go func() {
		defer close(out)
		defer sub.Close()

		var wg sync.WaitGroup
		done := make(chan struct{})
		var flushMu sync.Mutex
		nextEvent := len(baseline)
		emit := func(event RunEvent) bool {
			select {
			case out <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		emitSession := func(event session.Event, stop <-chan struct{}) bool {
			select {
			case out <- RunEvent{Type: RunEventSession, Event: event}:
				return true
			case <-ctx.Done():
				return false
			case <-stop:
				return false
			}
		}
		flushEvents := func(stop <-chan struct{}) error {
			flushMu.Lock()
			defer flushMu.Unlock()

			events, err := s.harness.Runner.Events(context.WithoutCancel(ctx), s.id)
			if err != nil {
				return err
			}
			for nextEvent < len(events) {
				if !emitSession(events[nextEvent], stop) {
					return context.Cause(ctx)
				}
				nextEvent++
			}
			return nil
		}

		wg.Go(func() {
			for {
				select {
				case _, ok := <-sub.Events():
					if !ok {
						return
					}
					if err := flushEvents(done); err != nil {
						return
					}
				case <-done:
					return
				}
			}
		})

		result, err := s.harness.Runner.SendStream(ctx, s.id, message, func(chunk *llm.Chunk) {
			if chunk != nil {
				emit(RunEvent{Type: RunEventChunk, Chunk: *chunk})
			}
		})
		close(done)
		sub.Close()
		wg.Wait()

		if snapshotErr := flushEvents(nil); snapshotErr != nil && err == nil {
			emit(RunEvent{Type: RunEventError, Err: snapshotErr})
			return
		}

		if err != nil {
			emit(RunEvent{Type: RunEventError, Err: err})
			return
		}
		emit(RunEvent{Type: RunEventResult, Result: result})
	}()
	return out, nil
}

// Run executes the agent on the existing session without appending a message.
func (s *Session) Run(ctx context.Context) (agent.StepResult, error) {
	if s == nil || s.harness == nil || s.harness.Runner == nil {
		return agent.StepResult{}, fmt.Errorf("canto harness: nil runner")
	}
	return s.harness.Runner.Run(ctx, s.id)
}

// Events subscribes to live durable session events.
func (s *Session) Events(ctx context.Context) (*session.Subscription, error) {
	if s == nil || s.harness == nil || s.harness.Runner == nil {
		return nil, fmt.Errorf("canto harness: nil runner")
	}
	return s.harness.Runner.Watch(ctx, s.id)
}
