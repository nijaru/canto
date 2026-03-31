package governor

import (
	"context"
	"fmt"
	"strings"

	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Summarizer summarizes old messages to reduce context size.
// It is the second step in the compaction hierarchy (after Offload).
type Summarizer struct {
	MaxTokens    int
	ThresholdPct float64
	MinKeepTurns int
	Provider     llm.Provider
	Model        string
	// Message is an optional instruction appended to the summarization prompt.
	// The summarizer LLM treats it as guidance on what to preserve or emphasize.
	Message string
	// OnPreCompact is called before summarization begins, if non-nil.
	OnPreCompact func(ctx context.Context, sess *session.Session)
}

// Effects reports that summarization appends durable compaction facts to the
// session log.
func (p *Summarizer) Effects() ccontext.SideEffects {
	return ccontext.SideEffects{Session: true}
}

// CompactionStrategy returns "summarize".
func (p *Summarizer) CompactionStrategy() string {
	return "summarize"
}

// NewSummarizer creates a new summarize processor.
func NewSummarizer(maxTokens int, provider llm.Provider, model string) *Summarizer {
	return &Summarizer{
		MaxTokens:    maxTokens,
		ThresholdPct: 0.60,
		MinKeepTurns: 3,
		Provider:     provider,
		Model:        model,
	}
}

// Mutate performs durable summarize compaction without directly rewriting the request.
func (p *Summarizer) Mutate(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
) error {
	return p.summarize(ctx, pr, model, sess)
}

func (p *Summarizer) summarize(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
) error {
	if p.MaxTokens <= 0 || p.Provider == nil {
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

	if p.OnPreCompact != nil {
		p.OnPreCompact(ctx, sess)
	}

	// 3. Identify candidates
	numMessages := len(entries)
	if numMessages <= p.MinKeepTurns {
		return nil
	}

	// Strategy: Keep system messages and the last N turns.
	// Summarize the rest.
	var systemEntries []session.HistoryEntry
	var candidates []llm.Message
	var recentEntries []session.HistoryEntry

	for i, entry := range entries {
		m := entry.Message
		if m.Role == llm.RoleSystem {
			systemEntries = append(systemEntries, entry)
		} else if i >= numMessages-p.MinKeepTurns {
			recentEntries = append(recentEntries, entry)
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
	summarizeMessages := []llm.Message{
		{
			Role:    llm.RoleSystem,
			Content: "You are a helpful assistant that summarizes conversations. Summarize the following conversation history concisely but comprehensively, retaining key facts, decisions, and tool execution outcomes.",
		},
	}
	if p.Message != "" {
		summarizeMessages = append(summarizeMessages, llm.Message{
			Role:    llm.RoleUser,
			Content: p.Message,
		})
	}
	summarizeMessages = append(summarizeMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: sb.String(),
	})
	summarizeReq := &llm.Request{
		Model:       p.Model,
		Messages:    summarizeMessages,
		Temperature: 0.0,
	}

	resp, err := p.Provider.Generate(ctx, summarizeReq)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}

	// Build new messages: system + summary + recent
	newEntries := cloneHistoryEntries(systemEntries)
	newEntries = append(newEntries, session.HistoryEntry{
		Message: llm.Message{
			Role: llm.RoleSystem,
			Content: fmt.Sprintf(
				"<conversation_summary>\n%s\n</conversation_summary>",
				resp.Content,
			),
		},
	})
	newEntries = append(newEntries, cloneHistoryEntries(recentEntries)...)

	event := session.NewCompactionEvent(sess.ID(), session.CompactionSnapshot{
		Strategy:      "summarize",
		MaxTokens:     p.MaxTokens,
		ThresholdPct:  p.ThresholdPct,
		CurrentTokens: currentTokens,
		CutoffEventID: lastMessageEventID(sess),
		Entries:       newEntries,
	})
	if err := sess.Append(ctx, event); err != nil {
		return err
	}
	return nil
}
