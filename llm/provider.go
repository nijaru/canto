package llm

import (
	"context"

	"charm.land/catwalk/pkg/catwalk"
)

// Role defines the role of a message in the conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single message in the LLM conversation.
type Message struct {
	Role    Role        `json:"role"`
	Content string      `json:"content"`
	Name    string      `json:"name,omitempty"` // For tool output or identifying the assistant
	ToolID  string      `json:"tool_id,omitempty"`
	Calls   []ToolCall  `json:"tool_calls,omitempty"`
}

// ToolCall represents a request from the LLM to call a tool.
type ToolCall struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // e.g., "function"
	Function  struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

// ToolSpec represents a tool that can be called by the LLM.
type ToolSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"` // JSON Schema
}

// LLMRequest is the unified request sent to any provider.
type LLMRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolSpec `json:"tools,omitempty"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// LLMResponse is the unified response from any provider.
type LLMResponse struct {
	Content string     `json:"content"`
	Calls   []ToolCall `json:"tool_calls,omitempty"`
	Usage   Usage      `json:"usage"`
}

// Usage tracks token consumption and cost.
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	Cost         float64 `json:"cost,omitempty"` // USD
}

// Provider defines the interface for an LLM backend.
type Provider interface {
	// ID returns the unique identifier for this provider.
	ID() string

	// Generate executes a non-streaming completion request.
	Generate(ctx context.Context, req *LLMRequest) (*LLMResponse, error)

	// Stream executes a streaming completion request.
	Stream(ctx context.Context, req *LLMRequest) (Stream, error)

	// Models returns the list of models supported by this provider.
	Models(ctx context.Context) ([]catwalk.Model, error)
}

// Stream defines the interface for a streaming LLM response.
type Stream interface {
	// Next returns the next chunk of the response.
	// It returns (nil, false) when the stream is exhausted.
	Next() (*Chunk, bool)
	// Err returns the first error encountered during streaming.
	Err() error
	// Close closes the stream.
	Close() error
}

// Chunk represents a single piece of a streaming response.
type Chunk struct {
	Content string     `json:"content"`
	Calls   []ToolCall `json:"tool_calls,omitempty"`
}
