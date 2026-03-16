package context

import (
	"context"
	"encoding/json"
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

// OffloadProcessor offloads large or old messages to the filesystem.
// It is the first step in the compaction hierarchy.
type OffloadProcessor struct {
	MaxTokens    int
	ThresholdPct float64
	OffloadDir   string
	MinKeepTurns int
	OnPreCompact func(ctx context.Context, sess *session.Session)
}

// NewOffloadProcessor creates a new offload processor.
func NewOffloadProcessor(maxTokens int, offloadDir string) *OffloadProcessor {
	return &OffloadProcessor{
		MaxTokens:    maxTokens,
		ThresholdPct: defaultThresholdPct,
		OffloadDir:   offloadDir,
		MinKeepTurns: defaultMinKeepTurns,
	}
}

func (p *OffloadProcessor) Process(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
	req *llm.LLMRequest,
) error {
	if p.MaxTokens <= 0 || p.OffloadDir == "" {
		return nil
	}

	// 1. Calculate usage
	currentTokens := EstimateMessagesTokens(ctx, pr, model, req.Messages)

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

	// Identify candidates
	numMessages := len(req.Messages)
	if numMessages <= p.MinKeepTurns {
		return nil
	}

	events := sess.Events()
	findEventID := func(content string) string {
		for i := len(events) - 1; i >= 0; i-- {
			e := events[i]
			if e.Type == session.EventTypeMessageAdded {
				var m llm.Message
				if err := json.Unmarshal(e.Data, &m); err == nil && m.Content == content {
					return e.ID.String()
				}
			}
		}
		return ""
	}

	// Simple implementation: Offload Tool results that are not in the last N messages
	candidates := req.Messages[:numMessages-p.MinKeepTurns]
	var newMessages []llm.Message

	for _, m := range candidates {
		if m.Role == llm.RoleTool && len(m.Content) > largeToolThreshold {
			// Offload it
			id := findEventID(m.Content)
			if id == "" {
				id = fmt.Sprintf("offload-%s-%d", sess.ID(), len(newMessages))
			}
			path := filepath.Join(p.OffloadDir, id+".json")

			if err := os.WriteFile(path, []byte(m.Content), filePerm); err != nil {
				return fmt.Errorf("failed to write offload file: %w", err)
			}

			// Replace with placeholder
			m.Content = fmt.Sprintf(
				"[Content offloaded to %s. Use read_offload tool to retrieve.]",
				path,
			)
		}
		newMessages = append(newMessages, m)
	}

	// Add remaining messages (non-candidates)
	newMessages = append(newMessages, req.Messages[numMessages-p.MinKeepTurns:]...)

	req.Messages = newMessages

	return nil
}
