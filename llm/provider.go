package llm

import (
	"context"
	"time"
)

// Role defines the role of a message in the conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	// RoleDeveloper is a privileged instruction channel accepted by some models.
	// Capabilities converts system messages to this role when
	// Capabilities.SystemRole is RoleDeveloper.
	RoleDeveloper Role = "developer"
)

// CacheControl defines the caching behavior for a block of content.
type CacheControl struct {
	Type string `json:"type"` // e.g. "ephemeral"
}

// ThinkingBlock represents a reasoning block from a provider like Anthropic.
type ThinkingBlock struct {
	Type      string `json:"type"` // "thinking" or "redacted_thinking"
	Thinking  string `json:"thinking,omitzero"`
	Signature string `json:"signature,omitzero"`
}

// Message represents a single message in the LLM conversation.
type Message struct {
	Role           Role            `json:"role"`
	Content        string          `json:"content"`
	Reasoning      string          `json:"reasoning,omitzero"`
	ThinkingBlocks []ThinkingBlock `json:"thinking_blocks,omitzero"`
	Name           string          `json:"name,omitzero"` // For tool output or identifying the assistant
	ToolID         string          `json:"tool_id,omitzero"`
	Calls          []Call          `json:"tool_calls,omitzero"`
	CacheControl   *CacheControl   `json:"cache_control,omitzero"`
}

// Call represents a request from the LLM to call a tool.
type Call struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // e.g., "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

// Spec represents a tool that can be called by the LLM.
type Spec struct {
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Parameters   any           `json:"parameters"` // JSON Schema
	CacheControl *CacheControl `json:"cache_control,omitzero"`
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
	Schema map[string]any `json:"schema,omitzero"`
	// Name identifies the schema for providers that require a name.
	Name   string `json:"name,omitzero"`
	Strict bool   `json:"strict,omitzero"`
}

// Request is the unified request sent to any provider.
type Request struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Tools          []*Spec         `json:"tools,omitzero"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitzero"`
	ResponseFormat *ResponseFormat `json:"response_format,omitzero"`
	// CachePrefixMessages is the number of leading messages Canto expects to
	// stay stable across ordinary turn growth. Use Request's message insertion
	// methods when changing Messages so this boundary stays aligned. Provider
	// adapters ignore it; prompt cache helpers use it to place provider-neutral
	// cache markers.
	CachePrefixMessages int `json:"-"`
	// ReasoningEffort controls the depth of internal reasoning for OpenAI o-series
	// models. Accepted values: "low", "medium", "high". Empty means provider default.
	ReasoningEffort string `json:"reasoning_effort,omitzero"`
	// ThinkingBudget, when > 0, enables Anthropic extended thinking with the given
	// token budget (minimum 1024, must be less than MaxTokens).
	ThinkingBudget int `json:"thinking_budget,omitzero"`
}

// Response is the unified response from any provider.
type Response struct {
	Content        string          `json:"content"`
	Reasoning      string          `json:"reasoning,omitzero"`
	ThinkingBlocks []ThinkingBlock `json:"thinking_blocks,omitzero"`
	Calls          []Call          `json:"tool_calls,omitzero"`
	Usage          Usage           `json:"usage"`
}

// Usage tracks token consumption and cost.
type Usage struct {
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int     `json:"cache_creation_tokens,omitempty"`
	TotalTokens         int     `json:"total_tokens"`
	Cost                float64 `json:"cost,omitzero"` // USD
}

// Model describes an LLM model exposed by a provider.
type Model struct {
	ID            string  `json:"id"`
	ContextWindow int     `json:"context_window,omitzero"`
	CostPer1MIn   float64 `json:"cost_per_1m_in,omitzero"`
	CostPer1MOut  float64 `json:"cost_per_1m_out,omitzero"`
}

// ProviderConfig captures the shared endpoint/auth/model metadata used by
// Canto's built-in provider adapters.
type ProviderConfig struct {
	ID             string
	APIKey         string
	APIEndpoint    string
	DefaultHeaders map[string]string
	Models         []Model
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
	// RoleUser means the model has no system role; Capabilities injects
	// system content as user messages with an "Instructions:" prefix.
	// RoleDeveloper means the model accepts a privileged instruction channel
	// distinct from the assistant conversation.
	SystemRole Role
	// ReasoningEffort indicates the model accepts a hint for reasoning depth.
	// When true, Request.ReasoningEffort is forwarded to the provider.
	ReasoningEffort bool
	// Thinking indicates the model supports extended chain-of-thought reasoning
	// with an explicit token budget. When true, Request.ThinkingBudget is
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

// GenerateFromStream collects chunks from a stream and assembles an Response.
// It is intended for use by Provider implementations to avoid duplicating
// the complex logic of assembling streaming chunks.
func GenerateFromStream(s Stream) (*Response, error) {
	defer s.Close()
	var resp Response
	// toolCallIndices tracks tool calls by their ID to handle deltas correctly.
	toolCallIndices := make(map[string]int)
	// thinkingBlockIndices tracks thinking blocks by their index to handle deltas if needed.
	// For now, most streaming thinking blocks don't have a unique ID, but Anthropic
	// may emit multiple blocks.
	thinkingBlockIndices := make(map[int]int)

	for {
		chunk, ok := s.Next()
		if !ok {
			break
		}
		resp.Content += chunk.Content
		resp.Reasoning += chunk.Reasoning
		for i, block := range chunk.ThinkingBlocks {
			if idx, ok := thinkingBlockIndices[i]; ok {
				resp.ThinkingBlocks[idx].Thinking += block.Thinking
				if block.Signature != "" {
					resp.ThinkingBlocks[idx].Signature = block.Signature
				}
			} else {
				thinkingBlockIndices[i] = len(resp.ThinkingBlocks)
				resp.ThinkingBlocks = append(resp.ThinkingBlocks, block)
			}
		}
		for _, call := range chunk.Calls {
			if idx, ok := toolCallIndices[call.ID]; ok {
				// Update existing call. If the chunk contains the full state,
				// we overwrite; if it's a delta, we should append.
				// For now, we assume the provider normalization layer (like
				// OpenAIStream) handles the delta-to-full-state conversion
				// and we just take the latest state.
				resp.Calls[idx] = call
			} else {
				// New call
				toolCallIndices[call.ID] = len(resp.Calls)
				resp.Calls = append(resp.Calls, call)
			}
		}
		if chunk.Usage != nil {
			resp.Usage = *chunk.Usage
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Provider defines the interface for an LLM backend.
type Provider interface {
	// ID returns the unique identifier for this provider.
	ID() string

	// Generate executes a non-streaming completion request. Providers receive a
	// neutral request draft and should prepare a provider-specific copy with
	// PrepareRequestForCapabilities before converting it to wire format.
	Generate(ctx context.Context, req *Request) (*Response, error)

	// Stream executes a streaming completion request. Providers receive a neutral
	// request draft and should prepare a provider-specific copy with
	// PrepareRequestForCapabilities before converting it to wire format.
	Stream(ctx context.Context, req *Request) (Stream, error)

	// Models returns the list of models supported by this provider.
	Models(ctx context.Context) ([]Model, error)

	// CountTokens returns the number of tokens in the given messages for a specific model.
	CountTokens(ctx context.Context, model string, messages []Message) (int, error)

	// Cost calculates the cost in USD for the given usage on a specific model.
	Cost(ctx context.Context, model string, usage Usage) float64

	// Capabilities returns the feature set supported by the given model.
	Capabilities(model string) Capabilities

	// IsTransient returns true if the given error is retryable (e.g. 429, 503).
	IsTransient(err error) bool

	// IsContextOverflow returns true if the error indicates the model's context
	// window was exceeded (e.g. context_length_exceeded, 400 bad request with
	// overflow message).
	IsContextOverflow(err error) bool
}

// RetryConfig controls the backoff behavior for a RetryProvider.
type RetryConfig struct {
	MaxAttempts               int
	MinInterval               time.Duration
	MaxInterval               time.Duration
	Multiplier                float64
	RetryForever              bool
	RetryForeverTransportOnly bool
	OnRetry                   func(RetryEvent)
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

// RetryEvent describes a transient provider failure that will be retried.
type RetryEvent struct {
	Attempt int
	Delay   time.Duration
	Err     error
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
	Content        string          `json:"content"`
	Reasoning      string          `json:"reasoning,omitempty"`
	ThinkingBlocks []ThinkingBlock `json:"thinking_blocks,omitempty"`
	Calls          []Call          `json:"tool_calls,omitempty"`
	// Usage is populated in the final chunk(s) if supported by the provider.
	Usage *Usage `json:"usage,omitempty"`
}

// RetryProvider wraps an LLM provider and automatically retries transient
// errors with exponential backoff.
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

func normalizedRetryConfig(cfg RetryConfig) RetryConfig {
	defaults := DefaultRetryConfig()
	if cfg.RetryForever && !cfg.RetryForeverTransportOnly {
		cfg.MaxAttempts = 0
	} else if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = defaults.MinInterval
	}
	if cfg.MaxInterval <= 0 {
		cfg.MaxInterval = defaults.MaxInterval
	}
	if cfg.MaxInterval < cfg.MinInterval {
		cfg.MaxInterval = cfg.MinInterval
	}
	if cfg.Multiplier <= 0 {
		cfg.Multiplier = defaults.Multiplier
	}
	return cfg
}

func retryLimitReached(cfg RetryConfig, attempt int, err error) bool {
	if cfg.RetryForever {
		if !cfg.RetryForeverTransportOnly {
			return false
		}
		if IsTransientTransportError(err) {
			return false
		}
	}
	return attempt >= cfg.MaxAttempts
}

func notifyRetry(cfg RetryConfig, event RetryEvent) {
	if cfg.OnRetry != nil {
		cfg.OnRetry(event)
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *RetryProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	var resp *Response
	var err error
	cfg := normalizedRetryConfig(r.Config)
	interval := cfg.MinInterval

	for i := 0; ; i++ {
		resp, err = r.Provider.Generate(ctx, req)
		if err == nil {
			return resp, nil
		}

		if !r.Provider.IsTransient(err) || retryLimitReached(cfg, i+1, err) {
			return nil, err
		}

		notifyRetry(cfg, RetryEvent{Attempt: i + 1, Delay: interval, Err: err})
		if err := waitForRetry(ctx, interval); err != nil {
			return nil, err
		}
		interval = time.Duration(float64(interval) * cfg.Multiplier)
		if interval > cfg.MaxInterval {
			interval = cfg.MaxInterval
		}
	}
	return nil, err
}

func (r *RetryProvider) Stream(ctx context.Context, req *Request) (Stream, error) {
	var s Stream
	var err error
	cfg := normalizedRetryConfig(r.Config)
	interval := cfg.MinInterval

	for i := 0; ; i++ {
		s, err = r.Provider.Stream(ctx, req)
		if err == nil {
			return s, nil
		}

		if !r.Provider.IsTransient(err) || retryLimitReached(cfg, i+1, err) {
			return nil, err
		}

		notifyRetry(cfg, RetryEvent{Attempt: i + 1, Delay: interval, Err: err})
		if err := waitForRetry(ctx, interval); err != nil {
			return nil, err
		}
		interval = time.Duration(float64(interval) * cfg.Multiplier)
		if interval > cfg.MaxInterval {
			interval = cfg.MaxInterval
		}
	}
	return nil, err
}
