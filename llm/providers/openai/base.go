package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/sashabaranov/go-openai"
)

// Base implements the core OpenAI-compatible provider logic.
// Providers like Ollama, OpenRouter, and OpenAI itself can embed or wrap this.
type Base struct {
	Client *openai.Client
	Config catwalk.Provider
	// ModelCaps holds per-model capability overrides. Capabilities(model) looks
	// up this map before falling back to DefaultCapabilities. Populate with
	// DefaultModelCaps() to get known reasoning model entries.
	ModelCaps map[string]llm.Capabilities
}

// ID returns the unique identifier for this provider.
func (b *Base) ID() string {
	return string(b.Config.ID)
}

// Models returns the list of models supported by this provider.
func (b *Base) Models(ctx context.Context) ([]catwalk.Model, error) {
	return b.Config.Models, nil
}

// CountTokens estimates tokens using per-message overhead documented by OpenAI:
// 3 tokens for reply priming, 4 tokens per message for role/delimiter encoding.
// Content is estimated at 1 token per 4 chars.
func (b *Base) CountTokens(_ context.Context, _ string, messages []llm.Message) (int, error) {
	total := 3 // reply priming
	for _, m := range messages {
		total += 4 // per-message overhead
		total += (len(m.Content) + 3) / 4
		for _, call := range m.Calls {
			total += (len(call.Function.Name) + 3) / 4
			total += (len(call.Function.Arguments) + 3) / 4
		}
	}
	return total, nil
}

// Cost calculates the cost in USD based on the model configuration.
func (b *Base) Cost(ctx context.Context, model string, usage llm.Usage) float64 {
	for _, m := range b.Config.Models {
		if string(m.ID) == model {
			return (float64(usage.InputTokens) * m.CostPer1MIn / 1_000_000) + (float64(usage.OutputTokens) * m.CostPer1MOut / 1_000_000)
		}
	}
	return 0.0
}

// Generate handles the OpenAI-compatible chat completion.
func (b *Base) Generate(ctx context.Context, req *llm.LLMRequest) (*llm.LLMResponse, error) {
	resp, err := b.Client.CreateChatCompletion(ctx, b.ConvertRequest(req))
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned from %s", b.Config.ID)
	}

	choice := resp.Choices[0]
	usage := llm.Usage{
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		TotalTokens:  resp.Usage.TotalTokens,
	}
	usage.Cost = b.Cost(ctx, req.Model, usage)

	return &llm.LLMResponse{
		Content: choice.Message.Content,
		Calls:   b.ConvertToolCalls(choice.Message.ToolCalls),
		Usage:   usage,
	}, nil
}

// Stream handles the OpenAI-compatible streaming chat completion.
func (b *Base) Stream(ctx context.Context, req *llm.LLMRequest) (llm.Stream, error) {
	stream, err := b.Client.CreateChatCompletionStream(ctx, b.ConvertRequest(req))
	if err != nil {
		return nil, err
	}

	return &OpenAIStream{
		stream:      stream,
		activeCalls: make(map[int]llm.ToolCall),
	}, nil
}

// ConvertRequest transforms the unified LLMRequest into OpenAI's format.
func (b *Base) ConvertRequest(req *llm.LLMRequest) openai.ChatCompletionRequest {
	messages := make([]openai.ChatCompletionMessage, len(req.Messages))
	for i, m := range req.Messages {
		msg := openai.ChatCompletionMessage{
			Role:    string(m.Role),
			Content: m.Content,
			Name:    m.Name,
		}
		if len(m.Calls) > 0 {
			msg.ToolCalls = make([]openai.ToolCall, len(m.Calls))
			for j, call := range m.Calls {
				msg.ToolCalls[j] = openai.ToolCall{
					ID:   call.ID,
					Type: openai.ToolType(call.Type),
					Function: openai.FunctionCall{
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					},
				}
			}
		}
		if m.Role == llm.RoleTool {
			msg.ToolCallID = m.ToolID
		}
		messages[i] = msg
	}

	var tools []openai.Tool
	if len(req.Tools) > 0 {
		tools = make([]openai.Tool, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			}
		}
	}

	caps := b.Capabilities(req.Model)
	cr := openai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
	}
	if caps.Temperature {
		cr.Temperature = float32(req.Temperature)
		cr.MaxTokens = req.MaxTokens
	} else {
		// Models without temperature control require max_completion_tokens,
		// which counts both visible output and internal reasoning tokens.
		cr.MaxCompletionTokens = req.MaxTokens
	}
	if caps.ReasoningEffort && req.ReasoningEffort != "" {
		cr.ReasoningEffort = req.ReasoningEffort
	}
	if rf := req.ResponseFormat; rf != nil {
		switch rf.Type {
		case llm.ResponseFormatJSON:
			cr.ResponseFormat = &openai.ChatCompletionResponseFormat{
				Type: openai.ChatCompletionResponseFormatTypeJSONObject,
			}
		case llm.ResponseFormatJSONSchema:
			cr.ResponseFormat = &openai.ChatCompletionResponseFormat{
				Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
				JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
					Name:   rf.Name,
					Schema: schemaMarshaler(rf.Schema),
					Strict: rf.Strict,
				},
			}
		}
	}
	return cr
}

// Capabilities returns the feature set for the given model.
// It consults ModelCaps first; unknown models get DefaultCapabilities.
func (b *Base) Capabilities(model string) llm.Capabilities {
	if b.ModelCaps != nil {
		if caps, ok := b.ModelCaps[model]; ok {
			return caps
		}
	}
	return llm.DefaultCapabilities()
}

// IsTransient returns true if the error is a rate limit or server error.
func (b *Base) IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 429, 500, 502, 503, 504:
			return true
		}
	}
	return false
}

// DefaultModelCaps returns capability entries for well-known OpenAI reasoning
// models. Pass to Base.ModelCaps (or merge with your own overrides) when
// constructing a provider that will use these models.
func DefaultModelCaps() map[string]llm.Capabilities {
	reasoning := func(systemRole llm.Role) llm.Capabilities {
		return llm.Capabilities{
			Streaming:       true,
			Tools:           true,
			SystemRole:      systemRole,
			ReasoningEffort: true,
			// Temperature is false (zero value) — reasoning models ignore it.
		}
	}
	return map[string]llm.Capabilities{
		// o1 family: no system role — instructions become user messages.
		"o1":         reasoning(llm.RoleUser),
		"o1-mini":    reasoning(llm.RoleUser),
		"o1-preview": reasoning(llm.RoleUser),
		// o3/o4 families: privileged instruction role.
		"o3":      reasoning(llm.RoleDeveloper),
		"o3-mini": reasoning(llm.RoleDeveloper),
		"o3-pro":  reasoning(llm.RoleDeveloper),
		"o4-mini": reasoning(llm.RoleDeveloper),
	}
}

// schemaMarshaler wraps a map[string]any to implement json.Marshaler,
// as required by the OpenAI SDK's JSONSchema field.
type schemaMarshaler map[string]any

func (s schemaMarshaler) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(s))
}

// ConvertToolCalls transforms OpenAI tool calls into the unified format.
func (b *Base) ConvertToolCalls(calls []openai.ToolCall) []llm.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	res := make([]llm.ToolCall, len(calls))
	for i, call := range calls {
		res[i] = llm.ToolCall{
			ID:   call.ID,
			Type: string(call.Type),
		}
		res[i].Function.Name = call.Function.Name
		res[i].Function.Arguments = call.Function.Arguments
	}
	return res
}

// OpenAIStream implements llm.Stream for OpenAI-compatible providers.
type OpenAIStream struct {
	stream      *openai.ChatCompletionStream
	err         error
	activeCalls map[int]llm.ToolCall // Track partial calls by their index in the response
}

func (s *OpenAIStream) Next() (*llm.Chunk, bool) {
	for {
		resp, err := s.stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, false
			}
			s.err = err
			return nil, false
		}

		if len(resp.Choices) == 0 {
			continue
		}

		choice := resp.Choices[0]
		chunk := &llm.Chunk{
			Content: choice.Delta.Content,
		}

		if len(choice.Delta.ToolCalls) > 0 {
			chunk.Calls = make([]llm.ToolCall, len(choice.Delta.ToolCalls))
			for i, delta := range choice.Delta.ToolCalls {
				index := delta.Index
				if index == nil {
					// Fallback if index is missing (unlikely in modern OpenAI)
					idx := i
					index = &idx
				}

				call, ok := s.activeCalls[*index]
				if !ok {
					call = llm.ToolCall{
						Type: string(delta.Type),
					}
				}

				if delta.ID != "" {
					call.ID = delta.ID
				}
				if delta.Function.Name != "" {
					call.Function.Name = delta.Function.Name
				}
				if delta.Function.Arguments != "" {
					call.Function.Arguments += delta.Function.Arguments
				}

				s.activeCalls[*index] = call
				chunk.Calls[i] = call
			}
		}

		return chunk, true
	}
}

func (s *OpenAIStream) Err() error {
	return s.err
}

func (s *OpenAIStream) Close() error {
	s.stream.Close()
	return nil
}
