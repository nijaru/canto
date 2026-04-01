package governor

import (
	"context"
	"fmt"
	"strings"

	"github.com/nijaru/canto/artifact"
	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

const (
	defaultThresholdPct = 0.60
	defaultMinKeepTurns = 3
	largeToolThreshold  = 1000
)

// Offloader offloads large or old messages to the filesystem.
// It is the first step in the compaction hierarchy.
type Offloader struct {
	MaxTokens    int
	ThresholdPct float64
	OffloadDir   string
	Artifacts    artifact.Store
	MinKeepTurns int
	// OnPreCompact is called right before offload work begins, if non-nil.
	OnPreCompact func(ctx context.Context, sess *session.Session)
}

// Effects reports that offloading mutates both session state and external
// filesystem state.
func (p *Offloader) Effects() ccontext.SideEffects {
	return ccontext.SideEffects{
		Session:  true,
		External: true,
	}
}

// CompactionStrategy returns "offload".
func (p *Offloader) CompactionStrategy() string {
	return "offload"
}

// NewOffloader creates a new offload processor.
func NewOffloader(maxTokens int, offloadDir string) *Offloader {
	return &Offloader{
		MaxTokens:    maxTokens,
		ThresholdPct: defaultThresholdPct,
		OffloadDir:   offloadDir,
		MinKeepTurns: defaultMinKeepTurns,
	}
}

// NewArtifactOffloader creates a new offload processor backed by an artifact store.
func NewArtifactOffloader(maxTokens int, store artifact.Store) *Offloader {
	return &Offloader{
		MaxTokens:    maxTokens,
		ThresholdPct: defaultThresholdPct,
		Artifacts:    store,
		MinKeepTurns: defaultMinKeepTurns,
	}
}

// Mutate performs durable offload compaction and records artifact descriptors.
func (p *Offloader) Mutate(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
) error {
	return p.compact(ctx, pr, model, sess)
}

func (p *Offloader) compact(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
) error {
	if p.MaxTokens <= 0 || (p.OffloadDir == "" && p.Artifacts == nil) {
		return nil
	}
	entries, err := sess.EffectiveEntries()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	messages := make([]llm.Message, 0, len(entries))
	for _, entry := range entries {
		messages = append(messages, entry.Message)
	}

	// 1. Calculate usage
	currentTokens := ccontext.EstimateMessagesTokens(ctx, pr, model, messages)

	// 2. If usage <= Threshold, do nothing
	if !ccontext.ExceedsThreshold(currentTokens, p.MaxTokens, p.ThresholdPct) {
		return nil
	}

	// Identify candidates
	numMessages := len(entries)
	if numMessages <= p.MinKeepTurns {
		return nil
	}
	if p.OnPreCompact != nil {
		p.OnPreCompact(ctx, sess)
	}
	cutoffEventID := lastMessageEventID(sess)
	store, closeStore, err := p.artifactStore()
	if err != nil {
		return err
	}
	if closeStore != nil {
		defer closeStore()
	}

	// Simple implementation: Offload Tool results that are not in the last N messages
	candidates := entries[:numMessages-p.MinKeepTurns]
	newEntries := make([]session.HistoryEntry, 0, numMessages)

	for i, entry := range candidates {
		m := entry.Message
		if m.Role == llm.RoleTool && len(m.Content) > largeToolThreshold {
			id := offloadCandidateID(sess.ID(), cutoffEventID, entry, i)
			desc, err := session.StoreArtifact(ctx, sess, store, session.ArtifactRecordedData{
				SessionID: sess.ID(),
				Artifact: artifact.Descriptor{
					ID:                id,
					Kind:              "context_offload",
					Label:             "Offloaded tool output",
					MIMEType:          "text/plain",
					ProducerSessionID: sess.ID(),
					ProducerEventID:   entry.EventID,
					Metadata: map[string]any{
						"strategy":        "offload",
						"source_event_id": entry.EventID,
						"tool_id":         m.ToolID,
					},
				},
			}, strings.NewReader(m.Content))
			if err != nil {
				return fmt.Errorf("failed to persist offload artifact: %w", err)
			}

			m.Content = offloadPlaceholder(desc.URI)
			entry.Message = m
		}
		newEntries = append(newEntries, entry)
	}

	// Add remaining messages (non-candidates)
	newEntries = append(newEntries, cloneHistoryEntries(entries[numMessages-p.MinKeepTurns:])...)

	event := session.NewCompactionEvent(sess.ID(), session.CompactionSnapshot{
		Strategy:      "offload",
		MaxTokens:     p.MaxTokens,
		ThresholdPct:  p.ThresholdPct,
		CurrentTokens: currentTokens,
		CutoffEventID: cutoffEventID,
		Entries:       newEntries,
	})
	if err := sess.Append(ctx, event); err != nil {
		return err
	}
	return nil
}

func (p *Offloader) artifactStore() (artifact.Store, func() error, error) {
	if p.Artifacts != nil {
		return p.Artifacts, nil, nil
	}
	store, err := artifact.NewFileStore(p.OffloadDir)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}
