package canto

import (
	"context"
	"fmt"
	"sync"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/oklog/ulid/v2"
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

// RunEventDurability identifies whether a streamed event is backed by the
// durable session log, live-only stream data, or terminal turn settlement.
type RunEventDurability string

const (
	RunEventDurable  RunEventDurability = "durable"
	RunEventLiveOnly RunEventDurability = "live_only"
	RunEventTerminal RunEventDurability = "terminal"
)

// RunEvent is one item in a host-facing streamed turn.
type RunEvent struct {
	Type       RunEventType
	SessionID  string
	TurnID     string
	Seq        int64
	Durability RunEventDurability
	Chunk      llm.Chunk
	Event      session.Event
	Result     agent.StepResult
	Err        error
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
	turnID := ulid.Make().String()
	go func() {
		defer close(out)
		defer sub.Close()

		var wg sync.WaitGroup
		done := make(chan struct{})
		var flushMu sync.Mutex
		var emitMu sync.Mutex
		var seq int64
		nextEvent := len(baseline)
		emit := func(event RunEvent, durability RunEventDurability, respectCancel bool) bool {
			emitMu.Lock()
			defer emitMu.Unlock()

			seq++
			event.SessionID = s.id
			event.TurnID = turnID
			event.Seq = seq
			event.Durability = durability

			if !respectCancel {
				out <- event
				return true
			}
			select {
			case out <- event:
				return true
			case <-ctx.Done():
				seq--
				return false
			}
		}
		emitLive := func(event RunEvent) bool {
			return emit(event, RunEventLiveOnly, true)
		}
		emitFinal := func(event RunEvent) bool {
			return emit(event, RunEventTerminal, false)
		}
		emitLiveSession := func(event session.Event, stop <-chan struct{}) bool {
			emitMu.Lock()
			defer emitMu.Unlock()

			seq++
			streamEvent := RunEvent{
				Type:       RunEventSession,
				SessionID:  s.id,
				TurnID:     turnID,
				Seq:        seq,
				Durability: RunEventDurable,
				Event:      event,
			}
			select {
			case out <- streamEvent:
				return true
			case <-ctx.Done():
				seq--
				return false
			case <-stop:
				seq--
				return false
			}
		}
		emitFinalSession := func(event session.Event, _ <-chan struct{}) bool {
			return emit(
				RunEvent{Type: RunEventSession, Event: event},
				RunEventDurable,
				false,
			)
		}
		flushEvents := func(
			stop <-chan struct{},
			emitSession func(session.Event, <-chan struct{}) bool,
		) error {
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
					if err := flushEvents(done, emitLiveSession); err != nil {
						return
					}
				case <-done:
					return
				}
			}
		})

		result, err := s.harness.Runner.SendStream(ctx, s.id, message, func(chunk *llm.Chunk) {
			if chunk == nil {
				return
			}
			if err := flushEvents(done, emitLiveSession); err != nil {
				return
			}
			emitLive(RunEvent{Type: RunEventChunk, Chunk: *chunk})
		})
		close(done)
		sub.Close()
		wg.Wait()

		if snapshotErr := flushEvents(nil, emitFinalSession); snapshotErr != nil && err == nil {
			emitFinal(RunEvent{Type: RunEventError, Err: snapshotErr})
			return
		}

		if err != nil {
			emitFinal(RunEvent{Type: RunEventError, Err: err})
			return
		}
		emitFinal(RunEvent{Type: RunEventResult, Result: result})
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
