package pool

import (
	"context"
	"errors"
	"testing"

	"github.com/nijaru/canto/agent"
	xtest "github.com/nijaru/canto/x/testing"
)

func TestRun_Empty(t *testing.T) {
	results := Run(context.Background(), nil, 2, func(_ Task) agent.Agent { return nil })
	if results != nil {
		t.Fatalf("expected nil, got %v", results)
	}
}

func TestRun_ResultsInOrder(t *testing.T) {
	tasks := []Task{
		{ID: "t1"}, {ID: "t2"}, {ID: "t3"},
	}

	results := Run(context.Background(), tasks, 2, func(task Task) agent.Agent {
		p := xtest.NewMockProvider("mock", xtest.Step{Content: "result: " + task.ID})
		return agent.New(task.ID, "", "m", p, nil)
	})

	if len(results) != len(tasks) {
		t.Fatalf("results len = %d, want %d", len(results), len(tasks))
	}
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("task %d: unexpected error: %v", i, r.Err)
		}
		if r.Task.ID != tasks[i].ID {
			t.Errorf("result[%d].Task.ID = %q, want %q", i, r.Task.ID, tasks[i].ID)
		}
		if r.Episode == nil {
			t.Errorf("result[%d]: expected non-nil episode", i)
		}
	}
}

func TestRun_WorkerCap(t *testing.T) {
	// 5 tasks, 1 worker — all should still complete.
	tasks := make([]Task, 5)
	for i := range tasks {
		tasks[i] = Task{ID: "t"}
	}

	results := Run(context.Background(), tasks, 1, func(task Task) agent.Agent {
		p := xtest.NewMockProvider("mock", xtest.Step{Content: "ok"})
		return agent.New(task.ID, "", "m", p, nil)
	})

	if len(results) != 5 {
		t.Fatalf("results len = %d, want 5", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("unexpected error: %v", r.Err)
		}
	}
}

func TestRun_AgentError(t *testing.T) {
	tasks := []Task{{ID: "fail"}}
	want := errors.New("provider down")

	results := Run(context.Background(), tasks, 1, func(task Task) agent.Agent {
		p := xtest.NewMockProvider("mock", xtest.Step{Err: want})
		return agent.New(task.ID, "", "m", p, nil)
	})

	if len(results) != 1 {
		t.Fatalf("results len = %d", len(results))
	}
	if !errors.Is(results[0].Err, want) {
		t.Fatalf("Err = %v, want %v", results[0].Err, want)
	}
	if results[0].Episode != nil {
		t.Fatal("expected nil episode on error")
	}
}

func TestRun_WorkersDefaultsToTaskCount(t *testing.T) {
	tasks := []Task{{ID: "a"}, {ID: "b"}}

	// workers=0 → should default to len(tasks)=2
	results := Run(context.Background(), tasks, 0, func(task Task) agent.Agent {
		p := xtest.NewMockProvider("mock", xtest.Step{Content: "ok"})
		return agent.New(task.ID, "", "m", p, nil)
	})

	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
}
