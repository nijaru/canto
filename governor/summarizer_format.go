package governor

import (
	"fmt"
	"strings"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

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
