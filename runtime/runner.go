package runtime

import (
	"context"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Runner orchestrates the execution of an agent within a session.
// It always uses a LaneManager to serialize execution within a session
// while allowing concurrent execution across different sessions.
type Runner struct {
	Store session.Store
	Agent agent.Agent
	lanes *LaneManager
	Hooks *hook.Runner
}

// NewRunner creates a Runner with per-session lane serialization enabled.
func NewRunner(s session.Store, a agent.Agent) *Runner {
	return &Runner{
		Store: s,
		Agent: a,
		lanes: NewLaneManager(),
		Hooks: hook.NewRunner(),
	}
}

// Send appends a user message to the session store and then runs the agent.
// It is the primary entry point for interactive agents — callers no longer need
// to manually construct and save the user message event before calling Run.
// The EventUserPromptSubmit hook fires before the agent turn begins.
func (r *Runner) Send(ctx context.Context, sessionID, message string) error {
	e := session.NewEvent(sessionID, session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: message,
	})
	if err := r.Store.Save(ctx, e); err != nil {
		return err
	}
	return r.Run(ctx, sessionID)
}

// Run executes the agent on the given session, serialized within the session lane.
func (r *Runner) Run(ctx context.Context, sessionID string) error {
	result := r.lanes.Execute(ctx, sessionID, func(ctx context.Context) error {
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

	meta := hook.SessionMeta{ID: sess.ID()}
	if _, err := r.Hooks.Run(ctx, hook.EventSessionStart, meta, nil); err != nil {
		return err
	}
	defer func() {
		r.Hooks.Run(context.Background(), hook.EventSessionEnd, meta, nil)
	}()

	// 2. Capture initial event count for durability
	initialEvents := sess.Events()
	initialCount := len(initialEvents)

	// 3. Fire UserPromptSubmit if the last message is from the user.
	msgs := sess.Messages()
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == llm.RoleUser {
		if _, err := r.Hooks.Run(ctx, hook.EventUserPromptSubmit, meta, map[string]any{
			"content": msgs[len(msgs)-1].Content,
		}); err != nil {
			return err
		}
	}

	// 4. Execute agent turn (handoff result ignored at this layer;
	//    graph/swarm handle routing above the Runner).
	if _, err := r.Agent.Turn(ctx, sess); err != nil {
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
