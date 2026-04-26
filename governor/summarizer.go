package governor

import (
	"context"
	"fmt"
	"strings"
	"sync"

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

	historyCandidates, turnPrefix, splitTurn := splitTurnPrefix(candidates, recentEntries)
	if len(historyCandidates) == 0 && len(turnPrefix) == 0 {
		return nil
	}

	summaryContent, err := p.generateSummary(
		ctx,
		systemEntries,
		formatMessages(historyCandidates),
		formatMessages(turnPrefix),
		splitTurn,
	)
	if err != nil {
		return err
	}

	// Build new messages: system + summary + recent
	newEntries := cloneHistoryEntries(systemEntries)

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
		Message: llm.Message{
			Role: llm.RoleSystem,
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

func (p *Summarizer) generateSummary(
	ctx context.Context,
	systemEntries []session.HistoryEntry,
	historyContent string,
	turnPrefixContent string,
	splitTurn bool,
) (string, error) {
	if !splitTurn {
		return p.generateHistorySummary(ctx, systemEntries, historyContent)
	}

	type result struct {
		text string
		err  error
	}
	historyCh := make(chan result, 1)
	turnCh := make(chan result, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		text, err := p.generateHistorySummary(ctx, systemEntries, historyContent)
		historyCh <- result{text: text, err: err}
	})
	wg.Go(func() {
		text, err := p.generateTurnPrefixSummary(ctx, turnPrefixContent)
		turnCh <- result{text: text, err: err}
	})
	wg.Wait()

	history := <-historyCh
	turn := <-turnCh
	if history.err != nil {
		return "", history.err
	}
	if turn.err != nil {
		return "", turn.err
	}
	if strings.TrimSpace(turn.text) == "" {
		return history.text, nil
	}
	return history.text + "\n\n## Active Turn Prefix\n" + turn.text, nil
}

func (p *Summarizer) generateHistorySummary(
	ctx context.Context,
	systemEntries []session.HistoryEntry,
	content string,
) (string, error) {
	const generatePrompt = `You summarize agent sessions.

Summarize the conversation into this structured format:

## Goal
What the user is trying to accomplish (1-2 sentences).

## Constraints
Known limitations, requirements, or constraints discovered.

## Progress
What has been done so far. Include key tool actions and their outcomes.

## Key Decisions
Important architectural or implementation decisions made, with brief rationale.

## Next Steps
What should happen next, in order.

## Critical Context
Anything that MUST be preserved — error states, partial work, active investigations, tool results, resource identifiers, and paths being modified.

Be specific. Preserve file paths, function names, and error messages. Do not summarize away actionable details.`

	const updatePrompt = `You summarize agent sessions.

You have an existing summary and new conversation segments. UPDATE the existing
summary to incorporate the new information. Follow the same structured format:

## Goal
## Constraints
## Progress
## Key Decisions
## Next Steps
## Critical Context

Rules:
- Preserve information from the existing summary that is still relevant.
- Add new progress, decisions, and context from the new segments.
- Remove information that is no longer relevant (completed tasks, superseded decisions).
- Keep the summary compact. Do not let it grow unboundedly across updates.
- Be specific. Preserve file paths, function names, and error messages.`

	existingSummary, hasPrevious := extractPreviousSummary(systemEntries)

	var systemPrompt string
	var userContent string

	if hasPrevious {
		systemPrompt = updatePrompt
		userContent = fmt.Sprintf(
			"<existing_summary>\n%s\n</existing_summary>\n\n<new_segments>\n%s\n</new_segments>",
			existingSummary,
			content,
		)
	} else {
		systemPrompt = generatePrompt
		userContent = content
	}

	summarizeMessages := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
	}
	if p.Message != "" {
		summarizeMessages = append(summarizeMessages, llm.Message{
			Role:    llm.RoleUser,
			Content: p.Message,
		})
	}
	summarizeMessages = append(summarizeMessages, llm.Message{
		Role:    llm.RoleUser,
		Content: userContent,
	})
	summarizeReq := &llm.Request{
		Model:       p.Model,
		Messages:    summarizeMessages,
		Temperature: 0.0,
	}

	resp, err := p.Provider.Generate(ctx, summarizeReq)
	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}
	return resp.Content, nil
}

func (p *Summarizer) generateTurnPrefixSummary(
	ctx context.Context,
	content string,
) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", nil
	}
	req := &llm.Request{
		Model: p.Model,
		Messages: []llm.Message{
			{
				Role: llm.RoleSystem,
				Content: `Summarize this active partial agent turn.

The turn was cut before all tool observations could remain in context. Preserve
the user request, assistant tool calls, tool IDs, arguments, and what result is
still expected next. Keep it concise.`,
			},
			{Role: llm.RoleUser, Content: content},
		},
		Temperature: 0.0,
	}
	resp, err := p.Provider.Generate(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to generate active turn summary: %w", err)
	}
	return resp.Content, nil
}

func formatMessages(messages []llm.Message) string {
	var sb strings.Builder
	for _, m := range messages {
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
	return sb.String()
}

func splitTurnPrefix(
	candidates []llm.Message,
	recentEntries []session.HistoryEntry,
) ([]llm.Message, []llm.Message, bool) {
	if len(candidates) == 0 || len(recentEntries) == 0 {
		return candidates, nil, false
	}
	firstRecent := recentEntries[0].Message.Role
	if firstRecent == llm.RoleUser || firstRecent == llm.RoleSystem {
		return candidates, nil, false
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		if candidates[i].Role == llm.RoleUser {
			return candidates[:i], candidates[i:], true
		}
	}
	return candidates, nil, false
}
