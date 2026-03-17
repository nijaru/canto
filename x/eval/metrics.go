package eval

import (
	"context"

	"github.com/nijaru/canto/session"
)

// ToolCallAccuracy checks whether the tool calls in a turn match
// a set of expected tool names. Score = fraction of expected tools called.
type ToolCallAccuracy struct {
	// Expected is the set of tool names that should appear in the turn.
	// If empty, every turn scores 1.0.
	Expected []string
}

func (s *ToolCallAccuracy) Name() string { return "tool_call_accuracy" }

func (s *ToolCallAccuracy) ScoreTurn(
	_ context.Context,
	turn session.RunTurn,
) (float64, error) {
	if len(s.Expected) == 0 {
		return 1.0, nil
	}
	called := make(map[string]bool, len(turn.ToolCalls))
	for _, tc := range turn.ToolCalls {
		called[tc.Function.Name] = true
	}
	var hits float64
	for _, name := range s.Expected {
		if called[name] {
			hits++
		}
	}
	return hits / float64(len(s.Expected)), nil
}

// CostEfficiency rewards low-cost turns.
type CostEfficiency struct{}

func (s *CostEfficiency) Name() string { return "cost_efficiency" }

func (s *CostEfficiency) ScoreTurn(
	_ context.Context,
	turn session.RunTurn,
) (float64, error) {
	return 1.0 / (1.0 + turn.Cost), nil
}

// TurnEfficiency penalises trajectories that take more steps.
type TurnEfficiency struct{}

func (s *TurnEfficiency) Name() string { return "turn_efficiency" }

func (s *TurnEfficiency) ScoreTurn(_ context.Context, turn session.RunTurn) (float64, error) {
	return 1.0 / (1.0 + float64(len(turn.ToolCalls))), nil
}
