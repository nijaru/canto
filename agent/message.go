package agent

import "github.com/nijaru/canto/llm"

func hasAssistantPayload(msg llm.Message) bool {
	return msg.Content != "" ||
		msg.Reasoning != "" ||
		len(msg.ThinkingBlocks) > 0 ||
		len(msg.Calls) > 0
}
