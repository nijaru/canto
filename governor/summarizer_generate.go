package governor

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func (p *Summarizer) generateSummary(
	ctx context.Context,
	contextEntries []session.HistoryEntry,
	historyContent string,
	turnPrefixContent string,
	splitTurn bool,
) (string, error) {
	if !splitTurn {
		return p.generateHistorySummary(ctx, contextEntries, historyContent)
	}

	type result struct {
		text string
		err  error
	}
	historyCh := make(chan result, 1)
	turnCh := make(chan result, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		text, err := p.generateHistorySummary(ctx, contextEntries, historyContent)
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
	contextEntries []session.HistoryEntry,
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

	existingSummary, hasPrevious := extractPreviousSummary(contextEntries)

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
