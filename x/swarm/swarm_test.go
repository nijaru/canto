package swarm

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func TestMemoryBlackboard(t *testing.T) {
	ctx := context.Background()
	b := NewMemoryBlackboard()

	// Test AddTask and ListUnclaimed
	task1 := Task{ID: "t1", Description: "task 1"}
	if err := b.AddTask(ctx, task1); err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}
	if err := b.AddTask(ctx, task1); err == nil {
		t.Error("expected error adding duplicate task")
	}

	unclaimed, _ := b.ListUnclaimed(ctx)
	if len(unclaimed) != 1 || unclaimed[0].ID != "t1" {
		t.Errorf("unexpected unclaimed list: %+v", unclaimed)
	}

	// Test Post and ReadAgent
	if err := b.Post(ctx, "a1", "status", "working"); err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	val, _ := b.ReadAgent(ctx, "a1", "status")
	if val != "working" {
		t.Errorf("expected working, got %v", val)
	}

	// Test ClaimTask
	ok, err := b.ClaimTask(ctx, "a1", "t1")
	if !ok || err != nil {
		t.Fatalf("ClaimTask failed: ok=%v, err=%v", ok, err)
	}
	ok, _ = b.ClaimTask(ctx, "a2", "t1")
	if ok {
		t.Error("expected second claim to fail")
	}

	unclaimed, _ = b.ListUnclaimed(ctx)
	if len(unclaimed) != 0 {
		t.Errorf("expected 0 unclaimed, got %d", len(unclaimed))
	}
}

type mockAgent struct {
	id      string
	turns   int32
	turnErr error
}

func (m *mockAgent) ID() string { return m.id }
func (m *mockAgent) Step(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return agent.StepResult{}, nil
}

func (m *mockAgent) Turn(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	atomic.AddInt32(&m.turns, 1)
	if m.turnErr != nil {
		return agent.StepResult{}, m.turnErr
	}
	return agent.StepResult{
		Usage: llm.Usage{TotalTokens: 10, Cost: 0.01},
	}, nil
}

func TestSwarm(t *testing.T) {
	ctx := context.Background()
	sess := session.New("test-session")
	bb := NewMemoryBlackboard()

	// Add tasks
	for i := 1; i <= 5; i++ {
		bb.AddTask(ctx, Task{ID: fmt.Sprintf("t%d", i), Description: "desc"})
	}

	// Create agents
	a1 := &mockAgent{id: "agent1"}
	a2 := &mockAgent{id: "agent2"}

	s := New(bb, 10, a1, a2)
	res, err := s.Run(ctx, sess)
	if err != nil {
		t.Fatalf("Swarm.Run failed: %v", err)
	}

	if res.TasksDone != 5 {
		t.Errorf("expected 5 tasks done, got %d", res.TasksDone)
	}
	if atomic.LoadInt32(&a1.turns)+atomic.LoadInt32(&a2.turns) != 5 {
		t.Errorf("total turns mismatch: a1=%d, a2=%d", a1.turns, a2.turns)
	}
}

func TestSwarmMaxRounds(t *testing.T) {
	ctx := context.Background()
	sess := session.New("test-session")
	bb := NewMemoryBlackboard()

	bb.AddTask(ctx, Task{ID: "t1", Description: "desc"})
	bb.AddTask(ctx, Task{ID: "t2", Description: "desc"})

	a1 := &mockAgent{id: "agent1"}
	// MaxRounds = 1, but 2 tasks exist. This should fail final check.
	s := New(bb, 1, a1)
	_, err := s.Run(ctx, sess)
	if err == nil {
		t.Error("expected error due to max rounds")
	} else {
		t.Logf("Got expected error: %v", err)
	}
}

func TestSwarmAgentFailure(t *testing.T) {
	ctx := context.Background()
	sess := session.New("test-session")
	bb := NewMemoryBlackboard()

	bb.AddTask(ctx, Task{ID: "t1", Description: "desc"})

	a1 := &mockAgent{id: "agent1", turnErr: errors.New("boom")}
	s := New(bb, 5, a1)
	_, err := s.Run(ctx, sess)
	if err == nil {
		t.Fatal("expected error from agent")
	}
	t.Logf("Got expected error: %v", err)
}

func TestSwarmContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sess := session.New("test-session")
	bb := NewMemoryBlackboard()
	bb.AddTask(ctx, Task{ID: "t1", Description: "desc"})

	a1 := &mockAgent{id: "agent1"}
	cancel() // cancel immediately

	s := New(bb, 5, a1)
	_, err := s.Run(ctx, sess)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
