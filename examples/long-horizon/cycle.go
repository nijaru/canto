package longhorizon

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/session"
)

// CycleRunner implements the "Ralph Wiggum" pattern: run an agent repeatedly
// with hard context resets between cycles until a completion check passes or
// MaxCycles is reached. Progress is communicated via files (e.g., plan.md,
// progress.md), not through context — this allows long-horizon tasks that
// exceed a single context window.
//
// Each cycle creates a fresh session to force a clean context reset. The
// agent reads progress files via tools to understand where to continue.
type CycleRunner struct {
	Agent     agent.Agent
	Store     session.Store
	PlanFile  string                              // path to the plan file (read by agent via tool)
	MaxCycles int                                 // hard limit on cycles
	CheckFn   func(planPath string) (bool, error) // returns true when the goal is achieved
	SessionFn func(cycle int) *session.Session    // factory: creates the session for each cycle
}

// Run executes the cycle loop. Each iteration creates a fresh session,
// runs the agent until it completes its turn, then checks whether the
// goal is achieved via CheckFn. Stops on completion, error, or MaxCycles.
func (c *CycleRunner) Run(ctx context.Context) error {
	if c.MaxCycles <= 0 {
		return fmt.Errorf("cycle: MaxCycles must be > 0")
	}
	if c.CheckFn == nil {
		return fmt.Errorf("cycle: CheckFn must be set")
	}
	if c.SessionFn == nil {
		return fmt.Errorf("cycle: SessionFn must be set")
	}

	for cycle := range c.MaxCycles {
		if err := ctx.Err(); err != nil {
			return err
		}

		sess := c.SessionFn(cycle)

		// Run the agent for this cycle.
		if _, err := c.Agent.Turn(ctx, sess); err != nil {
			return fmt.Errorf("cycle %d: %w", cycle, err)
		}

		// Persist the new events to the store if one is configured.
		if c.Store != nil {
			for _, e := range sess.Events() {
				if err := c.Store.Save(ctx, e); err != nil {
					return fmt.Errorf("cycle %d: save: %w", cycle, err)
				}
			}
		}

		// Check whether the goal has been reached.
		done, err := c.CheckFn(c.PlanFile)
		if err != nil {
			return fmt.Errorf("cycle %d: check: %w", cycle, err)
		}
		if done {
			return nil
		}
	}

	return fmt.Errorf("cycle: reached MaxCycles (%d) without completing", c.MaxCycles)
}
