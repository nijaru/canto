package runtime

import (
	"context"
	"fmt"

	"github.com/robfig/cron/v3"
	"github.com/nijaru/canto/session"
)

// HeartbeatEntry defines a scheduled agent task.
type HeartbeatEntry struct {
	// ID is a human-readable label used in logs.
	ID string
	// Schedule is a cron expression: "@every 5m", "@daily", "0 9 * * 1-5", etc.
	Schedule string
	// SessionFn is called each tick to obtain the session to run against.
	// It may create a new session or resume an existing one.
	SessionFn func() *session.Session
	// MaxCost is a per-session USD budget. The tick is skipped when the session's
	// accumulated cost meets or exceeds this value. Zero means no limit.
	MaxCost float64
}

// Heartbeat drives proactive, scheduled agent execution.
//
// Lifecycle:
//
//	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
//	defer cancel()
//	if err := heartbeat.Start(ctx); err != nil { ... }
//
// Start blocks until ctx is cancelled and all in-flight jobs have finished.
// The caller owns the context; there is no separate Stop method.
type Heartbeat struct {
	runner  *Runner
	entries []HeartbeatEntry
}

// NewHeartbeat creates a Heartbeat backed by the given runner.
func NewHeartbeat(r *Runner) *Heartbeat {
	return &Heartbeat{runner: r}
}

// Add registers a scheduled entry. Must be called before Start.
func (h *Heartbeat) Add(e HeartbeatEntry) {
	h.entries = append(h.entries, e)
}

// Schedule registers a session-ID-based entry and validates the spec immediately.
// It is a convenience wrapper around Add for callers that don't need a full HeartbeatEntry.
// Returns an error if the schedule expression is invalid.
func (h *Heartbeat) Schedule(spec, sessionID string) error {
	// Validate spec eagerly so callers get an error at registration time, not at Start.
	// Try the extended parser first (supports seconds field and descriptors like @every).
	p := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := p.Parse(spec); err != nil {
		return fmt.Errorf("invalid schedule %q: %w", spec, err)
	}

	h.entries = append(h.entries, HeartbeatEntry{
		ID:       sessionID,
		Schedule: spec,
		SessionFn: func() *session.Session {
			sess, err := h.runner.Store.Load(context.Background(), sessionID)
			if err != nil {
				return session.New(sessionID)
			}
			return sess
		},
	})
	return nil
}

// Start schedules all registered entries and blocks until ctx is cancelled.
// When ctx is cancelled it waits for all in-flight jobs to complete before
// returning — equivalent to a graceful shutdown.
func (h *Heartbeat) Start(ctx context.Context) error {
	c := cron.New(cron.WithSeconds())

	for _, e := range h.entries {
		e := e // capture
		if _, err := c.AddFunc(e.Schedule, func() {
			h.tick(ctx, e)
		}); err != nil {
			c.Stop()
			return fmt.Errorf("heartbeat %s: invalid schedule %q: %w", e.ID, e.Schedule, err)
		}
	}

	c.Start()
	<-ctx.Done()
	// Wait for all in-flight cron jobs to finish.
	stopCtx := c.Stop()
	<-stopCtx.Done()
	return nil
}

func (h *Heartbeat) tick(ctx context.Context, e HeartbeatEntry) {
	sess := e.SessionFn()

	if e.MaxCost > 0 && sess.TotalCost() >= e.MaxCost {
		fmt.Printf("heartbeat %s: cost budget %.4f exceeded (%.4f), skipping tick\n",
			e.ID, e.MaxCost, sess.TotalCost())
		return
	}

	// Enqueue into the lane rather than spawning a raw goroutine.
	// This guarantees session-level serialization: if a previous tick is still
	// running for this session ID, the new work is queued behind it.
	if err := <-h.runner.lanes.Execute(ctx, sess.ID(), func(ctx context.Context) error {
		return h.runner.execute(ctx, sess.ID())
	}); err != nil {
		fmt.Printf("heartbeat %s: tick failed: %v\n", e.ID, err)
	}
}
