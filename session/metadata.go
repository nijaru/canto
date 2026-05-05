package session

import "context"

type contextKey int

const (
	metadataKey contextKey = iota
)

// WithMetadata attaches metadata to the context. This metadata will be
// automatically added to all events appended to a session using this context.
func WithMetadata(ctx context.Context, md map[string]any) context.Context {
	if len(md) == 0 {
		return ctx
	}
	existing, _ := ctx.Value(metadataKey).(map[string]any)
	if len(existing) == 0 {
		return context.WithValue(ctx, metadataKey, md)
	}

	merged := make(map[string]any, len(existing)+len(md))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range md {
		merged[k] = v
	}
	return context.WithValue(ctx, metadataKey, merged)
}

// MetadataFromContext retrieves metadata from the context.
func MetadataFromContext(ctx context.Context) map[string]any {
	md, _ := ctx.Value(metadataKey).(map[string]any)
	return md
}
