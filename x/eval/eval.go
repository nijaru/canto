// Package eval provides an evaluation harness for scoring agent trajectories.
//
// RunLogs are exported from session event logs via session.ExportRun.
// Evaluators are pure functions over individual turns; RunEval collects per-run
// score maps and writes results to a JSONL file for offline analysis.
package eval

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/session"
)

// TurnEvaluator evaluates a single transcript turn and returns a score in [0, 1].
type TurnEvaluator interface {
	Name() string
	ScoreTurn(ctx context.Context, turn session.RunTurn) (float64, error)
}

// RunEvaluator evaluates an entire RunLog and returns a score in [0, 1].
type RunEvaluator interface {
	Name() string
	ScoreRun(ctx context.Context, log *session.RunLog) (float64, error)
}

// EvalResult is the scored output for a single run transcript.
type EvalResult struct {
	RunID     string             `json:"run_id"`
	AgentID   string             `json:"agent_id"`
	ScoredAt  time.Time          `json:"scored_at"`
	TurnCount int                `json:"turn_count"`
	TotalCost float64            `json:"total_cost"`
	Scores    map[string]float64 `json:"scores"` // evaluator name → mean score across turns
	Metadata  map[string]any     `json:"metadata,omitzero"`
}

// Options defines a collection of evaluators and configuration for an evaluation run.
type Options struct {
	TurnEvals   []TurnEvaluator
	RunEvals    []RunEvaluator
	OutputPath  string // Path to write JSONL results
	Concurrency int    // Number of parallel workers
}

// Run exports a RunLog from each session, scores every turn with each
// TurnEvaluator, and scores each log with each RunEvaluator.
func Run(
	ctx context.Context,
	sessions []*session.Session,
	opts Options,
) ([]EvalResult, error) {
	if len(opts.TurnEvals) == 0 && len(opts.RunEvals) == 0 {
		return nil, fmt.Errorf("eval: no evaluators provided")
	}

	workers := opts.Concurrency
	if workers <= 0 {
		workers = 10 // Default concurrency
	}

	type task struct {
		sess *session.Session
		idx  int
	}

	results := make([]EvalResult, len(sessions))
	taskCh := make(chan task, len(sessions))
	errCh := make(chan error, 1)

	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Go(func() {
			for t := range taskCh {
				traj, err := session.ExportRun(t.sess)
				if err != nil {
					select {
					case errCh <- fmt.Errorf("eval: export run %q: %w", t.sess.ID(), err):
					default:
					}
					return
				}

				res := EvalResult{
					RunID:     t.sess.ID(),
					AgentID:   traj.AgentID,
					ScoredAt:  time.Now().UTC(),
					TurnCount: len(traj.Turns),
					TotalCost: traj.TotalCost,
					Scores:    make(map[string]float64, len(opts.TurnEvals)+len(opts.RunEvals)),
				}

				// Score transcript level
				for _, re := range opts.RunEvals {
					s, err := re.ScoreRun(ctx, traj)
					if err != nil {
						select {
						case errCh <- fmt.Errorf("eval: run_eval %q on %q: %w", re.Name(), t.sess.ID(), err):
						default:
						}
						return
					}
					res.Scores[re.Name()] = s
				}

				// Score turns
				for _, te := range opts.TurnEvals {
					if len(traj.Turns) == 0 {
						res.Scores[te.Name()] = 0
						continue
					}
					var sum float64
					for _, turn := range traj.Turns {
						s, err := te.ScoreTurn(ctx, turn)
						if err != nil {
							select {
							case errCh <- fmt.Errorf("eval: turn_eval %q on %q turn %q: %w", te.Name(), t.sess.ID(), turn.TurnID, err):
							default:
							}
							return
						}
						sum += s
					}
					res.Scores[te.Name()] = sum / float64(len(traj.Turns))
				}
				results[t.idx] = res
			}
		})
	}

	for i, sess := range sessions {
		taskCh <- task{sess: sess, idx: i}
	}
	close(taskCh)

	wg.Wait()

	select {
	case err := <-errCh:
		return nil, err
	default:
	}

	// Write results to JSONL if output path provided
	if opts.OutputPath != "" {
		f, err := os.Create(opts.OutputPath)
		if err != nil {
			return results, fmt.Errorf("eval: create output %q: %w", opts.OutputPath, err)
		}
		defer f.Close()

		w := bufio.NewWriter(f)
		for _, res := range results {
			if err := json.MarshalWrite(w, res); err != nil {
				return results, fmt.Errorf("eval: write result: %w", err)
			}
			_ = w.WriteByte('\n')
		}

		if err := w.Flush(); err != nil {
			return results, fmt.Errorf("eval: flush output: %w", err)
		}
	}
	return results, nil
}
