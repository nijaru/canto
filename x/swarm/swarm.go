package swarm

import (
	"context"
	"fmt"
	"sync"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tracing"
)

// SwarmResult summarises a completed swarm run.
type SwarmResult struct {
	Rounds     int
	TasksDone  int
	TotalCost  float64
	TotalUsage llm.Usage
}

// Swarm coordinates a set of agents via a shared Blackboard.
// Each round, agents concurrently claim and execute unclaimed tasks.
// The swarm finishes when no unclaimed tasks remain or MaxRounds is reached.
//
// All agents share a single session so their tool calls and outputs are
// visible to each other through the event log. If isolation is needed,
// callers should manage separate sessions and aggregate results manually.
type Swarm struct {
	agents     []agent.Agent
	blackboard Blackboard
	maxRounds  int
}

// New creates a Swarm with the given agents and blackboard.
// maxRounds is a hard cap on coordination rounds (prevents infinite loops).
func New(blackboard Blackboard, maxRounds int, agents ...agent.Agent) *Swarm {
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

	ctx, span := tracing.StartSwarm(ctx, sess.ID())
	defer span.End()

	for round := range s.maxRounds {
		if err := ctx.Err(); err != nil {
			span.RecordError(err)
			return result, err
		}

		roundCtx, roundSpan := tracing.StartSwarmRound(ctx, round)

		unclaimed, err := s.blackboard.ListUnclaimed(roundCtx)
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
		var usageMu sync.Mutex

		// Distribute unclaimed tasks round-robin as hints, but let agents
		// actually claim via the atomic ClaimTask. An agent whose hint was
		// stolen simply produces worked=false for this round.
		for i, a := range s.agents {
			i, a := i, a
			wg.Go(func() {
				defer func() {
					if r := recover(); r != nil {
						outcomes[i] = outcome{
							agentID: a.ID(),
							err:     fmt.Errorf("agent %q panicked: %v", a.ID(), r),
						}
					}
				}()
				out := outcome{agentID: a.ID()}

				// Try each task in order until one is successfully claimed.
				var claimed *Task
				for j := range unclaimed {
					ok, claimErr := s.blackboard.ClaimTask(roundCtx, a.ID(), unclaimed[j].ID)
					if claimErr != nil {
						out.err = fmt.Errorf("claim %q: %w", unclaimed[j].ID, claimErr)
						outcomes[i] = out
						return
					}
					if ok {
						t := unclaimed[j]
						claimed = &t
						break
					}
				}
				if claimed == nil {
					// All tasks stolen by other agents this round — that's fine.
					outcomes[i] = out
					return
				}

				// Post the claimed task description to the blackboard so other
				// agents can observe what this agent is working on.
				_ = s.blackboard.Post(roundCtx, a.ID(), "current_task", claimed.Description)

				// Execute one agent turn on the shared session within its own span.
				ctx, agentSpan := tracing.StartAgent(roundCtx, a.ID())
				turnRes, turnErr := a.Turn(ctx, sess)
				if turnErr != nil {
					agentSpan.RecordError(turnErr)
					agentSpan.End()
					out.err = fmt.Errorf("turn for task %q: %w", claimed.ID, turnErr)
					outcomes[i] = out
					return
				}
				if turnRes.TurnStopReason.StopsProgress() {
					stopErr := fmt.Errorf(
						"turn for task %q stopped with turn stop state %s",
						claimed.ID,
						turnRes.TurnStopReason,
					)
					agentSpan.RecordError(stopErr)
					agentSpan.End()
					out.err = stopErr
					outcomes[i] = out
					return
				}
				agentSpan.End()
				usageMu.Lock()
				result.TotalUsage.InputTokens += turnRes.Usage.InputTokens
				result.TotalUsage.OutputTokens += turnRes.Usage.OutputTokens
				result.TotalUsage.TotalTokens += turnRes.Usage.TotalTokens
				result.TotalUsage.Cost += turnRes.Usage.Cost
				usageMu.Unlock()

				out.worked = true
				outcomes[i] = out
			})
		}
		wg.Wait()
		roundSpan.End()

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
	}

	// Set TotalCost once at the end — sess.TotalCost() is monotonically
	// increasing so reading it per-round would double-count earlier rounds.
	result.TotalCost = sess.TotalCost()

	// Verify all tasks are actually done.
	remaining, err := s.blackboard.ListUnclaimed(ctx)
	if err != nil {
		return result, fmt.Errorf("swarm: final check: %w", err)
	}
	if len(remaining) > 0 && result.Rounds >= s.maxRounds {
		return result, fmt.Errorf(
			"swarm: reached MaxRounds (%d) with %d tasks unclaimed",
			s.maxRounds,
			len(remaining),
		)
	}

	return result, nil
}
