package eval_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/x/eval"
	xtest "github.com/nijaru/canto/x/testing"
)

func TestPlanAdherence(t *testing.T) {
	sess := buildSession("sess-plan", []struct {
		assistantMsg string
		toolName     string
		cost         float64
	}{
		{"I will search first.", "search", 0.001},
		{"Plan followed.", "", 0.0005},
	})

	traj, err := session.ExportRun(sess)
	if err != nil {
		t.Fatalf("ExportRun: %v", err)
	}

	mock := xtest.NewMockProvider("judge", xtest.Step{
		Content: "Looks good.\nScore: 0.8/1.0",
	})
	scorer := &eval.PlanAdherence{
		NameText: "plan_adherence",
		Criteria: "Stay on the provided plan and complete the task directly.",
		Model:    "judge-model",
		Provider: mock,
	}

	score, err := scorer.ScoreRun(context.Background(), traj)
	if err != nil {
		t.Fatalf("ScoreRun: %v", err)
	}
	if score != 0.8 {
		t.Fatalf("score = %f, want 0.8", score)
	}

	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(calls))
	}
	req := calls[0]
	if req.Model != "judge-model" {
		t.Fatalf("model = %q, want judge-model", req.Model)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(req.Messages))
	}
	if got := req.Messages[0].Content; !strings.Contains(got, "### Trajectory") ||
		!strings.Contains(got, "Stay on the provided plan") {
		t.Fatalf("prompt missing expected content: %q", got)
	}
}
