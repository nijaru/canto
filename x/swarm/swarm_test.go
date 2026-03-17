package swarm_test

import (
	"context"
	"testing"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/x/swarm"
)

// mockProvider returns canned responses and never fails.
type mockProvider struct {
	llm.Provider
	msg string
}

func (m *mockProvider) ID() string                             { return "mock" }
func (m *mockProvider) Capabilities(_ string) llm.Capabilities { return llm.DefaultCapabilities() }
func (m *mockProvider) IsTransient(_ error) bool               { return false }
func (m *mockProvider) Generate(_ context.Context, _ *llm.LLMRequest) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{Content: m.msg}, nil
}

func TestSwarmClaimsAllTasks(t *testing.T) {
	ctx := context.Background()
	board := swarm.NewMemoryBlackboard()

	// Add 3 tasks.
	tasks := []swarm.Task{
		{ID: "task-1", Description: "Write introduction"},
		{ID: "task-2", Description: "Write body"},
		{ID: "task-3", Description: "Write conclusion"},
	}
	for _, task := range tasks {
		if err := board.AddTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	// 3 agents, each with a simple mock that replies once.
	agents := []agent.Agent{
		agent.New("writer-1", "You are writer 1.", "gpt-4", &mockProvider{msg: "part 1 done"}, nil),
		agent.New("writer-2", "You are writer 2.", "gpt-4", &mockProvider{msg: "part 2 done"}, nil),
		agent.New("writer-3", "You are writer 3.", "gpt-4", &mockProvider{msg: "part 3 done"}, nil),
	}

	s := swarm.New(board, 5, agents...)
	sess := session.New("test-swarm-session")

	result, err := s.Run(ctx, sess)
	if err != nil {
		t.Fatalf("swarm.Run: %v", err)
	}

	// All 3 tasks should have been claimed and executed.
	if result.TasksDone != 3 {
		t.Errorf("expected 3 tasks done, got %d", result.TasksDone)
	}

	// No tasks should remain unclaimed.
	remaining, _ := board.ListUnclaimed(ctx)
	if len(remaining) != 0 {
		t.Errorf("expected 0 unclaimed tasks, got %d", len(remaining))
	}
}

func TestSwarmNoDoubleClaim(t *testing.T) {
	ctx := context.Background()
	board := swarm.NewMemoryBlackboard()

	// Only 1 task, 3 competing agents.
	if err := board.AddTask(ctx, swarm.Task{ID: "solo", Description: "Only task"}); err != nil {
		t.Fatal(err)
	}

	agents := []agent.Agent{
		agent.New("a1", "Agent 1", "gpt-4", &mockProvider{msg: "done"}, nil),
		agent.New("a2", "Agent 2", "gpt-4", &mockProvider{msg: "done"}, nil),
		agent.New("a3", "Agent 3", "gpt-4", &mockProvider{msg: "done"}, nil),
	}

	s := swarm.New(board, 3, agents...)
	sess := session.New("no-double-claim")

	result, err := s.Run(ctx, sess)
	if err != nil {
		t.Fatalf("swarm.Run: %v", err)
	}

	// Exactly 1 task should have been worked on.
	if result.TasksDone != 1 {
		t.Errorf("expected exactly 1 task done, got %d", result.TasksDone)
	}

	// The task must be claimed by exactly one agent.
	remaining, _ := board.ListUnclaimed(ctx)
	if len(remaining) != 0 {
		t.Errorf("expected 0 unclaimed tasks remaining, got %d", len(remaining))
	}
}
