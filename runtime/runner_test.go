package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// echoAgent appends a single assistant message and returns.
type echoAgent struct{}

func (e *echoAgent) ID() string { return "echo" }

func (e *echoAgent) Step(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	msg := llm.Message{Role: llm.RoleAssistant, Content: "pong"}
	if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.MessageAdded, msg)); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: "pong"}, nil
}

func (e *echoAgent) Turn(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return e.Step(ctx, sess)
}

var errTestOverflow = errors.New("context_length_exceeded")

type overflowRecoveryAgent struct {
	calls      atomic.Int64
	sawCompact atomic.Bool
}

func (a *overflowRecoveryAgent) ID() string { return "overflow-recovery" }

func (a *overflowRecoveryAgent) Step(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a *overflowRecoveryAgent) Turn(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	if a.calls.Add(1) == 1 {
		return agent.StepResult{}, errTestOverflow
	}
	msgs, err := sess.EffectiveMessages()
	if err != nil {
		return agent.StepResult{}, err
	}
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "compacted context") {
			a.sawCompact.Store(true)
			break
		}
	}
	msg := llm.Message{Role: llm.RoleAssistant, Content: "recovered"}
	if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.MessageAdded, msg)); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: "recovered"}, nil
}

// TestRunner_Watch_ReceivesEvents is a regression test for the bug where
// Runner.Watch loaded a separate *session.Session object from the store,
// causing the subscriber channel to be permanently silent because events were
// emitted on a different in-memory object. After the session registry fix,
// Watch and Run share the same object, so events flow through correctly.
func TestRunner_Watch_ReceivesEvents(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(store, &echoAgent{})
	sessionID := "sub-test"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub, err := runner.Watch(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	result, err := runner.Send(ctx, sessionID, "ping")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "pong" {
		t.Errorf("expected Content=pong, got %q", result.Content)
	}

	// Drain the channel — collect events until it's quiet for 50ms.
	var events []session.Event
	idle := time.NewTimer(50 * time.Millisecond)
	defer idle.Stop()
collect:
	for {
		select {
		case e, ok := <-sub.Events():
			if !ok {
				break collect
			}
			events = append(events, e)
			if !idle.Stop() {
				<-idle.C
			}
			idle.Reset(50 * time.Millisecond)
		case <-idle.C:
			break collect
		}
	}

	if len(events) == 0 {
		t.Fatal("Watch received no events; session registry fix may be broken")
	}

	// Confirm we saw the user message and the assistant reply.
	var sawUser, sawAssistant bool
	for _, e := range events {
		if e.Type != session.MessageAdded {
			continue
		}
		var m llm.Message
		if err := e.UnmarshalData(&m); err != nil {
			continue
		}
		switch m.Role {
		case llm.RoleUser:
			sawUser = true
		case llm.RoleAssistant:
			sawAssistant = true
		}
	}
	if !sawUser {
		t.Error("Watch did not receive the user MessageAdded event")
	}
	if !sawAssistant {
		t.Error("Watch did not receive the assistant MessageAdded event")
	}
}

// TestRunner_Watch_SharedObject verifies that the same *session.Session is
// returned for repeated getOrLoad calls on the same sessionID.
func TestRunner_Watch_SharedObject(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(store, &echoAgent{})
	ctx := context.Background()
	sessionID := "shared-obj"

	s1, err := runner.getOrLoad(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := runner.getOrLoad(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Error(
			"getOrLoad returned different *session.Session objects for the same sessionID; registry is not working",
		)
	}
}

func TestRunnerOverflowRecoveryCompactsAndRebuildsOnce(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	recoveryAgent := &overflowRecoveryAgent{}
	var compactCalls atomic.Int64
	runner := NewRunner(
		store,
		recoveryAgent,
		WithOverflowRecovery(
			func(err error) bool { return errors.Is(err, errTestOverflow) },
			func(ctx context.Context, sess *session.Session) error {
				compactCalls.Add(1)
				return sess.Append(ctx, session.NewContext(
					sess.ID(),
					session.ContextEntry{
						Kind:      session.ContextKindSummary,
						Placement: session.ContextPlacementPrefix,
						Content:   "<conversation_summary>\ncompacted context\n</conversation_summary>",
					},
				))
			},
			1,
		),
	)

	result, err := runner.Send(t.Context(), "overflow-recovery", "please recover")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Content != "recovered" {
		t.Fatalf("result content = %q, want recovered", result.Content)
	}
	if compactCalls.Load() != 1 {
		t.Fatalf("compact calls = %d, want 1", compactCalls.Load())
	}
	if recoveryAgent.calls.Load() != 2 {
		t.Fatalf("agent calls = %d, want 2", recoveryAgent.calls.Load())
	}
	if !recoveryAgent.sawCompact.Load() {
		t.Fatal("retry did not rebuild from compacted session context")
	}

	sess, err := store.Load(t.Context(), "overflow-recovery")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	var userMessages int
	for _, msg := range sess.Messages() {
		if msg.Role == llm.RoleUser && msg.Content == "please recover" {
			userMessages++
		}
	}
	if userMessages != 1 {
		t.Fatalf("user messages = %d, want 1", userMessages)
	}
}

func TestRunnerOverflowRecoveryRebuildsBaseAgentRequest(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	provider := llm.NewFauxProvider(
		"faux",
		llm.FauxStep{Err: errTestOverflow},
		llm.FauxStep{Content: "recovered"},
	)
	provider.IsContextOverflowFn = func(err error) bool {
		return errors.Is(err, errTestOverflow)
	}
	a := agent.New("overflow-base", "System instructions.", "faux", provider, nil)
	runner := NewRunner(
		store,
		a,
		WithOverflowRecovery(
			provider.IsContextOverflow,
			func(ctx context.Context, sess *session.Session) error {
				return sess.Append(ctx, session.NewContext(
					sess.ID(),
					session.ContextEntry{
						Kind:      session.ContextKindSummary,
						Placement: session.ContextPlacementPrefix,
						Content:   "<conversation_summary>\ncompacted context\n</conversation_summary>",
					},
				))
			},
			1,
		),
	)

	result, err := runner.Send(t.Context(), "overflow-base", "please recover")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Content != "recovered" {
		t.Fatalf("result content = %q, want recovered", result.Content)
	}

	calls := provider.Calls()
	if len(calls) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(calls))
	}
	if requestContains(calls[0], "compacted context") {
		t.Fatal("first provider request unexpectedly contained compacted context")
	}
	if !requestContains(calls[1], "compacted context") {
		t.Fatal("retry provider request did not include compacted context")
	}
	if countRequestContent(calls[1], "please recover") != 1 {
		t.Fatalf(
			"retry request user message count = %d, want 1",
			countRequestContent(calls[1], "please recover"),
		)
	}
}

func requestContains(req *llm.Request, content string) bool {
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, content) {
			return true
		}
	}
	return false
}

func countRequestContent(req *llm.Request, content string) int {
	var count int
	for _, msg := range req.Messages {
		if msg.Content == content {
			count++
		}
	}
	return count
}

// TestRunner_Evict verifies that Evict removes the session from the registry
// so that the next getOrLoad reloads from the store.
func TestRunner_Evict(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(store, &echoAgent{})
	ctx := context.Background()
	sessionID := "evict-test"

	s1, err := runner.getOrLoad(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	runner.Evict(sessionID)

	s2, err := runner.getOrLoad(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if s1 == s2 {
		t.Error("expected a new *session.Session after Evict, but got the same object")
	}
}

func TestRunner_Evict_DoesNotDropActiveLane(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(store, &slowAgent{delay: 50 * time.Millisecond})
	sessionID := "evict-active"

	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = runner.Send(ctx, sessionID, "ping")
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		_, ok := runner.sessions[sessionID]
		runner.mu.Unlock()
		if ok && runner.queue.IsActive(sessionID) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	runner.Evict(sessionID)

	runner.mu.Lock()
	_, stillPresent := runner.sessions[sessionID]
	runner.mu.Unlock()
	if !stillPresent {
		t.Fatal("Evict removed a session with active local execution")
	}

	<-done
}

type coordinatorBlockingAgent struct {
	started chan<- string
	release <-chan struct{}
	current *int32
	maxSeen *int32
}

func (a *coordinatorBlockingAgent) ID() string { return "coordinator-blocking" }

func (a *coordinatorBlockingAgent) Step(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a *coordinatorBlockingAgent) Turn(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	current := atomic.AddInt32(a.current, 1)
	for {
		max := atomic.LoadInt32(a.maxSeen)
		if current <= max || atomic.CompareAndSwapInt32(a.maxSeen, max, current) {
			break
		}
	}
	defer atomic.AddInt32(a.current, -1)

	select {
	case a.started <- sess.ID():
	case <-ctx.Done():
		return agent.StepResult{}, ctx.Err()
	}

	select {
	case <-a.release:
	case <-ctx.Done():
		return agent.StepResult{}, ctx.Err()
	}

	msg := llm.Message{Role: llm.RoleAssistant, Content: "done " + sess.ID()}
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), msg)); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: msg.Content}, nil
}

type slowAgent struct {
	delay time.Duration
}

func (a *slowAgent) ID() string { return "slow" }

func (a *slowAgent) Step(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a *slowAgent) Turn(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	select {
	case <-time.After(a.delay):
	case <-ctx.Done():
		return agent.StepResult{}, ctx.Err()
	}
	msg := llm.Message{Role: llm.RoleAssistant, Content: "slow done"}
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), msg)); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: msg.Content}, nil
}

type inspectQueuedSendAgent struct {
	entered chan struct{}
	readNow chan struct{}
	release chan struct{}
	seen    chan []llm.Message
	calls   atomic.Int32
}

func (a *inspectQueuedSendAgent) ID() string { return "inspect-queued-send" }

func (a *inspectQueuedSendAgent) Step(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a *inspectQueuedSendAgent) Turn(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	call := a.calls.Add(1)
	if call == 1 {
		close(a.entered)
		select {
		case <-a.readNow:
		case <-ctx.Done():
			return agent.StepResult{}, ctx.Err()
		}
	}

	messages, err := sess.EffectiveMessages()
	if err != nil {
		return agent.StepResult{}, err
	}
	select {
	case a.seen <- messages:
	case <-ctx.Done():
		return agent.StepResult{}, ctx.Err()
	}

	if call == 1 {
		select {
		case <-a.release:
		case <-ctx.Done():
			return agent.StepResult{}, ctx.Err()
		}
	}

	content := "done"
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleAssistant,
		Content: content,
	})); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: content}, nil
}

func TestRunnerSendAppendsUserInsideSerializedLane(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts []Option
	}{
		{name: "local queue"},
		{name: "coordinator", opts: []Option{WithCoordinator(NewLocalCoordinator())}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, err := session.NewSQLiteStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()

			agent := &inspectQueuedSendAgent{
				entered: make(chan struct{}),
				readNow: make(chan struct{}),
				release: make(chan struct{}),
				seen:    make(chan []llm.Message, 2),
			}
			opts := []Option{
				WithWaitTimeout(time.Second),
				WithExecutionTimeout(5 * time.Second),
			}
			opts = append(opts, tt.opts...)
			runner := NewRunner(store, agent, opts...)
			defer runner.Close()

			firstErr := make(chan error, 1)
			go func() {
				_, err := runner.Send(t.Context(), "serialized-send-"+tt.name, "first")
				firstErr <- err
			}()

			select {
			case <-agent.entered:
			case err := <-firstErr:
				t.Fatalf("first send finished before inspection: %v", err)
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for first turn")
			}

			secondErr := make(chan error, 1)
			go func() {
				_, err := runner.Send(t.Context(), "serialized-send-"+tt.name, "second")
				secondErr <- err
			}()

			time.Sleep(25 * time.Millisecond)
			close(agent.readNow)

			var firstMessages []llm.Message
			select {
			case firstMessages = <-agent.seen:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for first observed history")
			}

			var userMessages []string
			for _, msg := range firstMessages {
				if msg.Role == llm.RoleUser {
					userMessages = append(userMessages, msg.Content)
				}
			}
			if len(userMessages) != 1 || userMessages[0] != "first" {
				t.Fatalf("first turn saw user messages %#v, want only first", userMessages)
			}

			close(agent.release)

			select {
			case err := <-firstErr:
				if err != nil {
					t.Fatalf("first send: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for first send")
			}
			select {
			case err := <-secondErr:
				if err != nil {
					t.Fatalf("second send: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for second send")
			}
		})
	}
}

type blockingStreamProvider struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (p *blockingStreamProvider) ID() string { return "blocking-stream" }

func (p *blockingStreamProvider) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: "done"}, nil
}

func (p *blockingStreamProvider) Stream(
	ctx context.Context,
	_ *llm.Request,
) (llm.Stream, error) {
	p.calls.Add(1)
	select {
	case p.started <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return llm.NewFauxStream(llm.Chunk{Content: "done"}), nil
}

func (p *blockingStreamProvider) Models(context.Context) ([]llm.Model, error) { return nil, nil }

func (p *blockingStreamProvider) CountTokens(
	context.Context,
	string,
	[]llm.Message,
) (int, error) {
	return 0, nil
}

func (p *blockingStreamProvider) Cost(context.Context, string, llm.Usage) float64 {
	return 0
}

func (p *blockingStreamProvider) Capabilities(string) llm.Capabilities {
	return llm.DefaultCapabilities()
}

func (p *blockingStreamProvider) IsTransient(error) bool { return false }

func (p *blockingStreamProvider) IsContextOverflow(error) bool { return false }

func TestRunnerLocalQueueWaitTimeoutDoesNotCancelActiveTurn(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	provider := &blockingStreamProvider{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	runner := NewRunner(
		store,
		agent.New("test-agent", "", "test-model", provider, nil),
		WithWaitTimeout(10*time.Millisecond),
		WithExecutionTimeout(time.Second),
	)
	defer runner.Close()

	done := make(chan error, 1)
	go func() {
		_, err := runner.SendStream(
			t.Context(),
			"active-wait-timeout",
			"first",
			func(*llm.Chunk) {},
		)
		done <- err
	}()

	select {
	case <-provider.started:
	case err := <-done:
		t.Fatalf("SendStream finished before provider stream started: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider stream to start")
	}

	time.Sleep(25 * time.Millisecond)
	close(provider.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SendStream returned error after active wait timeout: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active turn to finish")
	}
}

func TestRunnerQueuedTurnWaitTimeoutRecordsTerminalEvent(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	provider := &blockingStreamProvider{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	runner := NewRunner(
		store,
		agent.New("test-agent", "", "test-model", provider, nil),
		WithWaitTimeout(10*time.Millisecond),
		WithExecutionTimeout(time.Second),
	)
	defer runner.Close()

	firstErr := make(chan error, 1)
	go func() {
		_, err := runner.SendStream(
			t.Context(),
			"queued-wait-timeout",
			"first",
			func(*llm.Chunk) {},
		)
		firstErr <- err
	}()

	select {
	case <-provider.started:
	case err := <-firstErr:
		t.Fatalf("first SendStream finished before provider stream started: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first turn to start")
	}

	_, err = runner.SendStream(t.Context(), "queued-wait-timeout", "second", func(*llm.Chunk) {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued SendStream error = %v, want context deadline exceeded", err)
	}

	close(provider.release)
	select {
	case err := <-firstErr:
		if err != nil {
			t.Fatalf("first SendStream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first turn to finish")
	}

	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider stream calls = %d, want 1", got)
	}

	sess, err := store.Load(t.Context(), "queued-wait-timeout")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	var terminalErrors int
	for _, ev := range sess.Events() {
		data, ok, err := ev.TurnCompletedData()
		if err != nil {
			t.Fatalf("decode turn completed: %v", err)
		}
		if ok && data.Error == context.DeadlineExceeded.Error() {
			terminalErrors++
		}
	}
	if terminalErrors != 1 {
		t.Fatalf("terminal timeout errors = %d, want 1", terminalErrors)
	}
}

func TestRunnerCoordinator_SerializesPerSession(t *testing.T) {
	store, err := session.NewSQLiteStore(t.TempDir() + "/coord-session.db")
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan string, 2)
	release := make(chan struct{})
	var current int32
	var maxSeen int32

	runner := NewRunner(store, &coordinatorBlockingAgent{
		started: started,
		release: release,
		current: &current,
		maxSeen: &maxSeen,
	}, WithCoordinator(NewLocalCoordinator()))

	ctx := t.Context()
	sessionID := "coord-session"

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runner.Run(ctx, sessionID)
			errs <- err
		}()
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for coordinator-backed run to start")
	}

	select {
	case id := <-started:
		t.Fatalf("second run for %s started before release", id)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("run error: %v", err)
		}
	}
	if got := atomic.LoadInt32(&maxSeen); got != 1 {
		t.Fatalf("max concurrent same-session runs = %d, want 1", got)
	}
}

func TestRunnerCoordinator_RenewsLeaseForLongRunningWork(t *testing.T) {
	store, err := session.NewSQLiteStore(t.TempDir() + "/coord-renew.db")
	if err != nil {
		t.Fatal(err)
	}

	coord := NewLocalCoordinator()
	coord.SetLeaseTTL(20 * time.Millisecond)

	runner := NewRunner(store, &slowAgent{delay: 80 * time.Millisecond}, WithCoordinator(coord))

	result, err := runner.Run(t.Context(), "coord-renew")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Content != "slow done" {
		t.Fatalf("content = %q, want slow done", result.Content)
	}
}

func TestRunnerCoordinator_WaitTimeoutDoesNotPoisonLane(t *testing.T) {
	store, err := session.NewSQLiteStore(t.TempDir() + "/coord-timeout.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	started := make(chan string, 3)
	release := make(chan struct{})
	var current int32
	var maxSeen int32
	runner := NewRunner(
		store,
		&coordinatorBlockingAgent{
			started: started,
			release: release,
			current: &current,
			maxSeen: &maxSeen,
		},
		WithCoordinator(NewLocalCoordinator()),
		WithWaitTimeout(10*time.Millisecond),
	)
	defer runner.Close()

	firstErr := make(chan error, 1)
	go func() {
		_, err := runner.Run(t.Context(), "coord-timeout")
		firstErr <- err
	}()
	select {
	case <-started:
	case err := <-firstErr:
		t.Fatalf("first Run finished before start: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first run to start")
	}

	if _, err := runner.Run(t.Context(), "coord-timeout"); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Fatalf("second Run error = %v, want context deadline exceeded", err)
	}

	close(release)
	select {
	case err := <-firstErr:
		if err != nil {
			t.Fatalf("first Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first run to finish")
	}

	if _, err := runner.Run(t.Context(), "coord-timeout"); err != nil {
		t.Fatalf("third Run after timed-out queued turn: %v", err)
	}
}

func TestRunnerDelegate_UsesSharedChildRunner(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	parent := session.New("delegate-parent").WithWriter(store)
	runner := NewRunner(store, &echoAgent{})
	defer runner.Close()
	runner.sessions[parent.ID()] = parent

	result, err := runner.Delegate(t.Context(), parent.ID(), ChildSpec{
		ID:    "child-delegate",
		Agent: &echoAgent{},
		Mode:  session.ChildModeFresh,
		InitialMessages: []llm.Message{
			{Role: llm.RoleUser, Content: "go"},
		},
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if result.Status != session.ChildStatusCompleted {
		t.Fatalf("child status = %q, want %q", result.Status, session.ChildStatusCompleted)
	}
	if result.Summary != "pong" {
		t.Fatalf("child summary = %q, want pong", result.Summary)
	}
}

func TestRunnerSpawnChildAndWaitChild(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	parent := session.New("spawn-parent").WithWriter(store)
	runner := NewRunner(store, &echoAgent{})
	defer runner.Close()
	runner.sessions[parent.ID()] = parent

	ref, err := runner.SpawnChild(t.Context(), parent.ID(), ChildSpec{
		ID:    "child-spawn",
		Agent: &echoAgent{},
		Mode:  session.ChildModeFresh,
		InitialMessages: []llm.Message{
			{Role: llm.RoleUser, Content: "go"},
		},
	})
	if err != nil {
		t.Fatalf("spawn child: %v", err)
	}

	result, err := runner.WaitChild(t.Context(), ref.ID)
	if err != nil {
		t.Fatalf("wait child: %v", err)
	}
	if result.Ref.ID != ref.ID {
		t.Fatalf("child ref id = %q, want %q", result.Ref.ID, ref.ID)
	}
	if result.Summary != "pong" {
		t.Fatalf("child summary = %q, want pong", result.Summary)
	}
}

func TestRunnerScheduleChild_DelaysExecutionUntilDue(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	parent := session.New("scheduled-parent").WithWriter(store)
	runner := NewRunner(store, &echoAgent{})
	defer runner.Close()
	runner.sessions[parent.ID()] = parent

	started := make(chan string, 1)
	release := make(chan struct{})
	var current int32
	var maxSeen int32

	dueAt := time.Now().Add(50 * time.Millisecond)
	handle, err := runner.ScheduleChild(t.Context(), parent.ID(), dueAt, ChildSpec{
		ID: "scheduled-child",
		Agent: &blockingAgent{
			id:      "scheduled-child",
			started: started,
			release: release,
			current: &current,
			maxSeen: &maxSeen,
		},
		Mode: session.ChildModeFresh,
	})
	if err != nil {
		t.Fatalf("schedule child: %v", err)
	}
	if got := handle.ScheduleRef(); got.ID == "" || got.DueAt.IsZero() || got.Queued.IsZero() {
		t.Fatalf("schedule ref not populated: %#v", got)
	}
	if got := handle.ChildRef(); got.ID != "scheduled-child" || got.SessionID == "" {
		t.Fatalf("child ref not populated: %#v", got)
	}

	select {
	case id := <-started:
		t.Fatalf("child %q started before due time", id)
	case <-time.After(20 * time.Millisecond):
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for scheduled child to start")
	}

	close(release)

	result, err := handle.Wait(t.Context())
	if err != nil {
		t.Fatalf("wait scheduled child: %v", err)
	}
	if result.Status != session.ChildStatusCompleted {
		t.Fatalf(
			"scheduled child status = %q, want %q",
			result.Status,
			session.ChildStatusCompleted,
		)
	}
	if result.Summary != "scheduled-child done" {
		t.Fatalf("scheduled child summary = %q, want %q", result.Summary, "scheduled-child done")
	}
	if result.Ref.ID != "scheduled-child" {
		t.Fatalf("scheduled child ref = %q, want scheduled-child", result.Ref.ID)
	}
}

func TestRunnerScheduleChild_CancelBeforeStart(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	parent := session.New("scheduled-cancel-parent").WithWriter(store)
	runner := NewRunner(store, &echoAgent{})
	defer runner.Close()
	runner.sessions[parent.ID()] = parent

	started := make(chan string, 1)
	release := make(chan struct{})
	var current int32
	var maxSeen int32

	handle, err := runner.ScheduleChild(
		t.Context(),
		parent.ID(),
		time.Now().Add(time.Hour),
		ChildSpec{
			ID: "scheduled-cancel-child",
			Agent: &blockingAgent{
				id:      "scheduled-cancel-child",
				started: started,
				release: release,
				current: &current,
				maxSeen: &maxSeen,
			},
			Mode: session.ChildModeFresh,
		},
	)
	if err != nil {
		t.Fatalf("schedule child: %v", err)
	}

	if err := handle.Cancel(t.Context()); err != nil {
		t.Fatalf("cancel scheduled child: %v", err)
	}

	select {
	case id := <-started:
		t.Fatalf("child %q started after cancel", id)
	case <-time.After(20 * time.Millisecond):
	}

	result, err := handle.Wait(t.Context())
	if err != nil {
		t.Fatalf("wait canceled scheduled child: %v", err)
	}
	if result.Status != session.ChildStatusCanceled {
		t.Fatalf("scheduled child status = %q, want %q", result.Status, session.ChildStatusCanceled)
	}
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("scheduled child err = %v, want context.Canceled", result.Err)
	}
}
