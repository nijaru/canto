package openai

import (
	"context"
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
}

// ID returns the unique identifier for this provider.
func (b *Base) ID() string {
	return string(b.Config.ID)
}

// Models returns the list of models supported by this provider.
func (b *Base) Models(ctx context.Context) ([]catwalk.Model, error) {
	return b.Config.Models, nil
}

// CountTokens returns an estimate of the tokens in the given messages.
func (b *Base) CountTokens(ctx context.Context, model string, messages []llm.Message) (int, error) {
	// For Phase 1, we use a simple heuristic.
	// In the future, this can use tiktoken or similar for specific models.
	total := 0
	for _, m := range messages {
		total += len(m.Content) / 4
		for _, call := range m.Calls {
			total += len(call.Function.Name)/4 + len(call.Function.Arguments)/4
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

	return openai.ChatCompletionRequest{
		Model:       req.Model,
		Messages:    messages,
		Tools:       tools,
		Temperature: float32(req.Temperature),
		MaxTokens:   req.MaxTokens,
	}
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
					call.Function.Arguments = delta.Function.Arguments
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
