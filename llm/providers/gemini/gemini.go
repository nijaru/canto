package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/sashabaranov/go-openai"
)

// Provider implements the llm.Provider interface for Gemini via its OpenAI-compatible endpoint.
type Provider struct {
	client *openai.Client
	config catwalk.Provider
}

// NewProvider creates a new Gemini provider from a catwalk configuration.
func NewProvider(cfg catwalk.Provider) *Provider {
	apiKey := cfg.APIKey
	if apiKey == "$GEMINI_API_KEY" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}

	config := openai.DefaultConfig(apiKey)
	if cfg.APIEndpoint != "" {
		config.BaseURL = cfg.APIEndpoint
	} else {
		config.BaseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"
	}

	return &Provider{
		client: openai.NewClientWithConfig(config),
		config: cfg,
	}
}

func (p *Provider) ID() string {
	return string(p.config.ID)
}

func (p *Provider) Generate(ctx context.Context, req *llm.LLMRequest) (*llm.LLMResponse, error) {
	resp, err := p.client.CreateChatCompletion(ctx, p.convertRequest(req))
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned from gemini")
	}

	choice := resp.Choices[0]
	return &llm.LLMResponse{
		Content: choice.Message.Content,
		Calls:   p.convertToolCalls(choice.Message.ToolCalls),
		Usage: llm.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		},
	}, nil
}

func (p *Provider) Stream(ctx context.Context, req *llm.LLMRequest) (llm.Stream, error) {
	stream, err := p.client.CreateChatCompletionStream(ctx, p.convertRequest(req))
	if err != nil {
		return nil, err
	}

	return &Stream{stream: stream}, nil
}

func (p *Provider) Models(ctx context.Context) ([]catwalk.Model, error) {
	return p.config.Models, nil
}

func (p *Provider) convertRequest(req *llm.LLMRequest) openai.ChatCompletionRequest {
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

func (p *Provider) convertToolCalls(calls []openai.ToolCall) []llm.ToolCall {
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

// Stream implements llm.Stream for Gemini.
type Stream struct {
	stream *openai.ChatCompletionStream
	err    error
}

func (s *Stream) Next() (*llm.Chunk, bool) {
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
			for i, call := range choice.Delta.ToolCalls {
				chunk.Calls[i] = llm.ToolCall{
					ID:   call.ID,
					Type: string(call.Type),
				}
				chunk.Calls[i].Function.Name = call.Function.Name
				chunk.Calls[i].Function.Arguments = call.Function.Arguments
			}
		}

		return chunk, true
	}
}

func (s *Stream) Err() error {
	return s.err
}

func (s *Stream) Close() error {
	s.stream.Close()
	return nil
}
