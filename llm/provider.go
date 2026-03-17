package llm

import (
	"context"
	"time"

	"charm.land/catwalk/pkg/catwalk"
)

// Role defines the role of a message in the conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	// RoleDeveloper is a privileged instruction channel accepted by some models.
	// CapabilitiesProcessor converts system messages to this role when
	// Capabilities.SystemRole is RoleDeveloper.
	RoleDeveloper Role = "developer"
)

// Message represents a single message in the LLM conversation.
type Message struct {
	Role    Role       `json:"role"`
	Content string     `json:"content"`
	Name    string     `json:"name,omitempty"` // For tool output or identifying the assistant
	ToolID  string     `json:"tool_id,omitempty"`
	Calls   []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall represents a request from the LLM to call a tool.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // e.g., "function"
	Function struct {
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

// ResponseFormatType controls how the model formats its output.
type ResponseFormatType string

const (
	// ResponseFormatText is the default unstructured text output.
	ResponseFormatText ResponseFormatType = "text"
	// ResponseFormatJSON constrains output to valid JSON (no schema enforced).
	ResponseFormatJSON ResponseFormatType = "json_object"
	// ResponseFormatJSONSchema constrains output to JSON matching a schema.
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

// ResponseFormat constrains LLM output to structured JSON.
// Providers that do not support structured outputs ignore this field.
type ResponseFormat struct {
	Type ResponseFormatType `json:"type"`
	// Schema is the JSON Schema definition used when Type is ResponseFormatJSONSchema.
	Schema map[string]any `json:"schema,omitempty"`
	// Name identifies the schema for providers that require a name.
	Name   string `json:"name,omitempty"`
	Strict bool   `json:"strict,omitempty"`
}

// LLMRequest is the unified request sent to any provider.
type LLMRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Tools          []*ToolSpec     `json:"tools,omitempty"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	// ReasoningEffort controls the depth of internal reasoning for OpenAI o-series
	// models. Accepted values: "low", "medium", "high". Empty means provider default.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ThinkingBudget, when > 0, enables Anthropic extended thinking with the given
	// token budget (minimum 1024, must be less than MaxTokens).
	ThinkingBudget int `json:"thinking_budget,omitempty"`
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

// Capabilities describes what features a model supports.
// The pipeline uses these to adapt requests before they reach the provider.
type Capabilities struct {
	// Streaming indicates the model supports token-by-token streaming.
	Streaming bool
	// Tools indicates the model supports tool/function calling.
	Tools bool
	// Temperature indicates the model accepts a temperature parameter.
	// Models with internal fixed-temperature reasoning should set this to false.
	Temperature bool
	// SystemRole is the role to use when passing system-level instructions.
	// RoleSystem (default) passes them through unchanged.
	// RoleUser means the model has no system role; CapabilitiesProcessor injects
	// system content as user messages with an "Instructions:" prefix.
	// RoleDeveloper means the model accepts a privileged instruction channel
	// distinct from the assistant conversation.
	SystemRole Role
	// ReasoningEffort indicates the model accepts a hint for reasoning depth.
	// When true, LLMRequest.ReasoningEffort is forwarded to the provider.
	ReasoningEffort bool
	// Thinking indicates the model supports extended chain-of-thought reasoning
	// with an explicit token budget. When true, LLMRequest.ThinkingBudget is
	// forwarded to the provider.
	Thinking bool
}

// DefaultCapabilities returns full capabilities — suitable for most chat models.
func DefaultCapabilities() Capabilities {
	return Capabilities{
		Streaming:   true,
		Tools:       true,
		Temperature: true,
		SystemRole:  RoleSystem,
	}
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

	// CountTokens returns the number of tokens in the given messages for a specific model.
	CountTokens(ctx context.Context, model string, messages []Message) (int, error)

	// Cost calculates the cost in USD for the given usage on a specific model.
	Cost(ctx context.Context, model string, usage Usage) float64

	// Capabilities returns the feature set supported by the given model.
	// Use this to adapt requests for reasoning models that have constraints.
	Capabilities(model string) Capabilities

	// IsTransient returns true if the given error is retryable (e.g. 429, 503).
	IsTransient(err error) bool
}

// RetryConfig controls the backoff behavior for a RetryProvider.
type RetryConfig struct {
	MaxAttempts int
	MinInterval time.Duration
	MaxInterval time.Duration
	Multiplier  float64
}

// DefaultRetryConfig returns a safe default for production LLM usage.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		MinInterval: 1 * time.Second,
		MaxInterval: 10 * time.Second,
		Multiplier:  2.0,
	}
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

// RetryProvider wraps an LLM provider and automatically retries transient errors
// (rate limits, service unavailable) with exponential backoff.
type RetryProvider struct {
	Provider
	Config RetryConfig
}

// NewRetryProvider creates a new provider with the default retry policy.
func NewRetryProvider(p Provider) *RetryProvider {
	return &RetryProvider{
		Provider: p,
		Config:   DefaultRetryConfig(),
	}
}

func (r *RetryProvider) Generate(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	var resp *LLMResponse
	var err error
	interval := r.Config.MinInterval

	for i := 0; i < r.Config.MaxAttempts; i++ {
		resp, err = r.Provider.Generate(ctx, req)
		if err == nil {
			return resp, nil
		}

		if !r.Provider.IsTransient(err) || i == r.Config.MaxAttempts-1 {
			return nil, err
		}

		select {
		case <-time.After(interval):
			interval = time.Duration(float64(interval) * r.Config.Multiplier)
			if interval > r.Config.MaxInterval {
				interval = r.Config.MaxInterval
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, err
}

func (r *RetryProvider) Stream(ctx context.Context, req *LLMRequest) (Stream, error) {
	var s Stream
	var err error
	interval := r.Config.MinInterval

	for i := 0; i < r.Config.MaxAttempts; i++ {
		s, err = r.Provider.Stream(ctx, req)
		if err == nil {
			return s, nil
		}

		if !r.Provider.IsTransient(err) || i == r.Config.MaxAttempts-1 {
			return nil, err
		}

		select {
		case <-time.After(interval):
			interval = time.Duration(float64(interval) * r.Config.Multiplier)
			if interval > r.Config.MaxInterval {
				interval = r.Config.MaxInterval
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, err
}
