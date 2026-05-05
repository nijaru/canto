package governor

import (
	"context"
	"fmt"
	"strings"

	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
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
	// OnPreCompact is called right before summarization work begins, if non-nil.
	OnPreCompact func(ctx context.Context, sess *session.Session)
}

// Effects reports that summarization appends durable compaction facts to the
// session log.
func (p *Summarizer) Effects() prompt.SideEffects {
	return prompt.SideEffects{Session: true}
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
	currentTokens := prompt.EstimateMessagesTokens(ctx, pr, model, messages)

	// 2. If usage <= Threshold, do nothing
	if !prompt.ExceedsThreshold(currentTokens, p.MaxTokens, p.ThresholdPct) {
		return nil
	}

	// 3. Identify candidates
	numMessages := len(entries)
	if numMessages <= p.MinKeepTurns {
		return nil
	}
	if p.OnPreCompact != nil {
		p.OnPreCompact(ctx, sess)
	}

	// Strategy: Keep durable context entries and the last N turns.
	// Summarize the rest.
	var contextEntries []session.HistoryEntry
	var candidates []llm.Message
	var recentEntries []session.HistoryEntry

	for i, entry := range entries {
		m := entry.Message
		if isDurableContextEntry(entry) {
			contextEntries = append(contextEntries, entry)
		} else if i >= numMessages-p.MinKeepTurns {
			recentEntries = append(recentEntries, entry)
		} else {
			candidates = append(candidates, m)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	historyCandidates, turnPrefix, splitTurn := splitTurnPrefix(candidates, recentEntries)
	if len(historyCandidates) == 0 && len(turnPrefix) == 0 {
		return nil
	}

	summaryContent, err := p.generateSummary(
		ctx,
		contextEntries,
		formatMessages(historyCandidates),
		formatMessages(turnPrefix),
		splitTurn,
	)
	if err != nil {
		return err
	}

	// Build new messages: durable context + summary + recent
	newEntries := cloneHistoryEntries(contextEntries)

	// Extract and track file paths across compaction windows.
	newRead, newModified := extractFilePaths(candidates)
	prevRead, prevModified := previousFileLists(sess)
	allRead := mergeFileLists(newRead, prevRead)
	allModified := mergeFileLists(newModified, prevModified)

	if len(allRead) > 0 || len(allModified) > 0 {
		var fileTags strings.Builder
		if len(allRead) > 0 {
			fileTags.WriteString("<read-files>\n")
			for _, f := range allRead {
				fileTags.WriteString(f + "\n")
			}
			fileTags.WriteString("</read-files>\n")
		}
		if len(allModified) > 0 {
			fileTags.WriteString("<modified-files>\n")
			for _, f := range allModified {
				fileTags.WriteString(f + "\n")
			}
			fileTags.WriteString("</modified-files>\n")
		}
		summaryContent = fileTags.String() + summaryContent
	}

	newEntries = append(newEntries, session.HistoryEntry{
		EventType:        session.ContextAdded,
		ContextKind:      session.ContextKindSummary,
		ContextPlacement: session.ContextPlacementPrefix,
		Message: llm.Message{
			Role: llm.RoleUser,
			Content: fmt.Sprintf(
				"<conversation_summary>\n%s\n</conversation_summary>",
				summaryContent,
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
		ReadFiles:     allRead,
		ModifiedFiles: allModified,
	})
	if err := sess.Append(ctx, event); err != nil {
		return err
	}
	return nil
}
