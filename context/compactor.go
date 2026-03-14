package context

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// OffloadProcessor offloads large or old messages to the filesystem.
// It is the first step in the compaction hierarchy.
type OffloadProcessor struct {
	MaxTokens      int
	ThresholdPct   float64
	OffloadDir     string
	MinKeepTurns   int
}

// NewOffloadProcessor creates a new offload processor.
func NewOffloadProcessor(maxTokens int, offloadDir string) *OffloadProcessor {
	return &OffloadProcessor{
		MaxTokens:    maxTokens,
		ThresholdPct: 0.60,
		OffloadDir:   offloadDir,
		MinKeepTurns: 3,
	}
}

func (p *OffloadProcessor) Process(ctx context.Context, sess *session.Session, req *llm.LLMRequest) error {
	if p.MaxTokens <= 0 || p.OffloadDir == "" {
		return nil
	}

	// 1. Calculate usage
	currentTokens := 0
	for _, m := range req.Messages {
		currentTokens += len(m.Content) / 4
	}

	// 2. If usage <= Threshold, do nothing
	if float64(currentTokens) <= float64(p.MaxTokens)*p.ThresholdPct {
		return nil
	}

	// 3. Select messages to offload
	// Strategy: Keep last 3 turns (Assistant + User/Tool)
	// For messages older than that, if they are large tool results, offload them.
	
	// Ensure offload directory exists
	if err := os.MkdirAll(p.OffloadDir, 0755); err != nil {
		return fmt.Errorf("failed to create offload dir: %w", err)
	}

	// Identify candidates
	numMessages := len(req.Messages)
	if numMessages <= p.MinKeepTurns {
		return nil
	}

	// Simple implementation: Offload Tool results that are not in the last N messages
	candidates := req.Messages[:numMessages-p.MinKeepTurns]
	var newMessages []llm.Message
	
	for i, m := range candidates {
		if m.Role == llm.RoleTool && len(m.Content) > 1000 {
			// Offload it
			eventID := fmt.Sprintf("offload-%s-%d", sess.ID(), i) // Should ideally be the original event ID
			path := filepath.Join(p.OffloadDir, eventID+".json")
			
			if err := os.WriteFile(path, []byte(m.Content), 0644); err != nil {
				return fmt.Errorf("failed to write offload file: %w", err)
			}

			// Replace with placeholder
			m.Content = fmt.Sprintf("[Content offloaded to %s. Use read_offload tool to retrieve.]", path)
			// TODO: Add metadata/ref to allow retrieval
		}
		newMessages = append(newMessages, m)
	}

	// Add remaining messages (non-candidates)
	newMessages = append(newMessages, req.Messages[numMessages-p.MinKeepTurns:]...)
	
	req.Messages = newMessages
	
	return nil
}

// Helper to estimate tokens (same as in guard.go)
func estimateTokens(messages []llm.Message) int {
	tokens := 0
	for _, m := range messages {
		tokens += len(m.Content) / 4
	}
	return tokens
}
