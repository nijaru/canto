package context

import (
	"context"
	"fmt"
	"regexp"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// Builder implements the context engineering pipeline.
type Builder struct {
	requestProcessors []RequestProcessor
	mutators          []ContextMutator
}

// NewBuilder creates a new builder with the default request-shaping chain.
func NewBuilder(processors ...RequestProcessor) *Builder {
	return &Builder{requestProcessors: append([]RequestProcessor(nil), processors...)}
}

// RequestProcessors returns a copy of the current request-shaping chain.
func (b *Builder) RequestProcessors() []RequestProcessor {
	res := make([]RequestProcessor, len(b.requestProcessors))
	copy(res, b.requestProcessors)
	return res
}

// Mutators returns a copy of the current commit-time mutator chain.
func (b *Builder) Mutators() []ContextMutator {
	res := make([]ContextMutator, len(b.mutators))
	copy(res, b.mutators)
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
	return b.previewPipeline().BuildPreview(ctx, p, model, sess, req)
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

// Effects returns the aggregate side effects of the current mutator chain.
func (b *Builder) Effects() SideEffects {
	var effects SideEffects
	for _, proc := range b.requestProcessors {
		effects = effects.merge(requestProcessorEffects(proc))
	}
	for _, m := range b.mutators {
		effects = effects.merge(mutatorEffects(m))
	}
	return effects
}

// PrependRequestProcessors inserts preview-safe request processors at the
// front of the request-shaping chain.
func (b *Builder) PrependRequestProcessors(processors ...RequestProcessor) {
	if len(processors) == 0 {
		return
	}
	b.requestProcessors = append(
		append([]RequestProcessor(nil), processors...),
		b.requestProcessors...)
}

// AppendRequestProcessors adds preview-safe request processors to the end of
// the request-shaping chain.
func (b *Builder) AppendRequestProcessors(processors ...RequestProcessor) {
	b.requestProcessors = append(b.requestProcessors, processors...)
}

// InsertRequestProcessorsBeforeLast inserts preview-safe request processors
// immediately before the last request processor. If the chain is empty, it
// appends them.
func (b *Builder) InsertRequestProcessorsBeforeLast(processors ...RequestProcessor) {
	if len(processors) == 0 {
		return
	}
	if len(b.requestProcessors) == 0 {
		b.AppendRequestProcessors(processors...)
		return
	}
	n := len(b.requestProcessors)
	tail := b.requestProcessors[n-1]
	merged := make([]RequestProcessor, 0, n-1+len(processors)+1)
	merged = append(merged, b.requestProcessors[:n-1]...)
	merged = append(merged, processors...)
	merged = append(merged, tail)
	b.requestProcessors = merged
}

// PrependMutators inserts commit-time mutators at the front of the mutator chain.
func (b *Builder) PrependMutators(mutators ...ContextMutator) {
	if len(mutators) == 0 {
		return
	}
	b.mutators = append(append([]ContextMutator(nil), mutators...), b.mutators...)
}

// AppendMutators adds commit-time mutators to the end of the mutator chain.
func (b *Builder) AppendMutators(mutators ...ContextMutator) {
	b.mutators = append(b.mutators, mutators...)
}

func (b *Builder) previewPipeline() *Pipeline {
	return NewPipeline(b.requestProcessors...)
}

func (b *Builder) commitPipeline() (*Pipeline, error) {
	if err := validateCompactionOrder(b.mutators); err != nil {
		return nil, err
	}

	pipeline := NewPipeline(b.requestProcessors...)
	for _, m := range b.mutators {
		pipeline.AddMutator(m)
	}
	return pipeline, nil
}

func validateCompactionOrder(mutators []ContextMutator) error {
	var hasOffloader bool
	var hasSummarizer bool
	var seenSummarizer bool
	offloaderBeforeSummarizer := true

	for _, mutator := range mutators {
		if c, ok := mutator.(Compactor); ok {
			strategy := c.CompactionStrategy()
			if strategy == "offload" {
				hasOffloader = true
				if seenSummarizer {
					offloaderBeforeSummarizer = false
				}
			} else if strategy == "summarize" {
				hasSummarizer = true
				seenSummarizer = true
			}
		}
	}

	if hasSummarizer && !hasOffloader {
		return fmt.Errorf(
			"commit pipeline: compaction requires offloader before summarizer (never skip to summarize)",
		)
	}
	if hasSummarizer && hasOffloader && !offloaderBeforeSummarizer {
		return fmt.Errorf(
			"commit pipeline: compaction requires offloader to run before summarizer",
		)
	}
	return nil
}

// History appends the effective model-visible session history to the request.
func History() RequestProcessor {
	return RequestProcessorFunc(
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
func Tools(reg *tool.Registry) RequestProcessor {
	return RequestProcessorFunc(
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
func Instructions(instructions string) RequestProcessor {
	return RequestProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
			if instructions == "" {
				return nil
			}

			for i, m := range req.Messages {
				if m.Role == llm.RoleSystem {
					req.Messages[i].Content = instructions + "\n\n" + m.Content
					return nil
				}
			}

			sys := llm.Message{Role: llm.RoleSystem, Content: instructions}
			req.Messages = append(req.Messages, llm.Message{})
			copy(req.Messages[1:], req.Messages)
			req.Messages[0] = sys
			return nil
		},
	)
}

// injectSystemBlock prepends block into the first system message in req,
// replacing any existing match of blockRegex. If no system message exists,
// a new one is prepended.
func injectSystemBlock(req *llm.Request, blockRegex *regexp.Regexp, block string) {
	for i, m := range req.Messages {
		if m.Role != llm.RoleSystem {
			continue
		}
		if loc := blockRegex.FindStringIndex(m.Content); loc != nil {
			req.Messages[i].Content = m.Content[:loc[0]] + block + "\n\n" + m.Content[loc[1]:]
		} else {
			req.Messages[i].Content = block + "\n\n" + m.Content
		}
		return
	}
	sys := llm.Message{Role: llm.RoleSystem, Content: block}
	req.Messages = append(req.Messages, llm.Message{})
	copy(req.Messages[1:], req.Messages)
	req.Messages[0] = sys
}
