package eval

import (
	"context"
	"fmt"
	"sync"

	"github.com/oklog/ulid/v2"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// AgentFactory constructs a fresh agent for a task/run pair.
//
// Runner may call the factory concurrently, so implementations should return
// independent agents or otherwise guarantee concurrency safety.
type AgentFactory func(task Task, runIndex int) agent.Agent

// RunResult captures one repeated execution of a task.
type RunResult struct {
	RunID         string
	TaskID        string
	EnvironmentID string
	RunIndex      int
	AgentID       string
	Session       *session.Session
	StepResult    agent.StepResult
	Err           error
}

// Runner executes a task one or more times.
type Runner interface {
	Run(ctx context.Context, task Task, runs int) ([]RunResult, error)
}

// ParallelRunner executes repeated task runs in parallel with a bounded worker pool.
type ParallelRunner struct {
	Workers int
	AgentFn AgentFactory
}

// NewParallelRunner constructs a ParallelRunner.
func NewParallelRunner(workers int, agentFn AgentFactory) *ParallelRunner {
	return &ParallelRunner{
		Workers: workers,
		AgentFn: agentFn,
	}
}

// Run executes the task repeated runs times and returns ordered results.
func (r *ParallelRunner) Run(ctx context.Context, task Task, runs int) ([]RunResult, error) {
	if r == nil {
		return nil, fmt.Errorf("eval: nil runner")
	}
	if task == nil {
		return nil, fmt.Errorf("eval: nil task")
	}
	if r.AgentFn == nil {
		return nil, fmt.Errorf("eval: nil agent factory")
	}
	if runs <= 0 {
		return nil, fmt.Errorf("eval: runs must be > 0")
	}

	workers := r.Workers
	if workers <= 0 || workers > runs {
		workers = runs
	}

	results := make([]RunResult, runs)
	queue := make(chan int, runs)
	for idx := range runs {
		queue <- idx
	}
	close(queue)

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for runIndex := range queue {
				results[runIndex] = runTask(ctx, task, runIndex, r.AgentFn)
			}
		})
	}
	wg.Wait()

	return results, nil
}

func runTask(ctx context.Context, task Task, runIndex int, agentFn AgentFactory) RunResult {
	result := RunResult{
		RunIndex: runIndex,
		TaskID:   task.ID(),
	}

	if ctx.Err() != nil {
		result.Err = ctx.Err()
		return result
	}

	env := task.Environment()
	if env != nil {
		result.EnvironmentID = env.ID()
	}

	sessionID := ulid.Make().String()
	result.RunID = sessionID
	sess := session.New(sessionID)
	result.Session = sess

	if env != nil {
		if err := env.Bootstrap(ctx, sess); err != nil {
			result.Err = err
			return result
		}
	}

	instruction := task.Instruction()
	if instruction != "" {
		if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.MessageAdded, llm.Message{
			Role:    llm.RoleUser,
			Content: instruction,
		})); err != nil {
			result.Err = err
			return result
		}
	}

	a := agentFn(task, runIndex)
	if a == nil {
		result.Err = fmt.Errorf(
			"eval: agent factory returned nil for task %q run %d",
			task.ID(),
			runIndex,
		)
		return result
	}
	result.AgentID = a.ID()

	result.StepResult, result.Err = a.Turn(ctx, sess)
	return result
}
