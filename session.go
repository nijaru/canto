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

type runEventEmitter struct {
	ctx       context.Context
	out       chan<- RunEvent
	sessionID string
	turnID    string
	mu        sync.Mutex
	seq       int64
}

func newRunEventEmitter(
	ctx context.Context,
	out chan<- RunEvent,
	sessionID string,
	turnID string,
) *runEventEmitter {
	return &runEventEmitter{
		ctx:       ctx,
		out:       out,
		sessionID: sessionID,
		turnID:    turnID,
	}
}

func (e *runEventEmitter) emitLive(event RunEvent) bool {
	return e.emit(e.ctx, event, RunEventLiveOnly, true)
}

func (e *runEventEmitter) emitSession(ctx context.Context, event session.Event) bool {
	return e.emit(
		ctx,
		RunEvent{Type: RunEventSession, Event: event},
		RunEventDurable,
		true,
	)
}

func (e *runEventEmitter) emitFinal(event RunEvent) bool {
	return e.emit(context.Background(), event, RunEventTerminal, false)
}

func (e *runEventEmitter) emit(
	ctx context.Context,
	event RunEvent,
	durability RunEventDurability,
	respectCancel bool,
) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.seq++
	event.SessionID = e.sessionID
	event.TurnID = e.turnID
	event.Seq = e.seq
	event.Durability = durability

	if !respectCancel {
		e.out <- event
		return true
	}
	if ctx == nil || ctx.Done() == nil {
		e.out <- event
		return true
	}
	select {
	case e.out <- event:
		return true
	case <-ctx.Done():
		e.seq--
		return false
	}
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

	out := make(chan RunEvent)
	turnID := ulid.Make().String()
	emitter := newRunEventEmitter(ctx, out, s.id, turnID)
	detach, err := s.harness.Runner.ObserveEvents(
		ctx,
		s.id,
		func(eventCtx context.Context, event session.Event) error {
			if emitter.emitSession(eventCtx, event) {
				return nil
			}
			if err := eventCtx.Err(); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return context.Canceled
		},
	)
	if err != nil {
		return nil, err
	}

	go func() {
		defer close(out)
		defer detach()

		result, err := s.harness.Runner.SendStream(ctx, s.id, message, func(chunk *llm.Chunk) {
			if chunk == nil {
				return
			}
			emitter.emitLive(RunEvent{Type: RunEventChunk, Chunk: *chunk})
		})
		if err != nil {
			emitter.emitFinal(RunEvent{Type: RunEventError, Err: err})
			return
		}
		emitter.emitFinal(RunEvent{Type: RunEventResult, Result: result})
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
