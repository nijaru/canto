// Package tracing provides OpenTelemetry instrumentation for canto agents.
//
// Span hierarchy per session/turn:
//
//	canto.session
//	└── canto.turn
//	    ├── canto.context   (context pipeline build)
//	    ├── gen_ai.chat     (provider.Generate)
//	    └── canto.tool.{name}  (tool executions, one per call)
//
// Typical usage:
//
//	provider := tracing.WrapProvider(baseProvider)
//	reg.Register(tracing.WrapTool(myTool))
//
//	ctx, span := tracing.StartSession(ctx, agentID, sessID, model)
//	defer tracing.EndSession(span, err)
//	ctx, span := tracing.StartTurn(ctx, agentID, sessID, model)
//	defer tracing.EndTurn(span, err)
//	result, err := agent.Turn(ctx, sess)
package tracing

import (
	"context"
	"encoding/json"
	"iter"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

const tracerName = "github.com/nijaru/canto"

// Tracer returns the canto tracer.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// StartSession starts a "canto.session" root span for a session execution.
func StartSession(
	ctx context.Context,
	agentID, sessionID, model string,
) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "canto.session",
		trace.WithAttributes(
			attribute.String("canto.agent_id", agentID),
			attribute.String("canto.session_id", sessionID),
			attribute.String("gen_ai.request.model", model),
		),
	)
}

// EndSession ends a session span, setting the error status if err is non-nil.
func EndSession(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// StartTurn starts a "canto.turn" child span and returns the derived context
// and span. The caller must call span.End() when the turn is complete.
func StartTurn(
	ctx context.Context,
	agentID, sessionID, model string,
) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "canto.turn",
		trace.WithAttributes(
			attribute.String("canto.agent_id", agentID),
			attribute.String("canto.session_id", sessionID),
			attribute.String("gen_ai.request.model", model),
		),
	)
}

// EndTurn ends a turn span, setting the error status if err is non-nil.
func EndTurn(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// StartGraph starts a "canto.graph" child span for a graph execution.
func StartGraph(ctx context.Context, graphID, sessionID string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "canto.graph",
		trace.WithAttributes(
			attribute.String("canto.graph_id", graphID),
			attribute.String("canto.session_id", sessionID),
		),
	)
}

// StartNode starts a "canto.graph.node" child span for a node in a graph.
func StartNode(ctx context.Context, nodeID string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "canto.graph.node",
		trace.WithAttributes(attribute.String("canto.node_id", nodeID)),
	)
}

// StartSwarm starts a "canto.swarm" child span for a swarm execution.
func StartSwarm(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "canto.swarm",
		trace.WithAttributes(attribute.String("canto.session_id", sessionID)),
	)
}

// StartSwarmRound starts a "canto.swarm.round" child span for a single round in a swarm.
func StartSwarmRound(ctx context.Context, round int) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "canto.swarm.round",
		trace.WithAttributes(attribute.Int("canto.swarm.round", round)),
	)
}

// StartAgent starts a "canto.agent" child span.
func StartAgent(ctx context.Context, agentID string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "canto.agent",
		trace.WithAttributes(attribute.String("canto.agent_id", agentID)),
	)
}

// StartContext starts a "canto.context" child span for the context-pipeline
// build phase. Call this immediately before builder.Build.
func StartContext(
	ctx context.Context,
	agentID, sessionID, model string,
) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "canto.context",
		trace.WithAttributes(
			attribute.String("canto.agent_id", agentID),
			attribute.String("canto.session_id", sessionID),
			attribute.String("gen_ai.request.model", model),
		),
	)
}

// EndContext ends a context-build span, setting the error status if err is non-nil.
func EndContext(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// WrapOptions configures the instrumentation behavior.
type WrapOptions struct {
	RecordMessages bool
}

// WrapOption is a functional option for WrapProvider and WrapTool.
type WrapOption func(*WrapOptions)

// WithRecordMessages enables recording of the raw prompt and completion messages
// in the telemetry spans (as gen_ai.input.messages and gen_ai.output.messages).
// By default, messages are dropped to prevent PII leakage.
func WithRecordMessages(record bool) WrapOption {
	return func(o *WrapOptions) {
		o.RecordMessages = record
	}
}

// wrappedProvider adds OpenTelemetry spans to provider Generate calls.
type wrappedProvider struct {
	inner          llm.Provider
	recordMessages bool
}

func (*wrappedProvider) tracingWrapped() {}

// WrapProvider returns a Provider that records a "gen_ai.chat" child span on
// every Generate call. Stream calls are forwarded without instrumentation.
func WrapProvider(p llm.Provider, opts ...WrapOption) llm.Provider {
	if _, ok := p.(interface{ tracingWrapped() }); ok {
		return p
	}
	var options WrapOptions
	for _, opt := range opts {
		opt(&options)
	}
	return &wrappedProvider{
		inner:          p,
		recordMessages: options.RecordMessages,
	}
}

func (w *wrappedProvider) ID() string { return w.inner.ID() }

func (w *wrappedProvider) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	if err := llm.ValidateRequest(req); err != nil {
		return nil, err
	}

	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.request.model", req.Model),
		attribute.Int("gen_ai.request.message_count", len(req.Messages)),
	}

	if w.recordMessages {
		if b, err := json.Marshal(req.Messages); err == nil {
			attrs = append(attrs, attribute.String("gen_ai.input.messages", string(b)))
		}
	}

	ctx, span := Tracer().Start(ctx, "gen_ai.chat", trace.WithAttributes(attrs...))
	defer span.End()

	resp, err := w.inner.Generate(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	respAttrs := []attribute.KeyValue{
		attribute.Int("gen_ai.usage.input_tokens", resp.Usage.InputTokens),
		attribute.Int("gen_ai.usage.output_tokens", resp.Usage.OutputTokens),
		attribute.Int("gen_ai.usage.cache_read.input_tokens", resp.Usage.CacheReadTokens),
		attribute.Int("gen_ai.usage.cache_creation.input_tokens", resp.Usage.CacheCreationTokens),
		attribute.Int("gen_ai.response.tool_call_count", len(resp.Calls)),
	}
	if w.recordMessages {
		if b, err := json.Marshal(resp); err == nil {
			respAttrs = append(respAttrs, attribute.String("gen_ai.output.messages", string(b)))
		}
	}

	span.SetAttributes(respAttrs...)
	return resp, nil
}

func (w *wrappedProvider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if err := llm.ValidateRequest(req); err != nil {
		return nil, err
	}

	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.request.model", req.Model),
		attribute.Int("gen_ai.request.message_count", len(req.Messages)),
		attribute.Bool("gen_ai.request.stream", true),
	}

	if w.recordMessages {
		if b, err := json.Marshal(req.Messages); err == nil {
			attrs = append(attrs, attribute.String("gen_ai.input.messages", string(b)))
		}
	}

	ctx, span := Tracer().Start(ctx, "gen_ai.chat", trace.WithAttributes(attrs...))

	stream, err := w.inner.Stream(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return nil, err
	}

	return &wrappedStream{inner: stream, span: span, recordMessages: w.recordMessages}, nil
}

type wrappedStream struct {
	inner          llm.Stream
	span           trace.Span
	usage          llm.Usage
	recordMessages bool
	chunks         []llm.Chunk
}

func (w *wrappedStream) Next() (*llm.Chunk, bool) {
	chunk, ok := w.inner.Next()
	if ok {
		if chunk.Usage != nil {
			w.usage.InputTokens += chunk.Usage.InputTokens
			w.usage.OutputTokens += chunk.Usage.OutputTokens
			w.usage.TotalTokens += chunk.Usage.TotalTokens
			w.usage.CacheReadTokens += chunk.Usage.CacheReadTokens
			w.usage.CacheCreationTokens += chunk.Usage.CacheCreationTokens
		}

		if w.recordMessages {
			w.chunks = append(w.chunks, *chunk)
		}
	}
	return chunk, ok
}

func (w *wrappedStream) Err() error { return w.inner.Err() }
func (w *wrappedStream) Close() error {
	err := w.inner.Close()
	if err != nil {
		w.span.RecordError(err)
		w.span.SetStatus(codes.Error, err.Error())
	} else if serr := w.inner.Err(); serr != nil {
		w.span.RecordError(serr)
		w.span.SetStatus(codes.Error, serr.Error())
	}
	attrs := []attribute.KeyValue{
		attribute.Int("gen_ai.usage.input_tokens", w.usage.InputTokens),
		attribute.Int("gen_ai.usage.output_tokens", w.usage.OutputTokens),
		attribute.Int("gen_ai.usage.cache_read.input_tokens", w.usage.CacheReadTokens),
		attribute.Int("gen_ai.usage.cache_creation.input_tokens", w.usage.CacheCreationTokens),
	}

	if w.recordMessages && len(w.chunks) > 0 {
		if b, err := json.Marshal(w.chunks); err == nil {
			attrs = append(attrs, attribute.String("gen_ai.output.messages", string(b)))
		}
	}
	w.span.SetAttributes(attrs...)
	w.span.End()
	return err
}

func (w *wrappedProvider) Models(ctx context.Context) ([]llm.Model, error) {
	return w.inner.Models(ctx)
}

func (w *wrappedProvider) CountTokens(
	ctx context.Context,
	model string,
	messages []llm.Message,
) (int, error) {
	return w.inner.CountTokens(ctx, model, messages)
}

func (w *wrappedProvider) Cost(ctx context.Context, model string, usage llm.Usage) float64 {
	return w.inner.Cost(ctx, model, usage)
}

func (w *wrappedProvider) Capabilities(model string) llm.Capabilities {
	return w.inner.Capabilities(model)
}

func (w *wrappedProvider) IsTransient(err error) bool {
	return w.inner.IsTransient(err)
}

func (w *wrappedProvider) IsContextOverflow(err error) bool {
	return w.inner.IsContextOverflow(err)
}

// wrappedTool adds a "canto.tool.{name}" child span to a tool's Execute call.
type wrappedTool struct {
	inner tool.Tool
}

func (*wrappedTool) tracingWrapped() {}

// WrapTool returns a Tool that records a "canto.tool.{name}" child span on
// every Execute call. If the tool is a StreamingTool, the returned tool
// will also implement StreamingTool and instrument ExecuteStreaming.
func WrapTool(t tool.Tool) tool.Tool {
	if _, ok := t.(interface{ tracingWrapped() }); ok {
		return t
	}
	w := wrappedTool{inner: t}
	if st, ok := t.(tool.StreamingTool); ok {
		return &wrappedStreamingTool{wrappedTool: w, innerStreaming: st}
	}
	return &w
}

func (w *wrappedTool) Spec() llm.Spec { return w.inner.Spec() }

func (w *wrappedTool) Metadata() tool.Metadata {
	if mt, ok := w.inner.(tool.MetadataTool); ok {
		return mt.Metadata()
	}
	return tool.Metadata{}
}

func (w *wrappedTool) ApprovalRequirement(args string) (approval.Requirement, bool, error) {
	if at, ok := w.inner.(tool.ApprovalTool); ok {
		return at.ApprovalRequirement(args)
	}
	return approval.Requirement{}, false, nil
}

func (w *wrappedTool) Execute(ctx context.Context, args string) (string, error) {
	name := w.inner.Spec().Name
	ctx, span := Tracer().Start(ctx, "canto.tool."+name,
		trace.WithAttributes(attribute.String("canto.tool.name", name)),
	)
	defer span.End()

	out, err := w.inner.Execute(ctx, args)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return out, err
}

type wrappedStreamingTool struct {
	wrappedTool
	innerStreaming tool.StreamingTool
}

func (*wrappedStreamingTool) tracingWrapped() {}

func (w *wrappedStreamingTool) ExecuteStreaming(
	ctx context.Context,
	args string,
) iter.Seq2[string, error] {
	name := w.inner.Spec().Name
	ctx, span := Tracer().Start(ctx, "canto.tool."+name,
		trace.WithAttributes(
			attribute.String("canto.tool.name", name),
			attribute.Bool("canto.tool.streaming", true),
		),
	)

	return func(yield func(string, error) bool) {
		defer span.End()
		var buf strings.Builder
		for delta, err := range w.innerStreaming.ExecuteStreaming(ctx, args) {
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				if !yield("", err) {
					return
				}
				return
			}
			buf.WriteString(delta)
			if !yield(delta, nil) {
				return
			}
		}
		span.SetAttributes(attribute.Int("canto.tool.output_len", buf.Len()))
	}
}
