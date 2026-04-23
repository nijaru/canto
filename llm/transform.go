package llm

import (
	"fmt"
	"strings"
)

const missingToolResultContent = "No result provided."

// TransformRequestForCapabilities adapts a unified request to a model's
// capability constraints while preserving transcript continuity when sessions
// move across providers.
func TransformRequestForCapabilities(req *Request, caps Capabilities) {
	if req == nil {
		return
	}

	if caps.SystemRole != RoleSystem {
		rewriteSystemMessages(req, caps.SystemRole)
	}
	if !caps.Temperature {
		req.Temperature = 0
	}

	normalizeToolIDs(req.Messages)
	if !caps.Thinking {
		flattenUnsupportedThinking(req.Messages)
	}
	req.Messages = synthesizeMissingToolResults(req.Messages)
}

func rewriteSystemMessages(req *Request, targetRole Role) {
	for i, m := range req.Messages {
		if m.Role != RoleSystem {
			continue
		}
		content := m.Content
		if targetRole == RoleUser {
			content = fmt.Sprintf("Instructions:\n%s", content)
		}
		req.Messages[i] = Message{
			Role:         targetRole,
			Content:      content,
			CacheControl: m.CacheControl,
		}
	}
}

func flattenUnsupportedThinking(messages []Message) {
	for i := range messages {
		msg := &messages[i]
		if msg.Reasoning == "" && len(msg.ThinkingBlocks) == 0 {
			continue
		}
		msg.Content = appendThinkingText(msg.Content, msg.Reasoning, msg.ThinkingBlocks)
		msg.Reasoning = ""
		msg.ThinkingBlocks = nil
	}
}

func appendThinkingText(content, reasoning string, blocks []ThinkingBlock) string {
	var parts []string
	if reasoning != "" {
		parts = append(parts, reasoning)
	}
	for _, block := range blocks {
		switch block.Type {
		case "thinking":
			if block.Thinking != "" {
				parts = append(parts, "<thinking>\n"+block.Thinking+"\n</thinking>")
			}
		case "redacted_thinking":
			// Redacted content is intentionally omitted when replaying across
			// providers that do not support native thinking blocks.
		}
	}
	if len(parts) == 0 {
		return content
	}
	if content == "" {
		return strings.Join(parts, "\n\n")
	}
	return content + "\n\n" + strings.Join(parts, "\n\n")
}

func synthesizeMissingToolResults(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}

	type pendingCall struct {
		id   string
		name string
	}

	var transformed []Message
	var pending []pendingCall

	flushPending := func() {
		for _, call := range pending {
			transformed = append(transformed, Message{
				Role:    RoleTool,
				Name:    call.name,
				ToolID:  call.id,
				Content: missingToolResultContent,
			})
		}
		pending = pending[:0]
	}

	for _, msg := range messages {
		if msg.Role != RoleTool && len(pending) > 0 {
			flushPending()
		}

		transformed = append(transformed, msg)

		if msg.Role == RoleAssistant {
			for _, call := range msg.Calls {
				pending = append(pending, pendingCall{
					id:   call.ID,
					name: call.Function.Name,
				})
			}
			continue
		}

		if msg.Role != RoleTool || msg.ToolID == "" || len(pending) == 0 {
			continue
		}

		for i := 0; i < len(pending); i++ {
			if pending[i].id != msg.ToolID {
				continue
			}
			pending = append(pending[:i], pending[i+1:]...)
			break
		}
	}

	if len(pending) > 0 {
		flushPending()
	}

	return transformed
}

func normalizeToolIDs(messages []Message) {
	assigned := make(map[string]string)
	used := make(map[string]int)

	for _, msg := range messages {
		for _, call := range msg.Calls {
			if _, ok := assigned[call.ID]; ok {
				continue
			}
			assigned[call.ID] = uniqueToolCallID(call.ID, used)
		}
	}

	for i := range messages {
		msg := &messages[i]
		for j := range msg.Calls {
			msg.Calls[j].ID = assigned[msg.Calls[j].ID]
		}
		if msg.Role == RoleTool && msg.ToolID != "" {
			if normalized, ok := assigned[msg.ToolID]; ok {
				msg.ToolID = normalized
				continue
			}
			normalized := uniqueToolCallID(msg.ToolID, used)
			assigned[msg.ToolID] = normalized
			msg.ToolID = normalized
		}
	}
}

func uniqueToolCallID(id string, used map[string]int) string {
	base := normalizeToolCallID(id)
	if base == "" {
		base = "tool"
	}

	n := used[base]
	if n == 0 {
		used[base] = 1
		return base
	}

	for {
		n++
		suffix := fmt.Sprintf("-%d", n)
		trimmed := base
		if len(trimmed)+len(suffix) > 64 {
			trimmed = trimmed[:64-len(suffix)]
		}
		candidate := trimmed + suffix
		if used[candidate] == 0 {
			used[base] = n
			used[candidate] = 1
			return candidate
		}
	}
}

func normalizeToolCallID(id string) string {
	if id == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	return strings.Trim(b.String(), "_")
}
