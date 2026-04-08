// Package pool provides a bounded worker pool for the Slate orchestrator pattern:
// dispatch N tasks to M agents in parallel, collect distilled episodes.
package pool

import (
	"context"
	"fmt"
	"sync"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/session"
)

// Task is a unit of work dispatched to a pool agent.
type Task struct {
	ID   string
	Data any
}

// Result is the outcome of a single task.
type Result struct {
	Task    Task
	Episode *session.Episode
	Err     error
}

// Run dispatches tasks to a bounded pool of workers. Each task gets its own
// fresh in-memory session. agentFn is called once per task to produce the
// agent for that task (allowing per-task configuration).
//
// workers caps concurrency; defaults to len(tasks) if <= 0.
// Results are returned in the same order as tasks.
func Run(
	ctx context.Context,
	tasks []Task,
	workers int,
	agentFn func(task Task) agent.Agent,
) []Result {
	if len(tasks) == 0 {
		return nil
	}
	if workers <= 0 || workers > len(tasks) {
		workers = len(tasks)
	}

	results := make([]Result, len(tasks))
	queue := make(chan int, len(tasks))
	for i := range tasks {
		queue <- i
	}
	close(queue)

	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for idx := range queue {
				if ctx.Err() != nil {
					results[idx] = Result{Task: tasks[idx], Err: ctx.Err()}
					continue
				}
				task := tasks[idx]
				results[idx] = run(ctx, task, agentFn(task))
			}
		}()
	}
	wg.Wait()

	return results
}

func run(ctx context.Context, task Task, a agent.Agent) Result {
	sess := session.New(task.ID)

	turnRes, err := a.Turn(ctx, sess)
	if err != nil {
		return Result{Task: task, Err: err}
	}
	if turnRes.TurnStopReason.StopsProgress() {
		return Result{
			Task: task,
			Err: fmt.Errorf(
				"turn for task %q stopped with turn stop state %s",
				task.ID,
				turnRes.TurnStopReason,
			),
		}
	}

	traj, err := session.ExportRun(sess)
	if err != nil {
		return Result{Task: task, Err: err}
	}
	traj.AgentID = a.ID()

	ep := session.Distill(traj)
	return Result{Task: task, Episode: ep}
}
