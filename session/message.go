package session

import (
	"context"

	"github.com/nijaru/canto/llm"
)

// UserMessage creates a plain user message.
func UserMessage(content string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: content}
}

// SystemMessage creates a plain system message.
func SystemMessage(content string) llm.Message {
	return llm.Message{Role: llm.RoleSystem, Content: content}
}

// AssistantMessage creates a plain assistant message without tool calls.
func AssistantMessage(content string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: content}
}

// ToolMessage creates a tool result message.
func ToolMessage(name, toolID, content string) llm.Message {
	return llm.Message{
		Role:    llm.RoleTool,
		Name:    name,
		ToolID:  toolID,
		Content: content,
	}
}

// NewUserMessage creates a message-added event for a plain user message.
func NewUserMessage(sessionID string, content string) Event {
	return NewMessage(sessionID, UserMessage(content))
}

// AppendUser appends a plain user message to the session.
func (s *Session) AppendUser(ctx context.Context, content string) error {
	return s.Append(ctx, NewUserMessage(s.ID(), content))
}
