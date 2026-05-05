package tracing

import (
	"context"
	"iter"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

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
