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

// Send appends a user message to the session store and runs the agent.
// It returns the final StepResult so callers can read the assistant reply
// without a separate store load.
func (r *Runner) Send(ctx context.Context, sessionID, message string) (agent.StepResult, error) {
	e := session.NewEvent(sessionID, session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: message,
	})
	if err := r.Store.Save(ctx, e); err != nil {
		return agent.StepResult{}, err
	}
	return r.Run(ctx, sessionID)
}

// SendStream appends a user message and runs the agent with streaming.
// If the agent implements agent.Streamer, chunkFn receives tokens as they
// arrive; otherwise the call falls back to non-streaming Turn.
func (r *Runner) SendStream(
	ctx context.Context,
	sessionID, message string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	e := session.NewEvent(sessionID, session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: message,
	})
	if err := r.Store.Save(ctx, e); err != nil {
		return agent.StepResult{}, err
	}
	return r.RunStream(ctx, sessionID, chunkFn)
}

// Run executes the agent on the given session, serialized within the session lane.
func (r *Runner) Run(ctx context.Context, sessionID string) (agent.StepResult, error) {
	var result agent.StepResult
	errCh := r.lanes.Execute(ctx, sessionID, func(ctx context.Context) error {
		var err error
		result, err = r.execute(ctx, sessionID, nil)
		return err
	})
	return result, <-errCh
}

// RunStream executes the agent with streaming, serialized within the session lane.
// If the agent implements agent.Streamer, chunkFn receives tokens as they arrive.
func (r *Runner) RunStream(
	ctx context.Context,
	sessionID string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	var result agent.StepResult
	errCh := r.lanes.Execute(ctx, sessionID, func(ctx context.Context) error {
		var err error
		result, err = r.execute(ctx, sessionID, chunkFn)
		return err
	})
	return result, <-errCh
}

func (r *Runner) execute(
	ctx context.Context,
	sessionID string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	// 1. Load session
	sess, err := r.Store.Load(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}

	meta := hook.SessionMeta{ID: sess.ID()}
	if _, err := r.Hooks.Run(ctx, hook.EventSessionStart, meta, nil); err != nil {
		return agent.StepResult{}, err
	}
	defer func() {
		r.Hooks.Run(context.Background(), hook.EventSessionEnd, meta, nil)
	}()

	// 2. Capture initial event count for durability
	initialCount := len(sess.Events())

	// 3. Fire UserPromptSubmit if the last message is from the user.
	msgs := sess.Messages()
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == llm.RoleUser {
		if _, err := r.Hooks.Run(ctx, hook.EventUserPromptSubmit, meta, map[string]any{
			"content": msgs[len(msgs)-1].Content,
		}); err != nil {
			return agent.StepResult{}, err
		}
	}

	// 4. Execute agent turn.
	// Use streaming if chunkFn is set and the agent supports it.
	var result agent.StepResult
	if chunkFn != nil {
		if s, ok := r.Agent.(agent.Streamer); ok {
			result, err = s.StreamTurn(ctx, sess, chunkFn)
		} else {
			result, err = r.Agent.Turn(ctx, sess)
		}
	} else {
		result, err = r.Agent.Turn(ctx, sess)
	}
	if err != nil {
		return agent.StepResult{}, err
	}

	// 5. Save new events only.
	for _, e := range sess.Events()[initialCount:] {
		if err := r.Store.Save(ctx, e); err != nil {
			return agent.StepResult{}, err
		}
	}

	return result, nil
}
