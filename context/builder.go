package context

import (
	"context"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// Builder implements the context engineering pipeline.
type Builder struct {
	Processors []ContextProcessor
}

// NewBuilder creates a new builder with the default processor chain.
func NewBuilder(processors ...ContextProcessor) *Builder {
	return &Builder{
		Processors: processors,
	}
}

// Build executes the processor chain to transform the session and request.
func (b *Builder) Build(ctx context.Context, sess *session.Session, req *llm.LLMRequest) error {
	for _, p := range b.Processors {
		if err := p.Process(ctx, sess, req); err != nil {
			return err
		}
	}
	return nil
}

// HistoryProcessor appends the session event log to the LLM request messages.
func HistoryProcessor() ContextProcessor {
	return ProcessorFunc(func(ctx context.Context, sess *session.Session, req *llm.LLMRequest) error {
		// Extract all messages from the session
		messages := sess.Messages()
		req.Messages = append(req.Messages, messages...)
		return nil
	})
}

// ToolProcessor appends tool definitions to the LLM request.
func ToolProcessor(reg *tool.Registry) ContextProcessor {
	return ProcessorFunc(func(ctx context.Context, sess *session.Session, req *llm.LLMRequest) error {
		if reg == nil {
			return nil
		}
		req.Tools = append(req.Tools, reg.Specs()...)
		return nil
	})
}

// InstructionProcessor prepends instructions as a system message.
func InstructionProcessor(instructions string) ContextProcessor {
	return ProcessorFunc(func(ctx context.Context, sess *session.Session, req *llm.LLMRequest) error {
		if instructions == "" {
			return nil
		}
		
		// Prepend system instruction if not already there
		// Find first system message or prepend new one
		for i, m := range req.Messages {
			if m.Role == llm.RoleSystem {
				req.Messages[i].Content = instructions + "\n\n" + m.Content
				return nil
			}
		}
		
		// Prepend new system message
		sys := llm.Message{Role: llm.RoleSystem, Content: instructions}
		req.Messages = append([]llm.Message{sys}, req.Messages...)
		return nil
	})
}

// WorkspaceProcessor prepends project-wide instructions and persona from the workspace.
func WorkspaceProcessor(root string) ContextProcessor {
	return ProcessorFunc(func(ctx context.Context, sess *session.Session, req *llm.LLMRequest) error {
		// Use runtime's LoadWorkspace if needed, but we don't want to import runtime here
		// because runtime depends on context.
		// Instead, we should pass the prompts directly or have a generic interface.
		return nil
	})
}
