package session

import (
	"testing"
	"time"

	"github.com/nijaru/canto/llm"
)

func TestExportTrajectory(t *testing.T) {
	sess := New("test-session")

	// Add some events
	now := time.Now()

	// User message
	e1 := NewEvent(sess.ID(), EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Hello",
	})
	e1.Timestamp = now
	e1.Cost = 0.01
	sess.Append(e1)

	// Assistant response with tool call
	e2 := NewEvent(sess.ID(), EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleAssistant,
		Content: "Let me check",
		Calls: []llm.ToolCall{
			{ID: "call_1", Type: "function", Function: struct {
				Name      string "json:\"name\""
				Arguments string "json:\"arguments\""
			}{Name: "search", Arguments: "{}"}},
		},
	})
	e2.Timestamp = now.Add(time.Second)
	e2.Cost = 0.05
	sess.Append(e2)

	// Tool result
	e3 := NewEvent(sess.ID(), EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleTool,
		Content: "Result data",
		ToolID:  "call_1",
		Name:    "search",
	})
	e3.Timestamp = now.Add(2 * time.Second)
	sess.Append(e3)

	// Second assistant response
	e4 := NewEvent(sess.ID(), EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleAssistant,
		Content: "The result is data",
	})
	e4.Timestamp = now.Add(3 * time.Second)
	e4.Cost = 0.02
	sess.Append(e4)

	traj, err := ExportTrajectory(sess)
	if err != nil {
		t.Fatal(err)
	}

	if traj.SessionID != "test-session" {
		t.Errorf("expected session test-session, got %s", traj.SessionID)
	}
	if traj.TotalCost != 0.08 {
		t.Errorf("expected cost 0.08, got %f", traj.TotalCost)
	}

	if len(traj.Turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(traj.Turns))
	}

	turn1 := traj.Turns[0]
	if len(turn1.Input) != 1 || turn1.Input[0].Role != llm.RoleUser {
		t.Errorf("expected turn 1 input to be user message")
	}
	if turn1.Output.Role != llm.RoleAssistant || len(turn1.ToolCalls) != 1 {
		t.Errorf("expected turn 1 output to be assistant with 1 tool call")
	}
	if len(turn1.ToolResults) != 1 || turn1.ToolResults[0].Content != "Result data" {
		t.Errorf("expected turn 1 to have 1 tool result")
	}

	turn2 := traj.Turns[1]
	if len(turn2.Input) != 0 {
		t.Errorf("expected turn 2 input to be empty (carried over from tool result)")
	}
	if turn2.Output.Role != llm.RoleAssistant || turn2.Output.Content != "The result is data" {
		t.Errorf("expected turn 2 output to be final assistant message")
	}
}
