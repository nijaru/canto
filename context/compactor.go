package context

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

const (
	defaultThresholdPct = 0.60
	defaultMinKeepTurns = 3
	dirPerm             = 0o755
	filePerm            = 0o644
	largeToolThreshold  = 1000
)

// Offloader offloads large or old messages to the filesystem.
// It is the first step in the compaction hierarchy.
type Offloader struct {
	MaxTokens    int
	ThresholdPct float64
	OffloadDir   string
	MinKeepTurns int
	OnPreCompact func(ctx context.Context, sess *session.Session)
}

// Effects reports that offloading mutates both session state and external
// filesystem state.
func (p *Offloader) Effects() ProcessorEffects {
	return ProcessorEffects{
		Session:  true,
		External: true,
	}
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

func (p *Offloader) Process(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	if p.MaxTokens <= 0 || p.OffloadDir == "" {
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
	currentTokens := EstimateMessagesTokens(ctx, pr, model, messages)

	// 2. If usage <= Threshold, do nothing
	if !exceedsThreshold(currentTokens, p.MaxTokens, p.ThresholdPct) {
		return nil
	}

	if p.OnPreCompact != nil {
		p.OnPreCompact(ctx, sess)
	}

	// 3. Select messages to offload
	// Strategy: Keep last 3 turns (Assistant + User/Tool)
	// For messages older than that, if they are large tool results, offload them.

	// Ensure offload directory exists
	if err := os.MkdirAll(p.OffloadDir, dirPerm); err != nil {
		return fmt.Errorf("failed to create offload dir: %w", err)
	}
	root, err := os.OpenRoot(p.OffloadDir)
	if err != nil {
		return fmt.Errorf("failed to open offload root: %w", err)
	}
	defer root.Close()

	// Identify candidates
	numMessages := len(entries)
	if numMessages <= p.MinKeepTurns {
		return nil
	}
	prefix := historyPrefix(req, len(messages))
	events := sess.Events()
	cutoffEventID := lastMessageEventID(events)

	// Simple implementation: Offload Tool results that are not in the last N messages
	candidates := entries[:numMessages-p.MinKeepTurns]
	newEntries := make([]session.HistoryEntry, 0, numMessages)

	for i, entry := range candidates {
		m := entry.Message
		if m.Role == llm.RoleTool && len(m.Content) > largeToolThreshold {
			// Offload it
			id := offloadCandidateID(sess.ID(), cutoffEventID, entry, i)
			filename := id + ".json"
			path := filepath.Join(p.OffloadDir, filename)

			if err := root.WriteFile(filename, []byte(m.Content), filePerm); err != nil {
				return fmt.Errorf("failed to write offload file: %w", err)
			}

			// Replace with placeholder
			m.Content = offloadPlaceholder(path)
			entry.Message = m
		}
		newEntries = append(newEntries, entry)
	}

	// Add remaining messages (non-candidates)
	newEntries = append(newEntries, cloneHistoryEntries(entries[numMessages-p.MinKeepTurns:])...)

	newMessages := make([]llm.Message, 0, len(newEntries))
	for _, entry := range newEntries {
		newMessages = append(newMessages, entry.Message)
	}

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

	req.Messages = append(prefix, newMessages...)

	return nil
}
