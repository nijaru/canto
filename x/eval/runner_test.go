package eval_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/x/eval"
	xtest "github.com/nijaru/canto/x/testing"
)

func TestParallelRunnerRun(t *testing.T) {
	task := eval.TaskSpec{
		TaskID:          "task-1",
		InstructionText: "solve the task",
		Env: eval.StaticEnvironment{
			EnvironmentID: "env-1",
			Context: []session.ContextEntry{
				{Content: "environment ready"},
			},
		},
	}

	runner := eval.NewParallelRunner(2, func(task eval.Task, runIndex int) agent.Agent {
		provider := xtest.NewMockProvider(
			fmt.Sprintf("provider-%d", runIndex),
			xtest.Step{Content: fmt.Sprintf("run-%d", runIndex)},
		)
		return agent.New(
			fmt.Sprintf("agent-%d", runIndex),
			"",
			"mock-model",
			provider,
			nil,
		)
	})

	results, err := runner.Run(context.Background(), task, 4)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("results len = %d, want 4", len(results))
	}

	for i, result := range results {
		if result.Err != nil {
			t.Fatalf("result[%d] unexpected error: %v", i, result.Err)
		}
		if result.TaskID != task.ID() {
			t.Fatalf("result[%d].TaskID = %q, want %q", i, result.TaskID, task.ID())
		}
		if result.EnvironmentID != "env-1" {
			t.Fatalf("result[%d].EnvironmentID = %q, want env-1", i, result.EnvironmentID)
		}
		if result.RunIndex != i {
			t.Fatalf("result[%d].RunIndex = %d, want %d", i, result.RunIndex, i)
		}
		if result.AgentID != fmt.Sprintf("agent-%d", i) {
			t.Fatalf("result[%d].AgentID = %q, want agent-%d", i, result.AgentID, i)
		}
		if result.Session == nil {
			t.Fatalf("result[%d].Session is nil", i)
		}
		if result.StepResult.Content != fmt.Sprintf("run-%d", i) {
			t.Fatalf(
				"result[%d].StepResult.Content = %q, want run-%d",
				i,
				result.StepResult.Content,
				i,
			)
		}

		traj, err := session.ExportRun(result.Session)
		if err != nil {
			t.Fatalf("result[%d]: ExportRun: %v", i, err)
		}
		if len(traj.Turns) != 1 {
			t.Fatalf("result[%d]: turn count = %d, want 1", i, len(traj.Turns))
		}
		input := traj.Turns[0].Input
		if len(input) != 2 {
			t.Fatalf("result[%d]: input len = %d, want 2", i, len(input))
		}
		if input[0].Role != llm.RoleUser || input[0].Content != "environment ready" {
			t.Fatalf("result[%d]: env seed = %+v, want user environment ready", i, input[0])
		}
		entry := traj.Turns[0].InputEntries[0]
		if entry.EventType != session.ContextAdded ||
			entry.ContextKind != session.ContextKindHarness {
			t.Fatalf("result[%d]: env entry = %+v, want harness context", i, entry)
		}
		if input[1].Role != llm.RoleUser || input[1].Content != "solve the task" {
			t.Fatalf("result[%d]: task seed = %+v, want user solve the task", i, input[1])
		}
	}
}
