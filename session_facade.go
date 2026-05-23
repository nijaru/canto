package canto

import (
	"context"
	"fmt"
	"time"

	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/session"
)

// SnapshotOptions configures durable projection snapshot creation through the
// root session facade.
type SnapshotOptions struct {
	// MaxEvents overrides the count threshold for SnapshotIfNeeded. Zero keeps
	// the default; negative disables count-based snapshots.
	MaxEvents int
	// MaxAge enables age-based SnapshotIfNeeded checks when greater than zero.
	MaxAge time.Duration
	// Rebuilder overrides the default projection rebuilder.
	Rebuilder *session.Rebuilder
}

// Replay loads and replays this durable session from the harness store.
func (s *Session) Replay(ctx context.Context) (*session.Session, error) {
	if err := s.validateMaintenanceHandle(); err != nil {
		return nil, err
	}
	return s.harness.Store.Load(ctx, s.id)
}

// EventsAfter returns durable session events with sequence numbers greater than
// afterSeq. Stores with native sequence queries are used directly; other stores
// fall back to replaying the session and filtering in memory.
func (s *Session) EventsAfter(ctx context.Context, afterSeq int64) ([]session.Event, error) {
	if err := s.validateMaintenanceHandle(); err != nil {
		return nil, err
	}
	if queryStore, ok := s.harness.Store.(session.EventQueryStore); ok {
		return queryStore.EventsAfter(ctx, s.id, afterSeq)
	}
	replayed, err := s.Replay(ctx)
	if err != nil {
		return nil, err
	}
	events := replayed.Events()
	out := make([]session.Event, 0, len(events))
	for _, event := range events {
		if event.Seq > afterSeq {
			out = append(out, event)
		}
	}
	return out, nil
}

// Compact runs durable manual compaction for this session using the harness
// provider and model.
func (s *Session) Compact(
	ctx context.Context,
	opts governor.CompactOptions,
) (governor.CompactResult, error) {
	if err := s.validateMaintenanceHandle(); err != nil {
		return governor.CompactResult{}, err
	}
	if s.harness.Provider == nil {
		return governor.CompactResult{}, fmt.Errorf("canto session: nil provider")
	}
	if s.harness.Model == "" {
		return governor.CompactResult{}, fmt.Errorf("canto session: model is required")
	}
	replayed, err := s.Replay(ctx)
	if err != nil {
		return governor.CompactResult{}, err
	}
	return governor.CompactSession(
		ctx,
		s.harness.Provider,
		s.harness.Model,
		replayed,
		opts,
	)
}

// Snapshot appends a durable projection snapshot for this session.
func (s *Session) Snapshot(ctx context.Context, opts SnapshotOptions) (bool, error) {
	replayed, snapshotter, err := s.snapshotInputs(ctx, opts)
	if err != nil {
		return false, err
	}
	return snapshotter.Snapshot(ctx, replayed)
}

// SnapshotIfNeeded appends a durable projection snapshot when opts indicate the
// current event count or snapshot age requires one.
func (s *Session) SnapshotIfNeeded(ctx context.Context, opts SnapshotOptions) (bool, error) {
	replayed, snapshotter, err := s.snapshotInputs(ctx, opts)
	if err != nil {
		return false, err
	}
	return snapshotter.SnapshotIfNeeded(ctx, replayed)
}

// Fork persists a child session branch and returns a root facade handle for the
// new branch.
func (s *Session) Fork(
	ctx context.Context,
	newID string,
	opts session.ForkOptions,
) (*Session, error) {
	if err := s.validateMaintenanceHandle(); err != nil {
		return nil, err
	}
	replayed, err := s.Replay(ctx)
	if err != nil {
		return nil, err
	}
	child, err := replayed.Branch(ctx, newID, opts)
	if err != nil {
		return nil, err
	}
	return s.harness.Session(child.ID()), nil
}

func (s *Session) snapshotInputs(
	ctx context.Context,
	opts SnapshotOptions,
) (*session.Session, *session.ProjectionSnapshotter, error) {
	if err := s.validateMaintenanceHandle(); err != nil {
		return nil, nil, err
	}
	replayed, err := s.Replay(ctx)
	if err != nil {
		return nil, nil, err
	}
	snapshotter := session.NewProjectionSnapshotter()
	if opts.MaxEvents < 0 {
		snapshotter.MaxEvents = 0
	} else if opts.MaxEvents != 0 {
		snapshotter.MaxEvents = opts.MaxEvents
	}
	if opts.MaxAge != 0 {
		snapshotter.MaxAge = opts.MaxAge
	}
	if opts.Rebuilder != nil {
		snapshotter.Rebuilder = opts.Rebuilder
	}
	return replayed, snapshotter, nil
}

func (s *Session) validateMaintenanceHandle() error {
	if s == nil || s.harness == nil {
		return fmt.Errorf("canto session: nil harness")
	}
	if s.harness.Store == nil {
		return fmt.Errorf("canto session: nil store")
	}
	if s.id == "" {
		return fmt.Errorf("canto session: id is required")
	}
	return nil
}
