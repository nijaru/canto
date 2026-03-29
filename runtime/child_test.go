package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func TestChildRunnerSpawnAndWait_Handoff(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	parent := session.New("parent").WithWriter(store)
	childRunner := NewChildRunner(store)
	defer childRunner.Close()

	ref, err := childRunner.Spawn(t.Context(), parent, ChildSpec{
		ID:        "child-1",
		SessionID: "child-session-1",
		Agent:     &echoAgent{},
		Mode:      session.ChildModeHandoff,
		Task:      "Review the change",
		Context:   "Look for regressions",
		InitialMessages: []llm.Message{
			{Role: llm.RoleUser, Content: "Please review the patch."},
		},
	})
	if err != nil {
		t.Fatalf("spawn child: %v", err)
	}

	result, err := childRunner.Wait(t.Context(), ref.ID)
	if err != nil {
		t.Fatalf("wait child: %v", err)
	}
	if result.Result.Content != "pong" {
		t.Fatalf("child result content = %q, want pong", result.Result.Content)
	}

	parentReloaded, err := store.Load(t.Context(), parent.ID())
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	var sawRequested, sawStarted, sawCompleted bool
	for _, event := range parentReloaded.Events() {
		switch event.Type {
		case session.ChildRequested:
			sawRequested = true
		case session.ChildStarted:
			sawStarted = true
		case session.ChildCompleted:
			sawCompleted = true
		}
	}
	if !sawRequested || !sawStarted || !sawCompleted {
		t.Fatalf(
			"expected requested/started/completed child events, got requested=%t started=%t completed=%t",
			sawRequested,
			sawStarted,
			sawCompleted,
		)
	}

	childReloaded, err := store.Load(t.Context(), ref.SessionID)
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	messages := childReloaded.Messages()
	if len(messages) != 2 {
		t.Fatalf("expected 2 child messages, got %d", len(messages))
	}
	if messages[0].Content != "Please review the patch." {
		t.Fatalf("unexpected initial child message: %#v", messages[0])
	}
}

func TestChildRunnerSpawn_ForkCopiesParentHistory(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	parent := session.New("parent").WithWriter(store)
	if err := parent.Append(t.Context(), session.NewMessage(parent.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "Original task",
	})); err != nil {
		t.Fatalf("append parent message: %v", err)
	}

	childRunner := NewChildRunner(store)
	defer childRunner.Close()

	ref, err := childRunner.Spawn(t.Context(), parent, ChildSpec{
		ID:      "child-2",
		Agent:   &echoAgent{},
		Mode:    session.ChildModeFork,
		Context: "Reuse the parent context",
	})
	if err != nil {
		t.Fatalf("spawn forked child: %v", err)
	}
	if _, err := childRunner.Wait(t.Context(), ref.ID); err != nil {
		t.Fatalf("wait forked child: %v", err)
	}

	childReloaded, err := store.Load(t.Context(), ref.SessionID)
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	messages := childReloaded.Messages()
	if len(messages) < 2 {
		t.Fatalf(
			"expected forked child to inherit parent history plus reply, got %d messages",
			len(messages),
		)
	}
	if messages[0].Content != "Original task" {
		t.Fatalf("expected forked child to inherit parent history, got %#v", messages[0])
	}

	parentAncestry, err := store.Parent(t.Context(), ref.SessionID)
	if err != nil {
		t.Fatalf("load child parent ancestry: %v", err)
	}
	if parentAncestry == nil || parentAncestry.SessionID != parent.ID() {
		t.Fatalf("child parent ancestry = %#v, want %q", parentAncestry, parent.ID())
	}

	lineage, err := store.Lineage(t.Context(), ref.SessionID)
	if err != nil {
		t.Fatalf("load child lineage: %v", err)
	}
	if len(lineage) != 2 || lineage[0].SessionID != parent.ID() ||
		lineage[1].SessionID != ref.SessionID {
		t.Fatalf("child lineage = %#v", lineage)
	}
}

func TestChildRunnerWait_ReleasesHandle(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	parent := session.New("parent-release").WithWriter(store)
	childRunner := NewChildRunner(store)
	defer childRunner.Close()

	ref, err := childRunner.Spawn(t.Context(), parent, ChildSpec{
		ID:    "child-release",
		Agent: &echoAgent{},
		Mode:  session.ChildModeHandoff,
		InitialMessages: []llm.Message{
			{Role: llm.RoleUser, Content: "go"},
		},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	if _, err := childRunner.Wait(t.Context(), ref.ID); err != nil {
		t.Fatalf("first wait: %v", err)
	}

	// After Wait returns, the handle should be released — a second Wait must fail.
	if _, err := childRunner.Wait(t.Context(), ref.ID); err == nil {
		t.Error("expected second Wait to return error after handle released, got nil")
	}
}

func TestChildRunnerWaitUnknownChild(t *testing.T) {
	childRunner := NewChildRunner(nil)
	defer childRunner.Close()

	if _, err := childRunner.Wait(t.Context(), "missing-child"); err == nil {
		t.Fatal("expected missing child wait to fail")
	}
}

type blockingAgent struct {
	id      string
	started chan<- string
	release <-chan struct{}
	current *int32
	maxSeen *int32
}

func (a *blockingAgent) ID() string { return a.id }

func (a *blockingAgent) Step(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a *blockingAgent) Turn(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	current := atomic.AddInt32(a.current, 1)
	for {
		max := atomic.LoadInt32(a.maxSeen)
		if current <= max || atomic.CompareAndSwapInt32(a.maxSeen, max, current) {
			break
		}
	}
	defer atomic.AddInt32(a.current, -1)

	select {
	case a.started <- a.id:
	case <-ctx.Done():
		return agent.StepResult{}, ctx.Err()
	}

	select {
	case <-a.release:
	case <-ctx.Done():
		return agent.StepResult{}, ctx.Err()
	}

	msg := llm.Message{Role: llm.RoleAssistant, Content: a.id + " done"}
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), msg)); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: msg.Content}, nil
}

func TestChildRunnerSpawn_MaxConcurrent(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	parent := session.New("parent").WithWriter(store)
	childRunner := NewChildRunner(store, WithMaxConcurrent(2))
	defer childRunner.Close()

	started := make(chan string, 3)
	release := make(chan struct{})
	var current int32
	var maxSeen int32

	waitIDs := make([]string, 0, 3)
	for _, id := range []string{"child-a", "child-b", "child-c"} {
		ref, err := childRunner.Spawn(t.Context(), parent, ChildSpec{
			ID: id,
			Agent: &blockingAgent{
				id:      id,
				started: started,
				release: release,
				current: &current,
				maxSeen: &maxSeen,
			},
		})
		if err != nil {
			t.Fatalf("spawn %s: %v", id, err)
		}
		waitIDs = append(waitIDs, ref.ID)
	}

	startedIDs := make(map[string]struct{}, 2)
	for range 2 {
		select {
		case id := <-started:
			startedIDs[id] = struct{}{}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for initial child starts")
		}
	}

	select {
	case id := <-started:
		t.Fatalf("child %s started before a slot was released", id)
	case <-time.After(100 * time.Millisecond):
	}

	if got := atomic.LoadInt32(&maxSeen); got > 2 {
		t.Fatalf("max concurrent children = %d, want <= 2", got)
	}

	close(release)

	var wg sync.WaitGroup
	for _, id := range waitIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := childRunner.Wait(t.Context(), id); err != nil {
				t.Errorf("wait %s: %v", id, err)
			}
		}(id)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxSeen); got != 2 {
		t.Fatalf("max concurrent children = %d, want 2", got)
	}
}

func TestChildRunnerSpawn_InheritsSpawnContextByDefault(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	parent := session.New("parent").WithWriter(store)
	childRunner := NewChildRunner(store)
	defer childRunner.Close()

	started := make(chan string, 1)
	release := make(chan struct{})
	var current int32
	var maxSeen int32

	spawnCtx, cancel := context.WithCancel(t.Context())
	ref, err := childRunner.Spawn(spawnCtx, parent, ChildSpec{
		ID: "child-attached",
		Agent: &blockingAgent{
			id:      "child-attached",
			started: started,
			release: release,
			current: &current,
			maxSeen: &maxSeen,
		},
	})
	if err != nil {
		t.Fatalf("spawn child: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for child start")
	}

	cancel()

	result, err := childRunner.Wait(t.Context(), ref.ID)
	if err != nil {
		t.Fatalf("wait child: %v", err)
	}
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("child error = %v, want context.Canceled", result.Err)
	}

	parentReloaded, err := store.Load(t.Context(), parent.ID())
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	var sawCanceled bool
	for _, event := range parentReloaded.Events() {
		if event.Type == session.ChildCanceled {
			sawCanceled = true
			break
		}
	}
	if !sawCanceled {
		t.Fatal("expected parent to record child cancellation")
	}
}

func TestChildRunnerSpawn_DetachedIgnoresSpawnContextCancellation(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	parent := session.New("parent").WithWriter(store)
	childRunner := NewChildRunner(store)
	defer childRunner.Close()

	started := make(chan string, 1)
	release := make(chan struct{})
	var current int32
	var maxSeen int32

	spawnCtx, cancel := context.WithCancel(t.Context())
	ref, err := childRunner.Spawn(spawnCtx, parent, ChildSpec{
		ID:       "child-detached",
		Detached: true,
		Agent: &blockingAgent{
			id:      "child-detached",
			started: started,
			release: release,
			current: &current,
			maxSeen: &maxSeen,
		},
	})
	if err != nil {
		t.Fatalf("spawn child: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for detached child start")
	}

	cancel()

	waitCtx, waitCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer waitCancel()
	if _, err := childRunner.Wait(waitCtx, ref.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait before release = %v, want context.DeadlineExceeded", err)
	}

	close(release)

	result, err := childRunner.Wait(t.Context(), ref.ID)
	if err != nil {
		t.Fatalf("wait child: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("detached child err = %v, want nil", result.Err)
	}
	if result.Result.Content != "child-detached done" {
		t.Fatalf("detached child content = %q, want child-detached done", result.Result.Content)
	}
}
