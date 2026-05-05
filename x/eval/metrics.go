package eval

import (
	"context"

	"github.com/nijaru/canto/session"
)

// ToolCorrectness scores how accurately a turn used the expected tools.
// If Expected is empty, every turn scores 1.0.
type ToolCorrectness struct {
	// Expected is the set of tool names that should appear in the turn.
	Expected []string
}

// Name returns the scorer identifier.
func (s *ToolCorrectness) Name() string { return "tool_correctness" }

// ScoreTurn returns an F1-style score over expected vs actual tool names.
func (s *ToolCorrectness) ScoreTurn(
	_ context.Context,
	turn session.RunTurn,
) (float64, error) {
	if len(s.Expected) == 0 {
		return 1.0, nil
	}

	expected := make(map[string]struct{}, len(s.Expected))
	for _, name := range s.Expected {
		expected[name] = struct{}{}
	}

	called := make(map[string]struct{}, len(turn.ToolCalls))
	for _, tc := range turn.ToolCalls {
		called[tc.Function.Name] = struct{}{}
	}

	var hits float64
	for name := range expected {
		if _, ok := called[name]; ok {
			hits++
		}
	}
	if hits == 0 {
		return 0, nil
	}

	precision := hits / float64(len(called))
	recall := hits / float64(len(expected))
	return 2 * precision * recall / (precision + recall), nil
}

// StepEfficiency rewards turns that reach completion with fewer tool calls.
type StepEfficiency struct{}

// Name returns the scorer identifier.
func (s *StepEfficiency) Name() string { return "step_efficiency" }

// ScoreTurn penalises turns that require more tool calls.
func (s *StepEfficiency) ScoreTurn(_ context.Context, turn session.RunTurn) (float64, error) {
	return 1.0 / (1.0 + float64(len(turn.ToolCalls))), nil
}

// CostEfficiency rewards low-cost turns.
type CostEfficiency struct{}

// Name returns the scorer identifier.
func (s *CostEfficiency) Name() string { return "cost_efficiency" }

// ScoreTurn rewards lower-cost turns.
func (s *CostEfficiency) ScoreTurn(
	_ context.Context,
	turn session.RunTurn,
) (float64, error) {
	return 1.0 / (1.0 + turn.Cost), nil
}
