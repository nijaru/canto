// Package obs provides OpenTelemetry instrumentation for canto agents.
//
// Span hierarchy per turn:
//
//	canto.turn
//	├── canto.context   (context pipeline build)
//	├── gen_ai.chat     (provider.Generate)
//	└── canto.tool.{name}  (tool executions, one per call)
//
// Typical usage:
//
//	provider := obs.WrapProvider(baseProvider)
//	reg.Register(obs.WrapTool(myTool))
//
//	ctx, span := obs.StartTurn(ctx, agentID, sessID, model)
//	defer obs.EndTurn(span, err)
//	result, err := agent.Turn(ctx, sess)
package obs

import (
	"context"

	"charm.land/catwalk/pkg/catwalk"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

const tracerName = "github.com/nijaru/canto"

// Tracer returns the canto tracer.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// StartTurn starts a "canto.turn" root span and returns the derived context
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
// build phase. Call this immediately before builder.Build and defer span.End.
func StartContext(ctx context.Context) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "canto.context")
}

// wrappedProvider adds OpenTelemetry spans to provider Generate calls.
type wrappedProvider struct {
	inner llm.Provider
}

// WrapProvider returns a Provider that records a "gen_ai.chat" child span on
// every Generate call. Stream calls are forwarded without instrumentation.
func WrapProvider(p llm.Provider) llm.Provider {
	return &wrappedProvider{inner: p}
}

func (w *wrappedProvider) ID() string { return w.inner.ID() }

func (w *wrappedProvider) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	ctx, span := Tracer().Start(ctx, "gen_ai.chat",
		trace.WithAttributes(
			attribute.String("gen_ai.request.model", req.Model),
			attribute.Int("gen_ai.request.message_count", len(req.Messages)),
		),
	)
	defer span.End()

	resp, err := w.inner.Generate(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetAttributes(
		attribute.Int("gen_ai.usage.input_tokens", resp.Usage.InputTokens),
		attribute.Int("gen_ai.usage.output_tokens", resp.Usage.OutputTokens),
		attribute.Int("gen_ai.response.tool_call_count", len(resp.Calls)),
	)
	return resp, nil
}

func (w *wrappedProvider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	ctx, span := Tracer().Start(ctx, "gen_ai.chat",
		trace.WithAttributes(
			attribute.String("gen_ai.request.model", req.Model),
			attribute.Int("gen_ai.request.message_count", len(req.Messages)),
			attribute.Bool("gen_ai.request.stream", true),
		),
	)

	stream, err := w.inner.Stream(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		return nil, err
	}

	return &wrappedStream{inner: stream, span: span}, nil
}

type wrappedStream struct {
	inner llm.Stream
	span  trace.Span
	usage llm.Usage
}

func (w *wrappedStream) Next() (*llm.Chunk, bool) {
	chunk, ok := w.inner.Next()
	if ok && chunk.Usage != nil {
		w.usage.InputTokens += chunk.Usage.InputTokens
		w.usage.OutputTokens += chunk.Usage.OutputTokens
		w.usage.TotalTokens += chunk.Usage.TotalTokens
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
	w.span.SetAttributes(
		attribute.Int("gen_ai.usage.input_tokens", w.usage.InputTokens),
		attribute.Int("gen_ai.usage.output_tokens", w.usage.OutputTokens),
	)
	w.span.End()
	return err
}

func (w *wrappedProvider) Models(ctx context.Context) ([]catwalk.Model, error) {
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

// wrappedTool adds a "canto.tool.{name}" child span to a tool's Execute call.
type wrappedTool struct {
	inner tool.Tool
}

// WrapTool returns a Tool that records a "canto.tool.{name}" child span on
// every Execute call. If the tool is a StreamingTool, the returned tool
// will also implement StreamingTool and instrument ExecuteStreaming.
func WrapTool(t tool.Tool) tool.Tool {
	w := wrappedTool{inner: t}
	if st, ok := t.(tool.StreamingTool); ok {
		return &wrappedStreamingTool{wrappedTool: w, innerStreaming: st}
	}
	return &w
}

func (w *wrappedTool) Spec() llm.Spec { return w.inner.Spec() }

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

func (w *wrappedStreamingTool) ExecuteStreaming(
	ctx context.Context,
	args string,
	emit func(string) error,
) (string, error) {
	name := w.inner.Spec().Name
	ctx, span := Tracer().Start(ctx, "canto.tool."+name,
		trace.WithAttributes(
			attribute.String("canto.tool.name", name),
			attribute.Bool("canto.tool.streaming", true),
		),
	)
	defer span.End()

	out, err := w.innerStreaming.ExecuteStreaming(ctx, args, emit)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return out, err
}
