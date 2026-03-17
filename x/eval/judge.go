package eval

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Judge uses an LLM to score a transcript turn based on a rubric or criteria.
type Judge struct {
	NameText string
	Criteria string
	Model    string
	Provider llm.Provider
}

// Name returns the identifier for this judge.
func (j *Judge) Name() string { return j.NameText }

var scoreRegex = regexp.MustCompile(`(?i)Score:\s*([0-9.]+)/1\.0`)

// ScoreTurn executes an LLM call to evaluate the turn.
func (j *Judge) ScoreTurn(ctx context.Context, turn session.RunTurn) (float64, error) {
	prompt := fmt.Sprintf(`Evaluate the following agent turn according to these criteria:
%s

### Agent Turn
Input: %v
Reasoning: %s
Content: %s
Tool Calls: %d

### Instructions
Provide your evaluation and end with a score strictly formatted as "Score: X.X/1.0" where X.X is between 0.0 and 1.0.`,
		j.Criteria,
		turn.Input,
		turn.Output.Reasoning,
		turn.Output.Content,
		len(turn.ToolCalls),
	)

	req := &llm.Request{
		Model: j.Model,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	}

	resp, err := j.Provider.Generate(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("llm_judge %q: %w", j.NameText, err)
	}

	matches := scoreRegex.FindStringSubmatch(resp.Content)
	if len(matches) < 2 {
		return 0, fmt.Errorf(
			"llm_judge %q: could not find score in response: %q",
			j.NameText,
			resp.Content,
		)
	}

	score, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf(
			"llm_judge %q: invalid score format %q: %w",
			j.NameText,
			matches[1],
			err,
		)
	}

	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	return score, nil
}
