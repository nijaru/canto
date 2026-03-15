package session

import (
	"encoding/json"
	"time"

	"github.com/nijaru/canto/llm"
)

// Trajectory represents a structured trace of an agent's execution.
// It is used for evaluation, reinforcement learning (RL) fine-tuning,
// and offline analysis.
type Trajectory struct {
	SessionID string           `json:"session_id"`
	AgentID   string           `json:"agent_id"`
	StartTime time.Time        `json:"start_time"`
	EndTime   time.Time        `json:"end_time"`
	Turns     []TrajectoryTurn `json:"turns"`
	TotalCost float64          `json:"total_cost"`
	Metadata  map[string]any   `json:"metadata,omitempty"`
}

// TrajectoryTurn represents a single perceive-decide-act-observe loop.
type TrajectoryTurn struct {
	TurnID      string         `json:"turn_id"`
	Timestamp   time.Time      `json:"timestamp"`
	Input       []llm.Message  `json:"input"`
	Output      llm.Message    `json:"output"`
	ToolCalls   []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolResults []llm.Message  `json:"tool_results,omitempty"`
	Cost        float64        `json:"cost"`
	Metrics     map[string]any `json:"metrics,omitempty"`
}

// ExportTrajectory converts a session's event log into a structured Trajectory.
func ExportTrajectory(sess *Session) (*Trajectory, error) {
	events := sess.Events()
	if len(events) == 0 {
		return &Trajectory{
			SessionID: sess.ID(),
			Turns:     []TrajectoryTurn{},
		}, nil
	}

	traj := &Trajectory{
		SessionID: sess.ID(),
		StartTime: events[0].Timestamp,
		EndTime:   events[len(events)-1].Timestamp,
		Metadata:  make(map[string]any),
	}

	var currentTurn *TrajectoryTurn
	var inputBuffer []llm.Message

	for _, e := range events {
		traj.TotalCost += e.Cost

		switch e.Type {
		case EventTypeMessageAdded:
			var msg llm.Message
			if err := json.Unmarshal(e.Data, &msg); err != nil {
				continue
			}

			if msg.Role == llm.RoleUser || msg.Role == llm.RoleSystem {
				inputBuffer = append(inputBuffer, msg)
			} else if msg.Role == llm.RoleAssistant {
				if currentTurn != nil {
					traj.Turns = append(traj.Turns, *currentTurn)
				}
				currentTurn = &TrajectoryTurn{
					TurnID:    e.ID.String(),
					Timestamp: e.Timestamp,
					Input:     make([]llm.Message, len(inputBuffer)),
					Output:    msg,
					ToolCalls: msg.Calls,
					Cost:      e.Cost,
				}
				copy(currentTurn.Input, inputBuffer)
				inputBuffer = nil // Reset input for next turn
			} else if msg.Role == llm.RoleTool && currentTurn != nil {
				currentTurn.ToolResults = append(currentTurn.ToolResults, msg)
			}
		}
	}

	if currentTurn != nil {
		traj.Turns = append(traj.Turns, *currentTurn)
	}

	return traj, nil
}
