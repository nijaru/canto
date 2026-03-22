package context

import (
	"context"
	"fmt"
	"regexp"
	"strings"

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

// PrependRequestProcessor inserts a preview-safe request processor at the
// front of the processor chain.
func (b *Builder) PrependRequestProcessor(r RequestProcessor) {
	b.Prepend(wrapRequestProcessor(r))
}

// PrependMutator inserts a commit-time mutator at the front of the processor
// chain. Mutators default to session-side effects unless they expose a more
// specific effect description.
func (b *Builder) PrependMutator(m ContextMutator) {
	b.Prepend(wrapContextMutator(m))
}

// Append adds p at the end of the processor chain.
func (b *Builder) Append(p Processor) {
	b.processors = append(b.processors, p)
}

// AppendRequestProcessor adds a preview-safe request processor to the end of
// the processor chain.
func (b *Builder) AppendRequestProcessor(r RequestProcessor) {
	b.Append(wrapRequestProcessor(r))
}

// AppendMutator adds a commit-time mutator to the end of the processor chain.
// Mutators default to session-side effects unless they expose a more specific
// effect description.
func (b *Builder) AppendMutator(m ContextMutator) {
	b.Append(wrapContextMutator(m))
}

// InsertRequestProcessorsBeforeLast inserts preview-safe request processors
// immediately before the last processor. If the chain is empty, it appends
// them.
func (b *Builder) InsertRequestProcessorsBeforeLast(rs ...RequestProcessor) {
	if len(rs) == 0 {
		return
	}
	ps := make([]Processor, 0, len(rs))
	for _, r := range rs {
		ps = append(ps, wrapRequestProcessor(r))
	}
	b.InsertBeforeLast(ps...)
}

// InsertMutatorsBeforeLast inserts commit-time mutators immediately before the
// last processor. If the chain is empty, it appends them.
func (b *Builder) InsertMutatorsBeforeLast(ms ...ContextMutator) {
	if len(ms) == 0 {
		return
	}
	ps := make([]Processor, 0, len(ms))
	for _, m := range ms {
		ps = append(ps, wrapContextMutator(m))
	}
	b.InsertBeforeLast(ps...)
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

type requestProcessorBridge struct {
	request RequestProcessor
}

func wrapRequestProcessor(r RequestProcessor) Processor {
	return requestProcessorBridge{request: r}
}

func (b requestProcessorBridge) Process(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	return b.request.ApplyRequest(ctx, p, model, sess, req)
}

func (b requestProcessorBridge) Effects() ProcessorEffects {
	if b.request == nil {
		return ProcessorEffects{}
	}
	if d, ok := b.request.(EffectDescriber); ok {
		return d.Effects()
	}
	return ProcessorEffects{}
}

type contextMutatorBridge struct {
	mutator ContextMutator
}

func wrapContextMutator(m ContextMutator) Processor {
	return contextMutatorBridge{mutator: m}
}

func (b contextMutatorBridge) Process(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	if b.mutator == nil {
		return nil
	}
	return b.mutator.Mutate(ctx, p, model, sess)
}

func (b contextMutatorBridge) Effects() ProcessorEffects {
	if b.mutator == nil {
		return ProcessorEffects{}
	}
	if d, ok := b.mutator.(EffectDescriber); ok {
		return d.Effects()
	}
	return ProcessorEffects{Session: true}
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
			req.Messages = append(req.Messages, llm.Message{})
			copy(req.Messages[1:], req.Messages)
			req.Messages[0] = sys
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
			req.Messages = append(req.Messages, llm.Message{})
			copy(req.Messages[1:], req.Messages)
			req.Messages[0] = sys
			return nil
		},
	)
}

// knowledgeMemoryRegex matches an existing knowledge_memory block and any trailing newlines.
var knowledgeMemoryRegex = regexp.MustCompile(`(?s)<knowledge_memory>.*?</knowledge_memory>\n*`)

// KnowledgeMemory retrieves and injects matching knowledge items from the store based on a query.
// If query is empty, it uses the text of the last message in the session history.
func KnowledgeMemory(store *memory.CoreStore, query string, limit int) Processor {
	return ProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
			if store == nil {
				return nil
			}
			q := query
			if q == "" {
				messages := sess.Messages()
				for i := len(messages) - 1; i >= 0; i-- {
					if messages[i].Content != "" {
						q = messages[i].Content
						break
					}
				}
			}
			if q == "" {
				return nil
			}

			items, err := store.SearchKnowledge(ctx, q, limit)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return nil
			}

			var sb strings.Builder
			sb.WriteString("<knowledge_memory>\n")
			for _, item := range items {
				fmt.Fprintf(&sb, "---\n%s\n", item.Content)
			}
			sb.WriteString("</knowledge_memory>")
			memBlock := sb.String()

			// Prepend or replace system instruction if not already there
			for i, m := range req.Messages {
				if m.Role == llm.RoleSystem {
					if loc := knowledgeMemoryRegex.FindStringIndex(m.Content); loc != nil {
						req.Messages[i].Content = m.Content[:loc[0]] + memBlock + "\n\n" + m.Content[loc[1]:]
					} else {
						req.Messages[i].Content = memBlock + "\n\n" + m.Content
					}
					return nil
				}
			}

			// Prepend new system message
			sys := llm.Message{Role: llm.RoleSystem, Content: memBlock}
			req.Messages = append(req.Messages, llm.Message{})
			copy(req.Messages[1:], req.Messages)
			req.Messages[0] = sys
			return nil
		},
	)
}
