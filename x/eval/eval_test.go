package eval_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/x/eval"
)

// buildSession creates a session with a known sequence of events for testing.
func buildSession(id string, turns []struct {
	assistantMsg string
	toolName     string
	cost         float64
},
) *session.Session {
	sess := session.New(id)
	sess.Append(session.NewEvent(id, session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Do the task",
	}))
	for _, t := range turns {
		calls := []llm.ToolCall{}
		if t.toolName != "" {
			calls = append(calls, llm.ToolCall{
				ID:   "call-1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: t.toolName, Arguments: "{}"},
			})
		}
		e := session.NewEvent(id, session.EventTypeMessageAdded, llm.Message{
			Role:    llm.RoleAssistant,
			Content: t.assistantMsg,
			Calls:   calls,
		})
		e.Cost = t.cost
		sess.Append(e)
	}
	return sess
}

func TestRunEval(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "results.jsonl")

	sess1 := buildSession("sess-1", []struct {
		assistantMsg string
		toolName     string
		cost         float64
	}{
		{"I'll search for that.", "search", 0.001},
		{"Here's the result.", "", 0.0005},
	})

	sess2 := buildSession("sess-2", []struct {
		assistantMsg string
		toolName     string
		cost         float64
	}{
		{"Done directly.", "", 0.0002},
	})

	scorers := []eval.Scorer{
		&eval.ToolCallAccuracyScorer{Expected: []string{"search"}},
		&eval.CostEfficiencyScorer{},
		&eval.TurnCountScorer{},
	}

	results, err := eval.RunEval(
		context.Background(),
		[]*session.Session{sess1, sess2},
		scorers,
		outPath,
	)
	if err != nil {
		t.Fatalf("RunEval: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// sess-1: 2 turns, search called in turn 1 → accuracy = (1+0)/2 = 0.5
	acc1 := results[0].Scores["tool_call_accuracy"]
	if acc1 != 0.5 {
		t.Errorf("sess-1 tool_call_accuracy: expected 0.5, got %f", acc1)
	}

	// sess-2: 1 turn, no search → accuracy = 0.0
	acc2 := results[1].Scores["tool_call_accuracy"]
	if acc2 != 0.0 {
		t.Errorf("sess-2 tool_call_accuracy: expected 0.0, got %f", acc2)
	}

	// Verify JSONL output is well-formed and contains 2 lines.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	lines := splitNonEmpty(data)
	if len(lines) != 2 {
		t.Errorf("expected 2 JSONL lines, got %d", len(lines))
	}
	for i, line := range lines {
		var r eval.EvalResult
		if err := json.Unmarshal(line, &r); err != nil {
			t.Errorf("line %d: unmarshal: %v", i, err)
		}
		if len(r.Scores) != 3 {
			t.Errorf("line %d: expected 3 scores, got %d", i, len(r.Scores))
		}
	}
}

func splitNonEmpty(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
