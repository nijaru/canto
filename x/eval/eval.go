// Package eval provides an evaluation harness for scoring agent trajectories.
//
// Trajectories are exported from session event logs via session.ExportTrajectory.
// Scorers are pure functions over individual turns; RunEval collects per-trajectory
// score maps and writes results to a JSONL file for offline analysis.
package eval

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/session"
)

// Scorer evaluates a single trajectory turn and returns a score in [0, 1].
// Implementations must be pure (no side effects on the session or trajectory).
type Scorer interface {
	Name() string
	Score(ctx context.Context, turn session.TrajectoryTurn) (float64, error)
}

// EvalResult is the scored output for a single trajectory.
type EvalResult struct {
	TrajectoryID string             `json:"trajectory_id"`
	AgentID      string             `json:"agent_id"`
	ScoredAt     time.Time          `json:"scored_at"`
	TurnCount    int                `json:"turn_count"`
	TotalCost    float64            `json:"total_cost"`
	Scores       map[string]float64 `json:"scores"` // scorer name → mean score across turns
	Metadata     map[string]any     `json:"metadata,omitzero"`
}

// RunEval exports a Trajectory from each session, scores every turn with each
// Scorer, and writes the aggregated EvalResults to outPath as JSONL.
// Returns the full slice of results (for in-process inspection) and any error.
func RunEval(
	ctx context.Context,
	sessions []*session.Session,
	scorers []Scorer,
	outPath string,
) ([]EvalResult, error) {
	if len(scorers) == 0 {
		return nil, fmt.Errorf("eval: no scorers provided")
	}

	f, err := os.Create(outPath)
	if err != nil {
		return nil, fmt.Errorf("eval: create output %q: %w", outPath, err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)

	var results []EvalResult

	for _, sess := range sessions {
		if err := ctx.Err(); err != nil {
			return results, err
		}

		traj, err := session.ExportTrajectory(sess)
		if err != nil {
			return results, fmt.Errorf("eval: export trajectory %q: %w", sess.ID(), err)
		}

		result := EvalResult{
			TrajectoryID: sess.ID(),
			AgentID:      traj.AgentID,
			ScoredAt:     time.Now().UTC(),
			TurnCount:    len(traj.Turns),
			TotalCost:    traj.TotalCost,
			Scores:       make(map[string]float64, len(scorers)),
		}

		// Score each turn with each scorer; accumulate mean per scorer.
		for _, scorer := range scorers {
			if len(traj.Turns) == 0 {
				result.Scores[scorer.Name()] = 0
				continue
			}
			var sum float64
			for _, turn := range traj.Turns {
				s, err := scorer.Score(ctx, turn)
				if err != nil {
					return results, fmt.Errorf(
						"eval: scorer %q on trajectory %q turn %q: %w",
						scorer.Name(), sess.ID(), turn.TurnID, err,
					)
				}
				sum += s
			}
			result.Scores[scorer.Name()] = sum / float64(len(traj.Turns))
		}

		results = append(results, result)
		if err := json.MarshalWrite(w, result); err != nil {
			return results, fmt.Errorf("eval: write result: %w", err)
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return results, fmt.Errorf("eval: write newline: %w", err)
		}
	}

	if err := w.Flush(); err != nil {
		return results, fmt.Errorf("eval: flush output: %w", err)
	}
	return results, nil
}
