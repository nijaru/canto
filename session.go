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
	state   *harnessSessionState
}

// Turn is one accepted host-facing execution transaction.
type Turn struct {
	id         string
	events     <-chan RunEvent
	cancel     context.CancelFunc
	resultCh   <-chan turnOutcome
	resultOnce sync.Once
	result     turnOutcome
	resultOK   bool
}

type turnOutcome struct {
	result agent.StepResult
	err    error
}

// ID returns the durable turn identity.
func (t *Turn) ID() string {
	if t == nil {
		return ""
	}
	return t.id
}

// Events returns the single ordered event stream for this turn.
func (t *Turn) Events() <-chan RunEvent {
	if t == nil {
		return nil
	}
	return t.events
}

// Cancel requests cancellation of the turn.
func (t *Turn) Cancel(ctx context.Context) error {
	if t == nil {
		return fmt.Errorf("canto turn: nil turn")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	t.cancel()
	return nil
}

// Result waits for terminal turn settlement and returns the final result.
func (t *Turn) Result() (agent.StepResult, error) {
	if t == nil {
		return agent.StepResult{}, fmt.Errorf("canto turn: nil turn")
	}
	t.resultOnce.Do(func() {
		t.result, t.resultOK = <-t.resultCh
	})
	if !t.resultOK {
		return agent.StepResult{}, fmt.Errorf("canto turn %s: missing result", t.id)
	}
	return t.result.result, t.result.err
}

// ID returns the durable session ID.
func (s *Session) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

// Prompt appends a user message and executes one agent turn.
//
// It is a blocking convenience wrapper around Submit. Hosts that need
// streaming events, cancellation, or stable turn identity should call Submit.
func (s *Session) Prompt(ctx context.Context, message string) (agent.StepResult, error) {
	turn, err := s.Submit(ctx, llm.TextPrompt(message))
	if err != nil {
		return agent.StepResult{}, err
	}
	for range turn.Events() {
	}
	return turn.Result()
}

// RunEventKind identifies the concrete payload carried by a streamed harness
// event. It is derived from RunEvent.Payload instead of stored as a second
// semantic channel.
type RunEventKind string

const (
	RunEventChunk   RunEventKind = "chunk"
	RunEventSession RunEventKind = "session"
	RunEventRetry   RunEventKind = "retry"
	RunEventResult  RunEventKind = "result"
	RunEventError   RunEventKind = "error"
)

// RunEventDurability identifies whether a streamed event is backed by the
// durable session log, live-only stream data, or terminal turn settlement.
type RunEventDurability string

const (
	RunEventDurable  RunEventDurability = "durable"
	RunEventLiveOnly RunEventDurability = "live_only"
	RunEventTerminal RunEventDurability = "terminal"
)

// RunEventPayload is the typed content of one host-facing streamed event.
// Hosts should switch on this payload, then use the envelope metadata for
// ordering, durability, usage, and lifecycle projection.
type RunEventPayload interface {
	runEventPayload()
	Kind() RunEventKind
}

// RunChunkPayload carries live provider stream output.
type RunChunkPayload struct {
	Chunk llm.Chunk
}

func (RunChunkPayload) runEventPayload() {}
func (RunChunkPayload) Kind() RunEventKind {
	return RunEventChunk
}

// RunSessionPayload carries a durable session-log event.
type RunSessionPayload struct {
	Event session.Event
}

func (RunSessionPayload) runEventPayload() {}
func (RunSessionPayload) Kind() RunEventKind {
	return RunEventSession
}

// RunRetryPayload carries live provider retry metadata.
type RunRetryPayload struct {
	Retry llm.RetryEvent
}

func (RunRetryPayload) runEventPayload() {}
func (RunRetryPayload) Kind() RunEventKind {
	return RunEventRetry
}

// RunResultPayload carries terminal successful turn settlement.
type RunResultPayload struct {
	Result agent.StepResult
}

func (RunResultPayload) runEventPayload() {}
func (RunResultPayload) Kind() RunEventKind {
	return RunEventResult
}

// RunErrorPayload carries terminal failed turn settlement.
type RunErrorPayload struct {
	Err error
}

func (RunErrorPayload) runEventPayload() {}
func (RunErrorPayload) Kind() RunEventKind {
	return RunEventError
}

// RunEvent is one item in a host-facing streamed turn.
type RunEvent struct {
	SessionID  string
	TurnID     string
	Seq        int64
	Durability RunEventDurability
	Usage      *RunUsage
	Lifecycle  *RunLifecycle
	Payload    RunEventPayload
}

// Kind returns the concrete payload kind.
func (e RunEvent) Kind() RunEventKind {
	if e.Payload == nil {
		return ""
	}
	return e.Payload.Kind()
}

// Chunk returns the provider chunk payload when this is a chunk event.
func (e RunEvent) Chunk() (llm.Chunk, bool) {
	switch payload := e.Payload.(type) {
	case RunChunkPayload:
		return payload.Chunk, true
	case *RunChunkPayload:
		if payload != nil {
			return payload.Chunk, true
		}
	default:
		return llm.Chunk{}, false
	}
	return llm.Chunk{}, false
}

// SessionEvent returns the durable session event payload when present.
func (e RunEvent) SessionEvent() (session.Event, bool) {
	switch payload := e.Payload.(type) {
	case RunSessionPayload:
		return payload.Event, true
	case *RunSessionPayload:
		if payload != nil {
			return payload.Event, true
		}
	default:
		return session.Event{}, false
	}
	return session.Event{}, false
}

// Retry returns the retry payload when this is a retry event.
func (e RunEvent) Retry() (llm.RetryEvent, bool) {
	switch payload := e.Payload.(type) {
	case RunRetryPayload:
		return payload.Retry, true
	case *RunRetryPayload:
		if payload != nil {
			return payload.Retry, true
		}
	default:
		return llm.RetryEvent{}, false
	}
	return llm.RetryEvent{}, false
}

// Result returns the terminal result payload when this is a result event.
func (e RunEvent) Result() (agent.StepResult, bool) {
	switch payload := e.Payload.(type) {
	case RunResultPayload:
		return payload.Result, true
	case *RunResultPayload:
		if payload != nil {
			return payload.Result, true
		}
	default:
		return agent.StepResult{}, false
	}
	return agent.StepResult{}, false
}

// Err returns the terminal error payload when this is an error event.
func (e RunEvent) Err() (error, bool) {
	switch payload := e.Payload.(type) {
	case RunErrorPayload:
		return payload.Err, true
	case *RunErrorPayload:
		if payload != nil {
			return payload.Err, true
		}
	default:
		return nil, false
	}
	return nil, false
}

type runEventEmitter struct {
	ctx       context.Context
	out       chan<- RunEvent
	sessionID string
	turnID    string
	lifecycle runLifecycleState
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
		RunEvent{Payload: RunSessionPayload{Event: event}},
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
	e.lifecycle.annotate(&event)

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

// Submit accepts typed prompt input as one turn transaction and starts execution.
func (s *Session) Submit(ctx context.Context, prompt Prompt) (*Turn, error) {
	if s == nil || s.harness == nil || s.harness.Runner == nil {
		return nil, fmt.Errorf("canto harness: nil runner")
	}
	if s.state == nil {
		return nil, fmt.Errorf("canto harness: nil session state")
	}

	ctx, cancel := context.WithCancel(ctx)
	out := make(chan RunEvent)
	ctx, turnID := session.EnsureTurnID(ctx)
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
		cancel()
		return nil, err
	}
	if err := s.state.beginTurn(turnID, cancel); err != nil {
		cancel()
		detach()
		return nil, err
	}

	resultCh := make(chan turnOutcome, 1)
	go func() {
		defer close(out)
		defer detach()
		finish := func() {
			s.state.finishTurn(turnID, false)
		}

		runCtx := llm.WithRetryObserver(ctx, func(event llm.RetryEvent) {
			emitter.emitLive(RunEvent{Payload: RunRetryPayload{Retry: event}})
		})
		runPrompt := s.state.consumeNextTurn(prompt)
		result, err := s.harness.Runner.SendStream(runCtx, s.id, runPrompt, func(chunk *llm.Chunk) {
			if chunk == nil {
				return
			}
			emitter.emitLive(RunEvent{Payload: RunChunkPayload{Chunk: *chunk}})
		})
		if err != nil {
			emitter.emitFinal(RunEvent{Payload: RunErrorPayload{Err: err}})
			finish()
			resultCh <- turnOutcome{err: err}
			return
		}
		emitter.emitFinal(RunEvent{Payload: RunResultPayload{Result: result}})
		finish()
		resultCh <- turnOutcome{result: result}
	}()
	return &Turn{
		id:       turnID,
		events:   out,
		cancel:   cancel,
		resultCh: resultCh,
	}, nil
}

// PromptStream appends a user message and emits one ordered stream containing
// model chunks, live durable session events, and the terminal result/error.
//
// It is a convenience wrapper around Submit for hosts that do not need direct
// access to the Turn handle. New live hosts should prefer Submit.
func (s *Session) PromptStream(
	ctx context.Context,
	message string,
) (<-chan RunEvent, error) {
	turn, err := s.Submit(ctx, llm.TextPrompt(message))
	if err != nil {
		return nil, err
	}
	return turn.Events(), nil
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
