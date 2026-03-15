package swarm

import (
	"context"
	"fmt"
	"sync"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/session"
)

// SwarmResult summarises a completed swarm run.
type SwarmResult struct {
	Rounds    int
	TasksDone int
	TotalCost float64
}

// Swarm coordinates a set of agents via a shared Blackboard.
// Each round, agents concurrently claim and execute unclaimed tasks.
// The swarm finishes when no unclaimed tasks remain or MaxRounds is reached.
//
// All agents share a single session so their tool calls and outputs are
// visible to each other through the event log. If isolation is needed,
// callers should manage separate sessions and aggregate results manually.
type Swarm struct {
	agents     []*agent.Agent
	blackboard Blackboard
	maxRounds  int
}

// New creates a Swarm with the given agents and blackboard.
// maxRounds is a hard cap on coordination rounds (prevents infinite loops).
func New(blackboard Blackboard, maxRounds int, agents ...*agent.Agent) *Swarm {
	return &Swarm{
		agents:     agents,
		blackboard: blackboard,
		maxRounds:  maxRounds,
	}
}

// Run executes the swarm. Each round:
//  1. List unclaimed tasks on the blackboard.
//  2. If none remain, stop.
//  3. Each agent concurrently attempts to claim one unclaimed task. Agents
//     that win the claim run one Turn on the shared session.
//  4. Repeat for the next round.
//
// Returns SwarmResult summarising total rounds and tasks completed.
func (s *Swarm) Run(ctx context.Context, sess *session.Session) (SwarmResult, error) {
	var result SwarmResult

	for round := range s.maxRounds {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		unclaimed, err := s.blackboard.ListUnclaimed(ctx)
		if err != nil {
			return result, fmt.Errorf("swarm round %d: list: %w", round, err)
		}
		if len(unclaimed) == 0 {
			break // all tasks done
		}

		// Each agent tries to claim one unclaimed task, then runs a Turn.
		// Agents operate concurrently; claiming is atomic so there's no
		// double-assignment even under races.
		type outcome struct {
			agentID string
			err     error
			worked  bool // true if the agent claimed and ran a task
		}

		outcomes := make([]outcome, len(s.agents))
		var wg sync.WaitGroup

		// Distribute unclaimed tasks round-robin as hints, but let agents
		// actually claim via the atomic ClaimTask. An agent whose hint was
		// stolen simply produces worked=false for this round.
		for i, a := range s.agents {
			wg.Add(1)
			go func(idx int, ag *agent.Agent, tasks []Task) {
				defer wg.Done()
				out := outcome{agentID: ag.ID}

				// Try each task in order until one is successfully claimed.
				var claimed *Task
				for j := range tasks {
					ok, claimErr := s.blackboard.ClaimTask(ctx, ag.ID, tasks[j].ID)
					if claimErr != nil {
						out.err = fmt.Errorf("claim %q: %w", tasks[j].ID, claimErr)
						outcomes[idx] = out
						return
					}
					if ok {
						t := tasks[j]
						claimed = &t
						break
					}
				}
				if claimed == nil {
					// All tasks stolen by other agents this round — that's fine.
					outcomes[idx] = out
					return
				}

				// Post the claimed task description to the blackboard so other
				// agents can observe what this agent is working on.
				_ = s.blackboard.Post(ctx, ag.ID, "current_task", claimed.Description)

				// Execute one agent turn on the shared session.
				if _, turnErr := ag.Turn(ctx, sess); turnErr != nil {
					out.err = fmt.Errorf("turn for task %q: %w", claimed.ID, turnErr)
					outcomes[idx] = out
					return
				}
				out.worked = true
				outcomes[idx] = out
			}(i, a, unclaimed)
		}
		wg.Wait()

		// Collect errors and tally completions.
		for _, o := range outcomes {
			if o.err != nil {
				return result, fmt.Errorf("swarm round %d agent %q: %w", round, o.agentID, o.err)
			}
			if o.worked {
				result.TasksDone++
			}
		}
		result.Rounds++
		result.TotalCost += sess.TotalCost()
	}

	// Verify all tasks are actually done.
	remaining, err := s.blackboard.ListUnclaimed(ctx)
	if err != nil {
		return result, fmt.Errorf("swarm: final check: %w", err)
	}
	if len(remaining) > 0 && result.Rounds >= s.maxRounds {
		return result, fmt.Errorf("swarm: reached MaxRounds (%d) with %d tasks unclaimed", s.maxRounds, len(remaining))
	}

	return result, nil
}
