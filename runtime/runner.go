package runtime

import (
	"context"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/session"
)

// Runner orchestrates the execution of an agent within a session.
type Runner struct {
	Store session.Store
	Agent *agent.Agent
}

// NewRunner creates a new runner.
func NewRunner(s session.Store, a *agent.Agent) *Runner {
	return &Runner{
		Store: s,
		Agent: a,
	}
}

// Run executes the agent on the given session.
func (r *Runner) Run(ctx context.Context, sessionID string) error {
	// 1. Load session
	sess, err := r.Store.Load(ctx, sessionID)
	if err != nil {
		return err
	}

	// 2. Capture initial event count for durability
	initialEvents := sess.Events()
	initialCount := len(initialEvents)

	// 3. Execute agent turn
	if err := r.Agent.Turn(ctx, sess); err != nil {
		return err
	}

	// 4. Save NEW events only
	allEvents := sess.Events()
	newEvents := allEvents[initialCount:]
	for _, e := range newEvents {
		if err := r.Store.Save(ctx, e); err != nil {
			return err
		}
	}

	return nil
}
