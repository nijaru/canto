package eval

import (
	"fmt"
	"sort"
	"strings"
)

// MarkdownReport generates a beautiful Markdown summary of evaluation results.
func MarkdownReport(results []EvalResult) string {
	if len(results) == 0 {
		return "No evaluation results."
	}

	// Collect all unique score keys across all results
	scoreKeysMap := make(map[string]bool)
	for _, res := range results {
		for k := range res.Scores {
			scoreKeysMap[k] = true
		}
	}
	var keys []string
	for k := range scoreKeysMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString("## Evaluation Report\n\n")
	sb.WriteString("| Run ID | Agent | Turns | Cost |")
	for _, k := range keys {
		sb.WriteString(fmt.Sprintf(" %s |", k))
	}
	sb.WriteString("\n| --- | --- | --- | --- |")
	for range keys {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")

	for _, res := range results {
		sb.WriteString(fmt.Sprintf("| `%s` | %s | %d | $%.4f |",
			res.RunID, res.AgentID, res.TurnCount, res.TotalCost,
		))
		for _, k := range keys {
			score, ok := res.Scores[k]
			if !ok {
				sb.WriteString(" - |")
				continue
			}
			indicator := "⚪️"
			if score >= 0.8 {
				indicator = "🟢"
			} else if score >= 0.5 {
				indicator = "🟡"
			} else if score > 0 {
				indicator = "🔴"
			}
			sb.WriteString(fmt.Sprintf(" %s %.2f |", indicator, score))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
