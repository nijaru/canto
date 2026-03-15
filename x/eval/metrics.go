package eval

import (
	"context"

	"github.com/nijaru/canto/session"
)

// ToolCallAccuracyScorer checks whether the tool calls in a turn match
// a set of expected tool names. Score = fraction of expected tools called.
// Score is 1.0 if no expected tools are configured (unconstrained turns pass).
type ToolCallAccuracyScorer struct {
	// Expected is the set of tool names that should appear in the turn.
	// If empty, every turn scores 1.0.
	Expected []string
}

func (s *ToolCallAccuracyScorer) Name() string { return "tool_call_accuracy" }

func (s *ToolCallAccuracyScorer) Score(_ context.Context, turn session.TrajectoryTurn) (float64, error) {
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

// CostEfficiencyScorer rewards low-cost turns.
// Score = 1 / (1 + turn.Cost). Approaches 1 as cost → 0, approaches 0 as cost → ∞.
type CostEfficiencyScorer struct{}

func (s *CostEfficiencyScorer) Name() string { return "cost_efficiency" }

func (s *CostEfficiencyScorer) Score(_ context.Context, turn session.TrajectoryTurn) (float64, error) {
	return 1.0 / (1.0 + turn.Cost), nil
}

// TurnCountScorer penalises trajectories that take more steps.
// Since it operates per-turn, it scores based on the number of tool calls
// in the turn: score = 1 / (1 + len(tool_calls)). A turn with no tool
// calls scores 1.0 (direct answer — most efficient).
type TurnCountScorer struct{}

func (s *TurnCountScorer) Name() string { return "turn_efficiency" }

func (s *TurnCountScorer) Score(_ context.Context, turn session.TrajectoryTurn) (float64, error) {
	return 1.0 / (1.0 + float64(len(turn.ToolCalls))), nil
}
