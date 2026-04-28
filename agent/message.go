package agent

import (
	"strings"

	"github.com/nijaru/canto/llm"
)

func hasAssistantPayload(msg llm.Message) bool {
	return strings.TrimSpace(msg.Content) != "" ||
		strings.TrimSpace(msg.Reasoning) != "" ||
		len(msg.ThinkingBlocks) > 0 ||
		len(msg.Calls) > 0
}
