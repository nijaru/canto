package runtime

import (
	"context"

	"github.com/nijaru/canto/llm"
)

// ChannelAdapter is the interface that normalizes incoming requests from
// various external sources (HTTP, CLI, Slack, Telegram) into a standard
// format that the Canto framework can process, and vice-versa.
type ChannelAdapter interface {
	// Name returns the unique identifier for this channel (e.g., "http", "cli").
	Name() string

	// Listen starts the channel adapter listening for incoming messages.
	// It should block until the context is canceled or an error occurs.
	Listen(ctx context.Context, handler ChannelHandler) error
}

// ChannelHandler is a callback provided to a ChannelAdapter to process
// normalized incoming messages.
type ChannelHandler interface {
	Handle(ctx context.Context, req ChannelRequest) (*ChannelResponse, error)
}

// ChannelHandlerFunc is an adapter to allow the use of ordinary functions
// as channel handlers.
type ChannelHandlerFunc func(ctx context.Context, req ChannelRequest) (*ChannelResponse, error)

func (f ChannelHandlerFunc) Handle(
	ctx context.Context,
	req ChannelRequest,
) (*ChannelResponse, error) {
	return f(ctx, req)
}

// ChannelRequest represents a normalized incoming message from any channel.
type ChannelRequest struct {
	SessionID string
	AgentID   string
	Message   llm.Message
	Metadata  map[string]any
}

// ChannelResponse represents a normalized outgoing message to any channel.
type ChannelResponse struct {
	SessionID string
	Messages  []llm.Message
	Metadata  map[string]any
}
