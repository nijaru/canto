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

// LeafID returns the event id at the tip of this session's active branch.
func (s *Session) LeafID(ctx context.Context) (string, error) {
	replayed, err := s.Replay(ctx)
	if err != nil {
		return "", err
	}
	return replayed.LeafID(), nil
}

// ActiveEvents returns the durable events on this session's active branch.
func (s *Session) ActiveEvents(ctx context.Context) ([]session.Event, error) {
	replayed, err := s.Replay(ctx)
	if err != nil {
		return nil, err
	}
	return replayed.ActiveEvents()
}

// BranchEvents returns durable events on the branch ending at eventID. An empty
// eventID returns an empty root branch.
func (s *Session) BranchEvents(ctx context.Context, eventID string) ([]session.Event, error) {
	replayed, err := s.Replay(ctx)
	if err != nil {
		return nil, err
	}
	return replayed.BranchEvents(eventID)
}

// MoveLeaf records a durable active-branch movement for this session. An empty
// eventID moves the active branch to the session root.
func (s *Session) MoveLeaf(ctx context.Context, eventID string) error {
	replayed, err := s.Replay(ctx)
	if err != nil {
		return err
	}
	return replayed.MoveLeaf(ctx, eventID)
}

// EffectiveSettings returns model/thinking selections recovered from this
// session's active branch.
func (s *Session) EffectiveSettings(ctx context.Context) (session.EffectiveSettings, error) {
	replayed, err := s.Replay(ctx)
	if err != nil {
		return session.EffectiveSettings{}, err
	}
	return replayed.EffectiveSettings()
}

// SetModel records a durable model selection for this session and updates the
// harness agent for future turns. The provider stays the harness provider.
func (s *Session) SetModel(ctx context.Context, model string) error {
	if err := s.validateMaintenanceHandle(); err != nil {
		return err
	}
	providerID := ""
	if s.harness.Provider != nil {
		providerID = s.harness.Provider.ID()
	}
	replayed, err := s.Replay(ctx)
	if err != nil {
		return err
	}
	previous, err := replayed.EffectiveSettings()
	if err != nil {
		return err
	}
	selection := session.ModelSelection{ProviderID: providerID, Model: model}
	if err := replayed.AppendModelSelection(ctx, selection); err != nil {
		return err
	}
	s.harness.setModel(model)
	s.publishRuntimeEvent(ModelSelectedPayload{
		Model:         selection,
		PreviousModel: previous.Model,
		HadPrevious:   previous.HasModel,
	})
	return nil
}

// SetThinkingLevel records a durable thinking/reasoning selection for this
// session. Hosts map the stored level to provider-specific request controls.
func (s *Session) SetThinkingLevel(ctx context.Context, level string) error {
	replayed, err := s.Replay(ctx)
	if err != nil {
		return err
	}
	previous, err := replayed.EffectiveSettings()
	if err != nil {
		return err
	}
	selection := session.ThinkingSelection{Level: level}
	if err := replayed.AppendThinkingSelection(ctx, selection); err != nil {
		return err
	}
	s.publishRuntimeEvent(ThinkingSelectedPayload{
		Level:         level,
		PreviousLevel: previous.ThinkingLevel,
	})
	return nil
}

func (s *Session) publishRuntimeEvent(payload HarnessEventPayload) {
	if s == nil || s.state == nil || payload == nil {
		return
	}
	s.state.mu.Lock()
	event := s.state.newEventLocked("", payload)
	s.state.publishLocked(event)
	s.state.mu.Unlock()
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
