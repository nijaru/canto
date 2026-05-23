package canto

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
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

// QueueMode controls how many queued prompts drain at each provider
// boundary.
type QueueMode string

const (
	QueueOneAtATime QueueMode = "one-at-a-time"
	QueueAll        QueueMode = "all"
)

// HarnessEventKind identifies session-scoped facade events that are not
// necessarily durable session-log entries.
type HarnessEventKind string

const (
	HarnessEventQueueUpdated     HarnessEventKind = "queue_update"
	HarnessEventSavePoint        HarnessEventKind = "save_point"
	HarnessEventSettled          HarnessEventKind = "settled"
	HarnessEventAbort            HarnessEventKind = "abort"
	HarnessEventModelSelected    HarnessEventKind = "model_select"
	HarnessEventThinkingSelected HarnessEventKind = "thinking_select"
	HarnessEventToolsSelected    HarnessEventKind = "tools_select"
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

// ModelSelectedPayload reports a model selection recorded for the session.
type ModelSelectedPayload struct {
	Model         session.ModelSelection
	PreviousModel session.ModelSelection
	HadPrevious   bool
}

func (ModelSelectedPayload) harnessEventPayload() {}
func (ModelSelectedPayload) Kind() HarnessEventKind {
	return HarnessEventModelSelected
}

// ThinkingSelectedPayload reports a thinking/reasoning selection recorded for
// the session.
type ThinkingSelectedPayload struct {
	Level         string
	PreviousLevel string
}

func (ThinkingSelectedPayload) harnessEventPayload() {}
func (ThinkingSelectedPayload) Kind() HarnessEventKind {
	return HarnessEventThinkingSelected
}

// ToolsSelectedPayload reports an active-tool selection recorded for the
// session.
type ToolsSelectedPayload struct {
	Names         []string
	PreviousNames []string
	HadPrevious   bool
}

func (ToolsSelectedPayload) harnessEventPayload() {}
func (ToolsSelectedPayload) Kind() HarnessEventKind {
	return HarnessEventToolsSelected
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
	steerMode      QueueMode
	followUpMode   QueueMode
	activeTools    []string
	hasActiveTools bool
}

func newHarnessSessionState(sessionID string) *harnessSessionState {
	return &harnessSessionState{
		sessionID:    sessionID,
		phase:        HarnessPhaseIdle,
		steerMode:    QueueOneAtATime,
		followUpMode: QueueOneAtATime,
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

// Steer queues prompt input for in-flight steering. Steering drains after the
// current assistant/tool step and before the next provider request.
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
// stop.
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

// QueuedInput returns the current session-scoped input queue snapshot.
func (s *Session) QueuedInput() QueueSnapshot {
	if s == nil || s.state == nil {
		return QueueSnapshot{}
	}
	return s.state.queuedInput()
}

// ClearQueuedInput clears queued steer, follow-up, and next-turn prompts and
// returns the prompts that were removed.
func (s *Session) ClearQueuedInput(ctx context.Context) (QueueSnapshot, error) {
	if s == nil || s.state == nil {
		return QueueSnapshot{}, fmt.Errorf("canto harness: nil session state")
	}
	return s.state.clearQueuedInput(ctx)
}

// SteeringMode returns how queued steering prompts drain.
func (s *Session) SteeringMode() QueueMode {
	if s == nil || s.state == nil {
		return QueueOneAtATime
	}
	return s.state.queueMode(queueKindSteer)
}

// SetSteeringMode configures how queued steering prompts drain.
func (s *Session) SetSteeringMode(mode QueueMode) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("canto harness: nil session state")
	}
	return s.state.setQueueMode(queueKindSteer, mode)
}

// FollowUpMode returns how queued follow-up prompts drain.
func (s *Session) FollowUpMode() QueueMode {
	if s == nil || s.state == nil {
		return QueueOneAtATime
	}
	return s.state.queueMode(queueKindFollowUp)
}

// SetFollowUpMode configures how queued follow-up prompts drain.
func (s *Session) SetFollowUpMode(mode QueueMode) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("canto harness: nil session state")
	}
	return s.state.setQueueMode(queueKindFollowUp, mode)
}

// ActiveToolNames returns the tool names active for the next turn. Without an
// explicit session selection, all registered harness tools are active.
func (s *Session) ActiveToolNames(ctx context.Context) ([]string, error) {
	if s == nil || s.state == nil {
		return nil, fmt.Errorf("canto harness: nil session state")
	}
	if names, ok := s.state.activeToolNames(); ok {
		return names, nil
	}
	settings, err := s.EffectiveSettings(ctx)
	if err != nil {
		return nil, err
	}
	if settings.HasTools {
		s.state.setActiveToolNames(settings.ActiveTools)
		return append([]string(nil), settings.ActiveTools...), nil
	}
	return s.defaultToolNames(), nil
}

// SetActiveTools records the active tool names for future turns. Names must
// exist in the harness registry; pass no names to run without tools.
func (s *Session) SetActiveTools(ctx context.Context, names ...string) error {
	if err := s.validateMaintenanceHandle(); err != nil {
		return err
	}
	names = normalizeToolNames(names)
	if err := s.validateToolNames(names); err != nil {
		return err
	}
	replayed, err := s.Replay(ctx)
	if err != nil {
		return err
	}
	previous, err := replayed.EffectiveSettings()
	if err != nil {
		return err
	}
	selection := session.ToolSelection{Names: append([]string(nil), names...)}
	if err := replayed.AppendToolSelection(ctx, selection); err != nil {
		return err
	}
	s.state.setActiveToolNames(names)
	s.publishRuntimeEvent(ToolsSelectedPayload{
		Names:         append([]string(nil), names...),
		PreviousNames: append([]string(nil), previous.ActiveTools...),
		HadPrevious:   previous.HasTools,
	})
	return nil
}

// Abort cancels the active turn, clears steering/follow-up queues, and waits
// until the session facade reaches idle.
func (s *Session) Abort(ctx context.Context) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("canto harness: nil session state")
	}
	return s.state.abort(ctx)
}

func (s *Session) activeToolRegistry(ctx context.Context) (*tool.Registry, error) {
	if s == nil || s.harness == nil {
		return nil, fmt.Errorf("canto harness: nil session")
	}
	all := s.harness.Tools
	if all == nil {
		return nil, nil
	}
	names, err := s.ActiveToolNames(ctx)
	if err != nil {
		return nil, err
	}
	if slices.Equal(names, all.Names()) {
		return all, nil
	}
	return all.Subset(names...)
}

func (s *Session) defaultToolNames() []string {
	if s == nil || s.harness == nil || s.harness.Tools == nil {
		return nil
	}
	return s.harness.Tools.Names()
}

func (s *Session) validateToolNames(names []string) error {
	if s == nil || s.harness == nil || s.harness.Tools == nil {
		if len(names) > 0 {
			return fmt.Errorf("canto session tools: no registry")
		}
		return nil
	}
	for _, name := range names {
		if _, ok := s.harness.Tools.Get(name); !ok {
			return fmt.Errorf("canto session tools: unknown tool %q", name)
		}
	}
	return nil
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

func (s *harnessSessionState) activeToolNames() ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasActiveTools {
		return nil, false
	}
	return append([]string(nil), s.activeTools...), true
}

func (s *harnessSessionState) setActiveToolNames(names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeTools = append([]string(nil), names...)
	s.hasActiveTools = true
}

type queueKind int

const (
	queueKindSteer queueKind = iota
	queueKindFollowUp
	queueKindNextTurn
)

type harnessInputQueues struct {
	h *Harness
}

func (q harnessInputQueues) DrainSteering(
	ctx context.Context,
	sess *session.Session,
) (Prompt, bool, error) {
	return q.drain(ctx, sess, queueKindSteer)
}

func (q harnessInputQueues) DrainFollowUp(
	ctx context.Context,
	sess *session.Session,
) (Prompt, bool, error) {
	return q.drain(ctx, sess, queueKindFollowUp)
}

func (q harnessInputQueues) drain(
	ctx context.Context,
	sess *session.Session,
	kind queueKind,
) (Prompt, bool, error) {
	if q.h == nil || sess == nil {
		return Prompt{}, false, nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return Prompt{}, false, err
		}
	}
	return q.h.sessionState(sess.ID()).drainQueuedPrompt(kind)
}

func (s *harnessSessionState) queueMode(kind queueKind) QueueMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch kind {
	case queueKindSteer:
		return s.steerMode
	case queueKindFollowUp:
		return s.followUpMode
	default:
		return QueueOneAtATime
	}
}

func (s *harnessSessionState) setQueueMode(kind queueKind, mode QueueMode) error {
	if mode != QueueOneAtATime && mode != QueueAll {
		return fmt.Errorf("canto session queue: unsupported mode %q", mode)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch kind {
	case queueKindSteer:
		s.steerMode = mode
	case queueKindFollowUp:
		s.followUpMode = mode
	default:
		return fmt.Errorf("canto session queue: unsupported mode queue kind %d", kind)
	}
	return nil
}

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

func (s *harnessSessionState) drainQueuedPrompt(kind queueKind) (Prompt, bool, error) {
	s.mu.Lock()
	var prompts []Prompt
	switch kind {
	case queueKindSteer:
		prompts = drainPromptQueue(&s.steerQueue, s.steerMode)
	case queueKindFollowUp:
		prompts = drainPromptQueue(&s.followUpQueue, s.followUpMode)
	default:
		s.mu.Unlock()
		return Prompt{}, false, fmt.Errorf("canto session queue: unsupported drain kind %d", kind)
	}
	if len(prompts) == 0 {
		s.mu.Unlock()
		return Prompt{}, false, nil
	}
	event := s.newEventLocked(s.activeTurnID, QueueUpdatedPayload{
		Queue: s.queueSnapshotLocked(),
	})
	s.publishLocked(event)
	s.mu.Unlock()
	return combinePrompts(prompts), true, nil
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

func (s *harnessSessionState) queuedInput() QueueSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queueSnapshotLocked()
}

func (s *harnessSessionState) clearQueuedInput(ctx context.Context) (QueueSnapshot, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return QueueSnapshot{}, err
		}
	}

	s.mu.Lock()
	cleared := s.queueSnapshotLocked()
	if queueSnapshotEmpty(cleared) {
		s.mu.Unlock()
		return cleared, nil
	}
	s.steerQueue = nil
	s.followUpQueue = nil
	s.nextTurnQueue = nil
	event := s.newEventLocked(s.activeTurnID, QueueUpdatedPayload{
		Queue: s.queueSnapshotLocked(),
	})
	s.publishLocked(event)
	s.mu.Unlock()
	return cleared, nil
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
		turnID          string
		clearedSteer    []Prompt
		clearedFollowUp []Prompt
	)
	s.mu.Lock()
	cancel = s.activeCancel
	done = s.activeDone
	turnID = s.activeTurnID
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
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	abortEvent := s.newEventLocked(turnID, AbortPayload{
		ClearedSteer:    clearedSteer,
		ClearedFollowUp: clearedFollowUp,
	})
	s.publishLocked(abortEvent)
	s.mu.Unlock()
	return nil
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

func queueSnapshotEmpty(snapshot QueueSnapshot) bool {
	return len(snapshot.Steer) == 0 &&
		len(snapshot.FollowUp) == 0 &&
		len(snapshot.NextTurn) == 0
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

func drainPromptQueue(queue *[]Prompt, mode QueueMode) []Prompt {
	if len(*queue) == 0 {
		return nil
	}
	count := 1
	if mode == QueueAll {
		count = len(*queue)
	}
	drained := clonePrompts((*queue)[:count])
	remaining := append([]Prompt(nil), (*queue)[count:]...)
	*queue = remaining
	return drained
}

func combinePrompts(prompts []Prompt) Prompt {
	if len(prompts) == 0 {
		return Prompt{}
	}
	if len(prompts) == 1 {
		return prompts[0].Clone()
	}
	messages := make([]llm.Message, 0, queuedMessageCount(prompts))
	for _, prompt := range prompts {
		messages = append(messages, prompt.Clone().Messages...)
	}
	return llm.NewPrompt(messages...)
}

func queuedMessageCount(prompts []Prompt) int {
	total := 0
	for _, prompt := range prompts {
		total += len(prompt.Messages)
	}
	return total
}

func normalizeToolNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}
