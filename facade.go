package canto

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/nijaru/canto/llm"
)

// ErrSessionBusy reports that the session-scoped harness facade already owns an
// active phase. Use Steer, FollowUp, or NextTurn instead of Prompt/Submit while
// this error is returned.
var ErrSessionBusy = errors.New("canto session: busy")

// HarnessPhase is the session-scoped runtime phase exposed by the root facade.
type HarnessPhase string

const (
	HarnessPhaseIdle          HarnessPhase = "idle"
	HarnessPhaseTurn          HarnessPhase = "turn"
	HarnessPhaseCompaction    HarnessPhase = "compaction"
	HarnessPhaseBranchSummary HarnessPhase = "branch_summary"
	HarnessPhaseRetry         HarnessPhase = "retry"
)

// HarnessEventKind identifies session-scoped facade events that are not
// necessarily durable session-log entries.
type HarnessEventKind string

const (
	HarnessEventQueueUpdated HarnessEventKind = "queue_update"
	HarnessEventSavePoint    HarnessEventKind = "save_point"
	HarnessEventSettled      HarnessEventKind = "settled"
	HarnessEventAbort        HarnessEventKind = "abort"
)

// HarnessEventPayload is the typed payload carried by a HarnessEvent.
type HarnessEventPayload interface {
	harnessEventPayload()
	Kind() HarnessEventKind
}

// HarnessEvent is the Pi-like session facade event stream for queues,
// save-points, settled state, and abort settlement.
type HarnessEvent struct {
	SessionID string
	TurnID    string
	Seq       int64
	Payload   HarnessEventPayload
}

// Kind returns the event payload kind.
func (e HarnessEvent) Kind() HarnessEventKind {
	if e.Payload == nil {
		return ""
	}
	return e.Payload.Kind()
}

// QueueSnapshot is an immutable snapshot of pending facade queues.
type QueueSnapshot struct {
	Steer    []Prompt
	FollowUp []Prompt
	NextTurn []Prompt
}

// QueueUpdatedPayload reports a changed steer/follow-up/next-turn queue.
type QueueUpdatedPayload struct {
	Queue QueueSnapshot
}

func (QueueUpdatedPayload) harnessEventPayload() {}
func (QueueUpdatedPayload) Kind() HarnessEventKind {
	return HarnessEventQueueUpdated
}

// SavePointPayload reports that a turn boundary has reached a host-visible
// save point after pending facade writes have been flushed.
type SavePointPayload struct {
	HadPendingMutations bool
}

func (SavePointPayload) harnessEventPayload() {}
func (SavePointPayload) Kind() HarnessEventKind {
	return HarnessEventSavePoint
}

// SettledPayload reports that the session facade has returned to idle after a
// turn and the next-turn queue is visible for the following prompt.
type SettledPayload struct {
	NextTurnCount int
}

func (SettledPayload) harnessEventPayload() {}
func (SettledPayload) Kind() HarnessEventKind {
	return HarnessEventSettled
}

// AbortPayload reports an explicit facade abort and the queues it cleared.
type AbortPayload struct {
	ClearedSteer    []Prompt
	ClearedFollowUp []Prompt
}

func (AbortPayload) harnessEventPayload() {}
func (AbortPayload) Kind() HarnessEventKind {
	return HarnessEventAbort
}

type harnessSessionState struct {
	sessionID string

	mu             sync.Mutex
	phase          HarnessPhase
	activeTurnID   string
	activeCancel   context.CancelFunc
	activeDone     chan struct{}
	eventSeq       int64
	subscribers    map[int]chan HarnessEvent
	nextSubscriber int
	steerQueue     []Prompt
	followUpQueue  []Prompt
	nextTurnQueue  []Prompt
}

func newHarnessSessionState(sessionID string) *harnessSessionState {
	return &harnessSessionState{
		sessionID: sessionID,
		phase:     HarnessPhaseIdle,
	}
}

func (s *Session) Phase() HarnessPhase {
	if s == nil || s.state == nil {
		return HarnessPhaseIdle
	}
	return s.state.currentPhase()
}

// RuntimeEvents subscribes to session-scoped facade events. The returned
// channel is closed when ctx is canceled.
func (s *Session) RuntimeEvents(ctx context.Context) (<-chan HarnessEvent, error) {
	if s == nil || s.state == nil {
		return nil, fmt.Errorf("canto harness: nil session state")
	}
	return s.state.subscribe(ctx), nil
}

// WaitForIdle blocks until the session-scoped facade has no active phase.
func (s *Session) WaitForIdle(ctx context.Context) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("canto harness: nil session state")
	}
	return s.state.waitForIdle(ctx)
}

// Steer queues prompt input for in-flight steering. A later facade slice will
// define the exact provider-boundary drain point; this method establishes the
// session-owned queue and event contract.
func (s *Session) Steer(ctx context.Context, prompt Prompt) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("canto harness: nil session state")
	}
	return s.state.enqueue(ctx, queueKindSteer, prompt)
}

// SteerText queues a text steering prompt.
func (s *Session) SteerText(ctx context.Context, text string) error {
	return s.Steer(ctx, TextPrompt(text))
}

// FollowUp queues prompt input for when the active agent turn would otherwise
// stop. A later facade slice will wire this into the agent loop drain point.
func (s *Session) FollowUp(ctx context.Context, prompt Prompt) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("canto harness: nil session state")
	}
	return s.state.enqueue(ctx, queueKindFollowUp, prompt)
}

// FollowUpText queues a text follow-up prompt.
func (s *Session) FollowUpText(ctx context.Context, text string) error {
	return s.FollowUp(ctx, TextPrompt(text))
}

// NextTurn prepends prompt input to the next accepted prompt.
func (s *Session) NextTurn(ctx context.Context, prompt Prompt) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("canto harness: nil session state")
	}
	return s.state.enqueue(ctx, queueKindNextTurn, prompt)
}

// NextTurnText queues a text prompt for the next accepted prompt.
func (s *Session) NextTurnText(ctx context.Context, text string) error {
	return s.NextTurn(ctx, TextPrompt(text))
}

// Abort cancels the active turn, clears steering/follow-up queues, and waits
// until the session facade reaches idle.
func (s *Session) Abort(ctx context.Context) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("canto harness: nil session state")
	}
	return s.state.abort(ctx)
}

func (s *harnessSessionState) currentPhase() HarnessPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

func (s *harnessSessionState) beginTurn(
	turnID string,
	cancel context.CancelFunc,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != HarnessPhaseIdle {
		return ErrSessionBusy
	}
	s.phase = HarnessPhaseTurn
	s.activeTurnID = turnID
	s.activeCancel = cancel
	s.activeDone = make(chan struct{})
	return nil
}

func (s *harnessSessionState) finishTurn(
	turnID string,
	hadPendingMutations bool,
) {
	var done chan struct{}
	s.mu.Lock()
	if s.activeTurnID != turnID {
		s.mu.Unlock()
		return
	}
	s.phase = HarnessPhaseIdle
	s.activeTurnID = ""
	s.activeCancel = nil
	done = s.activeDone
	s.activeDone = nil
	savePoint := s.newEventLocked(
		turnID,
		SavePointPayload{HadPendingMutations: hadPendingMutations},
	)
	settled := s.newEventLocked(
		turnID,
		SettledPayload{NextTurnCount: len(s.nextTurnQueue)},
	)
	s.publishLocked(savePoint)
	s.publishLocked(settled)
	s.mu.Unlock()

	if done != nil {
		close(done)
	}
}

func (s *harnessSessionState) waitForIdle(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		s.mu.Lock()
		done := s.activeDone
		if s.phase == HarnessPhaseIdle || done == nil {
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()

		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type queueKind int

const (
	queueKindSteer queueKind = iota
	queueKindFollowUp
	queueKindNextTurn
)

func (s *harnessSessionState) enqueue(
	ctx context.Context,
	kind queueKind,
	prompt Prompt,
) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	prompt = prompt.Clone()
	if len(prompt.Messages) == 0 {
		return fmt.Errorf("canto session queue: prompt must contain at least one message")
	}

	var event HarnessEvent
	s.mu.Lock()
	switch kind {
	case queueKindSteer:
		if s.phase == HarnessPhaseIdle {
			s.mu.Unlock()
			return fmt.Errorf("%w: cannot steer while idle", ErrSessionBusy)
		}
		s.steerQueue = append(s.steerQueue, prompt)
	case queueKindFollowUp:
		if s.phase == HarnessPhaseIdle {
			s.mu.Unlock()
			return fmt.Errorf("%w: cannot follow up while idle", ErrSessionBusy)
		}
		s.followUpQueue = append(s.followUpQueue, prompt)
	case queueKindNextTurn:
		s.nextTurnQueue = append(s.nextTurnQueue, prompt)
	}
	event = s.newEventLocked("", QueueUpdatedPayload{Queue: s.queueSnapshotLocked()})
	s.publishLocked(event)
	s.mu.Unlock()
	return nil
}

func (s *harnessSessionState) consumeNextTurn(prompt Prompt) Prompt {
	s.mu.Lock()
	queued := clonePrompts(s.nextTurnQueue)
	if len(queued) > 0 {
		s.nextTurnQueue = nil
		event := s.newEventLocked(s.activeTurnID, QueueUpdatedPayload{
			Queue: s.queueSnapshotLocked(),
		})
		s.publishLocked(event)
	}
	s.mu.Unlock()
	if len(queued) == 0 {
		return prompt.Clone()
	}
	messages := make([]llm.Message, 0, queuedMessageCount(queued)+len(prompt.Messages))
	for _, queuedPrompt := range queued {
		messages = append(messages, queuedPrompt.Clone().Messages...)
	}
	messages = append(messages, prompt.Clone().Messages...)
	return llm.NewPrompt(messages...)
}

func (s *harnessSessionState) abort(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	var (
		cancel          context.CancelFunc
		done            chan struct{}
		clearedSteer    []Prompt
		clearedFollowUp []Prompt
	)
	s.mu.Lock()
	cancel = s.activeCancel
	done = s.activeDone
	clearedSteer = clonePrompts(s.steerQueue)
	clearedFollowUp = clonePrompts(s.followUpQueue)
	if len(s.steerQueue) > 0 || len(s.followUpQueue) > 0 {
		s.steerQueue = nil
		s.followUpQueue = nil
		queueEvent := s.newEventLocked(s.activeTurnID, QueueUpdatedPayload{
			Queue: s.queueSnapshotLocked(),
		})
		s.publishLocked(queueEvent)
	}
	abortEvent := s.newEventLocked(s.activeTurnID, AbortPayload{
		ClearedSteer:    clearedSteer,
		ClearedFollowUp: clearedFollowUp,
	})
	s.publishLocked(abortEvent)
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *harnessSessionState) subscribe(ctx context.Context) <-chan HarnessEvent {
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan HarnessEvent, 64)
	s.mu.Lock()
	if s.subscribers == nil {
		s.subscribers = make(map[int]chan HarnessEvent)
	}
	id := s.nextSubscriber
	s.nextSubscriber++
	s.subscribers[id] = ch
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if current := s.subscribers[id]; current == ch {
			delete(s.subscribers, id)
			close(ch)
		}
		s.mu.Unlock()
	}()
	return ch
}

func (s *harnessSessionState) newEventLocked(
	turnID string,
	payload HarnessEventPayload,
) HarnessEvent {
	s.eventSeq++
	return HarnessEvent{
		SessionID: s.sessionID,
		TurnID:    turnID,
		Seq:       s.eventSeq,
		Payload:   payload,
	}
}

func (s *harnessSessionState) publishLocked(event HarnessEvent) {
	for _, ch := range s.subscribers {
		ch <- event
	}
}

func (s *harnessSessionState) queueSnapshotLocked() QueueSnapshot {
	return QueueSnapshot{
		Steer:    clonePrompts(s.steerQueue),
		FollowUp: clonePrompts(s.followUpQueue),
		NextTurn: clonePrompts(s.nextTurnQueue),
	}
}

func clonePrompts(prompts []Prompt) []Prompt {
	if len(prompts) == 0 {
		return nil
	}
	cloned := make([]Prompt, len(prompts))
	for i, prompt := range prompts {
		cloned[i] = prompt.Clone()
	}
	return cloned
}

func queuedMessageCount(prompts []Prompt) int {
	total := 0
	for _, prompt := range prompts {
		total += len(prompt.Messages)
	}
	return total
}
