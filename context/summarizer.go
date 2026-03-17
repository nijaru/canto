package context

import (
	"context"
	"fmt"
	"strings"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// SummarizeProcessor summarizes old messages to reduce context size.
// It is the second step in the compaction hierarchy (after Offload).
type SummarizeProcessor struct {
	MaxTokens    int
	ThresholdPct float64
	MinKeepTurns int
	Provider     llm.Provider
	Model        string
	// OnPreCompact is called before summarization begins, if non-nil.
	OnPreCompact func(ctx context.Context, sess *session.Session)
}

// NewSummarizeProcessor creates a new summarize processor.
func NewSummarizeProcessor(maxTokens int, provider llm.Provider, model string) *SummarizeProcessor {
	return &SummarizeProcessor{
		MaxTokens:    maxTokens,
		ThresholdPct: 0.60,
		MinKeepTurns: 3,
		Provider:     provider,
		Model:        model,
	}
}

func (p *SummarizeProcessor) Process(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
	req *llm.LLMRequest,
) error {
	if p.MaxTokens <= 0 || p.Provider == nil {
		return nil
	}

	// 1. Calculate usage
	currentTokens := EstimateMessagesTokens(ctx, pr, model, req.Messages)

	// 2. If usage <= Threshold, do nothing
	if !exceedsThreshold(currentTokens, p.MaxTokens, p.ThresholdPct) {
		return nil
	}

	if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.EventTypeCompactionTriggered, map[string]any{
		"strategy":       "summarize",
		"max_tokens":     p.MaxTokens,
		"threshold_pct":  p.ThresholdPct,
		"current_tokens": currentTokens,
	})); err != nil {
		return err
	}

	if p.OnPreCompact != nil {
		p.OnPreCompact(ctx, sess)
	}

	// 3. Identify candidates
	numMessages := len(req.Messages)
	if numMessages <= p.MinKeepTurns {
		return nil
	}

	// Strategy: Keep system messages and the last N turns.
	// Summarize the rest.
	var systemMsgs []llm.Message
	var candidates []llm.Message
	var recentMsgs []llm.Message

	for i, m := range req.Messages {
		if m.Role == llm.RoleSystem {
			systemMsgs = append(systemMsgs, m)
		} else if i >= numMessages-p.MinKeepTurns {
			recentMsgs = append(recentMsgs, m)
		} else {
			candidates = append(candidates, m)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// We format the older messages into a string to prompt the LLM to summarize them
	var sb strings.Builder
	for _, m := range candidates {
		role := strings.ToUpper(string(m.Role))
		if m.Name != "" {
			role = fmt.Sprintf("%s (%s)", role, m.Name)
		}
		sb.WriteString(fmt.Sprintf("%s: ", role))

		if m.Content != "" {
			sb.WriteString(m.Content)
		}

		if len(m.Calls) > 0 {
			sb.WriteString("\nTOOL CALLS:")
			for _, call := range m.Calls {
				sb.WriteString(
					fmt.Sprintf(
						"\n- %s(%s) [ID: %s]",
						call.Function.Name,
						call.Function.Arguments,
						call.ID,
					),
				)
			}
		}

		if m.Role == llm.RoleTool && m.ToolID != "" {
			sb.WriteString(fmt.Sprintf("\n[TOOL ID: %s]", m.ToolID))
		}

		sb.WriteString("\n\n")
	}

	// Generate summary
	summarizeReq := &llm.LLMRequest{
		Model: p.Model,
		Messages: []llm.Message{
			{
				Role:    llm.RoleSystem,
				Content: "You are a helpful assistant that summarizes conversations. Summarize the following conversation history concisely but comprehensively, retaining key facts, decisions, and tool execution outcomes.",
			},
			{
				Role:    llm.RoleUser,
				Content: sb.String(),
			},
		},
		Temperature: 0.0,
	}

	resp, err := p.Provider.Generate(ctx, summarizeReq)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}

	// Build new messages: system + summary + recent
	var newMessages []llm.Message
	newMessages = append(newMessages, systemMsgs...)
	newMessages = append(newMessages, llm.Message{
		Role:    llm.RoleSystem,
		Content: fmt.Sprintf("<conversation_summary>\n%s\n</conversation_summary>", resp.Content),
	})
	newMessages = append(newMessages, recentMsgs...)

	req.Messages = newMessages

	return nil
}
