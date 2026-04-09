package eval

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// PlanAdherence scores whether the full trajectory stayed aligned with the
// intended plan and task objective.
type PlanAdherence struct {
	NameText string
	Criteria string
	Model    string
	Provider llm.Provider
}

// Name returns the scorer identifier.
func (p *PlanAdherence) Name() string {
	if p != nil && p.NameText != "" {
		return p.NameText
	}
	return "plan_adherence"
}

// ScoreRun executes an LLM judge over the full trajectory.
func (p *PlanAdherence) ScoreRun(ctx context.Context, log *session.RunLog) (float64, error) {
	if p == nil {
		return 0, fmt.Errorf("eval: nil plan adherence scorer")
	}
	if p.Provider == nil {
		return 0, fmt.Errorf("eval: nil provider")
	}

	req := &llm.Request{
		Model: p.Model,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: p.renderPrompt(log)},
		},
	}

	resp, err := p.Provider.Generate(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("plan_adherence %q: %w", p.Name(), err)
	}

	matches := scoreRegex.FindStringSubmatch(resp.Content)
	if len(matches) < 2 {
		return 0, fmt.Errorf(
			"plan_adherence %q: could not find score in response: %q",
			p.Name(),
			resp.Content,
		)
	}

	score, err := parseScore(matches[1])
	if err != nil {
		return 0, fmt.Errorf("plan_adherence %q: %w", p.Name(), err)
	}
	return score, nil
}

func (p *PlanAdherence) renderPrompt(log *session.RunLog) string {
	var sb strings.Builder
	sb.WriteString("Evaluate the following agent trajectory according to these criteria:\n")
	if p.Criteria != "" {
		sb.WriteString(p.Criteria)
		sb.WriteByte('\n')
	}
	sb.WriteString("\n### Run Summary\n")
	fmt.Fprintf(&sb, "Session: %s\nAgent: %s\nTurns: %d\nTotal Cost: %.6f\n",
		log.SessionID, log.AgentID, len(log.Turns), log.TotalCost)
	if len(log.ChildRuns) > 0 {
		sb.WriteString("Child Runs:\n")
		for _, child := range log.ChildRuns {
			fmt.Fprintf(&sb, "- %s (%s): %s\n", child.ChildID, child.Status, child.Summary)
		}
	}
	sb.WriteString("\n### Trajectory\n")
	for i, turn := range log.Turns {
		fmt.Fprintf(&sb, "Turn %d [%s]\n", i+1, turn.TurnID)
		sb.WriteString("Input:\n")
		for _, msg := range turn.Input {
			fmt.Fprintf(&sb, "- %s: %s\n", msg.Role, msg.Content)
		}
		fmt.Fprintf(&sb, "Reasoning: %s\n", turn.Output.Reasoning)
		fmt.Fprintf(&sb, "Content: %s\n", turn.Output.Content)
		if len(turn.ToolCalls) > 0 {
			sb.WriteString("Tool Calls:\n")
			for _, call := range turn.ToolCalls {
				fmt.Fprintf(&sb, "- %s %s\n", call.Function.Name, call.Function.Arguments)
			}
		}
		if len(turn.ToolResults) > 0 {
			sb.WriteString("Tool Results:\n")
			for _, msg := range turn.ToolResults {
				fmt.Fprintf(&sb, "- %s: %s\n", msg.Role, msg.Content)
			}
		}
		sb.WriteByte('\n')
	}
	sb.WriteString("End your evaluation with exactly: Score: X.X/1.0\n")
	return sb.String()
}

func parseScore(text string) (float64, error) {
	score, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, err
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score, nil
}
