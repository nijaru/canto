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
	processors []Processor
}

// NewBuilder creates a new builder with the default processor chain.
func NewBuilder(processors ...Processor) *Builder {
	return &Builder{
		processors: processors,
	}
}

// Processors returns a copy of the current processor chain.
func (b *Builder) Processors() []Processor {
	res := make([]Processor, len(b.processors))
	copy(res, b.processors)
	return res
}

// Build executes the commit-time pipeline to transform the session and request.
func (b *Builder) Build(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	return b.BuildCommit(ctx, p, model, sess, req)
}

// BuildPreview builds a request using only preview-safe request processors.
func (b *Builder) BuildPreview(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	pipeline, err := b.previewPipeline()
	if err != nil {
		return err
	}
	return pipeline.BuildPreview(ctx, p, model, sess, req)
}

// BuildCommit runs commit-time mutation first and then rebuilds the request
// from the updated session state.
func (b *Builder) BuildCommit(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	pipeline, err := b.commitPipeline()
	if err != nil {
		return err
	}
	return pipeline.BuildCommit(ctx, p, model, sess, req)
}

// Effects returns the aggregate side effects of the current processor chain.
func (b *Builder) Effects() ProcessorEffects {
	var effects ProcessorEffects
	for _, p := range b.processors {
		effects = effects.merge(EffectsOf(p))
	}
	return effects
}

// Prepend inserts p at the front of the processor chain.
func (b *Builder) Prepend(p Processor) {
	b.processors = append([]Processor{p}, b.processors...)
}

// Append adds p at the end of the processor chain.
func (b *Builder) Append(p Processor) {
	b.processors = append(b.processors, p)
}

// InsertBeforeLast inserts processors into the chain immediately before the
// last processor. If the chain is empty, it appends them.
func (b *Builder) InsertBeforeLast(ps ...Processor) {
	if len(b.processors) == 0 {
		b.processors = append(b.processors, ps...)
		return
	}
	// Insert before the last processor (e.g. Capabilities).
	n := len(b.processors)
	tail := b.processors[n-1]
	merged := make([]Processor, 0, n-1+len(ps)+1)
	merged = append(merged, b.processors[:n-1]...)
	merged = append(merged, ps...)
	merged = append(merged, tail)
	b.processors = merged
}

func (b *Builder) previewPipeline() (*Pipeline, error) {
	pipeline := NewPipeline()
	for _, proc := range b.processors {
		rp, err := adaptRequestProcessor(proc)
		if err != nil {
			return nil, fmt.Errorf("preview pipeline: %w", err)
		}
		pipeline.AddRequestProcessor(rp)
	}
	return pipeline, nil
}

func (b *Builder) commitPipeline() (*Pipeline, error) {
	pipeline := NewPipeline()
	for _, proc := range b.processors {
		if EffectsOf(proc).HasSideEffects() {
			pipeline.AddMutator(adaptMutator(proc))
			continue
		}
		rp, err := adaptRequestProcessor(proc)
		if err != nil {
			return nil, fmt.Errorf("commit pipeline: %w", err)
		}
		pipeline.AddRequestProcessor(rp)
	}
	return pipeline, nil
}

// History appends the effective model-visible session history to the request.
func History() Processor {
	return ProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
			messages, err := sess.EffectiveMessages()
			if err != nil {
				return err
			}
			req.Messages = append(req.Messages, messages...)
			return nil
		},
	)
}

// Tools appends tool definitions to the LLM request.
func Tools(reg *tool.Registry) Processor {
	return ProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
			if reg == nil {
				return nil
			}
			req.Tools = append(req.Tools, reg.Specs()...)
			return nil
		},
	)
}

// Instructions prepends instructions as a system message.
func Instructions(instructions string) Processor {
	return ProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
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
func CoreMemoryProcessor(store *memory.CoreStore) Processor {
	return ProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
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
