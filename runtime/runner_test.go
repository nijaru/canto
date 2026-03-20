package runtime

import (
	"context"
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

// TestRunner_Subscribe_ReceivesEvents is a regression test for the bug where
// Runner.Subscribe loaded a separate *session.Session object from the store,
// causing the subscriber channel to be permanently silent because events were
// emitted on a different in-memory object. After the session registry fix,
// Subscribe and Run share the same object, so events flow through correctly.
func TestRunner_Subscribe_ReceivesEvents(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(store, &echoAgent{})
	sessionID := "sub-test"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := runner.Subscribe(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}

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
		case e, ok := <-ch:
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
		t.Fatal("Subscribe received no events; session registry fix may be broken")
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
		t.Error("Subscribe did not receive the user MessageAdded event")
	}
	if !sawAssistant {
		t.Error("Subscribe did not receive the assistant MessageAdded event")
	}
}

// TestRunner_Subscribe_SharedObject verifies that the same *session.Session is
// returned for repeated getOrLoad calls on the same sessionID.
func TestRunner_Subscribe_SharedObject(t *testing.T) {
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
	})
	runner.Coordinator = NewLocalCoordinator()

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

	runner := NewRunner(store, &slowAgent{delay: 80 * time.Millisecond})
	runner.Coordinator = coord

	result, err := runner.Run(t.Context(), "coord-renew")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Content != "slow done" {
		t.Fatalf("content = %q, want slow done", result.Content)
	}
}
