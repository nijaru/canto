package context

import (
	"context"
	"fmt"
	"regexp"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/memory"
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
func (b *Builder) Build(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
	req *llm.LLMRequest,
) error {
	for _, cp := range b.Processors {
		if err := cp.Process(ctx, p, model, sess, req); err != nil {
			return err
		}
	}
	return nil
}

// Prepend inserts p at the front of the processor chain.
func (b *Builder) Prepend(p ContextProcessor) {
	b.Processors = append([]ContextProcessor{p}, b.Processors...)
}

// Append adds p at the end of the processor chain.
func (b *Builder) Append(p ContextProcessor) {
	b.Processors = append(b.Processors, p)
}

// HistoryProcessor appends the session event log to the LLM request messages.
func HistoryProcessor() ContextProcessor {
	return ProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.LLMRequest) error {
			// Extract all messages from the session
			messages := sess.Messages()
			req.Messages = append(req.Messages, messages...)
			return nil
		},
	)
}

// ToolProcessor appends tool definitions to the LLM request.
func ToolProcessor(reg *tool.Registry) ContextProcessor {
	return ProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.LLMRequest) error {
			if reg == nil {
				return nil
			}
			req.Tools = append(req.Tools, reg.Specs()...)
			return nil
		},
	)
}

// InstructionProcessor prepends instructions as a system message.
func InstructionProcessor(instructions string) ContextProcessor {
	return ProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.LLMRequest) error {
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
		},
	)
}

// coreMemoryRegex matches an existing core_memory block and any trailing newlines.
var coreMemoryRegex = regexp.MustCompile(`(?s)<core_memory>.*?</core_memory>\n*`)

// CoreMemoryProcessor retrieves the core memory persona and injects it.
func CoreMemoryProcessor(store *memory.CoreStore) ContextProcessor {
	return ProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.LLMRequest) error {
			if store == nil {
				return nil
			}
			persona, err := store.GetPersona(ctx, sess.ID())
			if err != nil {
				return err
			}
			if persona == nil {
				return nil
			}

			memBlock := fmt.Sprintf(
				"<core_memory>\nAgent Name: %s\nPersona Context: %s\nDirectives: %s\n</core_memory>",
				persona.Name,
				persona.Description,
				persona.Directives,
			)

			// Prepend or replace system instruction if not already there
			for i, m := range req.Messages {
				if m.Role == llm.RoleSystem {
					if loc := coreMemoryRegex.FindStringIndex(m.Content); loc != nil {
						req.Messages[i].Content = m.Content[:loc[0]] + memBlock + "\n\n" + m.Content[loc[1]:]
					} else {
						req.Messages[i].Content = memBlock + "\n\n" + m.Content
					}
					return nil
				}
			}

			// Prepend new system message
			sys := llm.Message{Role: llm.RoleSystem, Content: memBlock}
			req.Messages = append([]llm.Message{sys}, req.Messages...)
			return nil
		},
	)
}
