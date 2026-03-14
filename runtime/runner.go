package runtime

import (
	"context"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/session"
)

// Runner orchestrates the execution of an agent within a session.
// It always uses a LaneManager to serialize execution within a session
// while allowing concurrent execution across different sessions.
type Runner struct {
	Store session.Store
	Agent *agent.Agent
	Lanes *LaneManager
}

// NewRunner creates a Runner with per-session lane serialization enabled.
func NewRunner(s session.Store, a *agent.Agent) *Runner {
	return &Runner{
		Store: s,
		Agent: a,
		Lanes: NewLaneManager(),
	}
}

// Run executes the agent on the given session, serialized within the session lane.
func (r *Runner) Run(ctx context.Context, sessionID string) error {
	result := r.Lanes.Execute(ctx, sessionID, func(ctx context.Context) error {
		return r.execute(ctx, sessionID)
	})
	return <-result
}

func (r *Runner) execute(ctx context.Context, sessionID string) error {
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
