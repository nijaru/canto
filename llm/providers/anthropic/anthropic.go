package anthropic

import (
	"context"
	"errors"
	"os"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/go-json-experiment/json"
	"github.com/nijaru/canto/llm"
)

// Provider implements the llm.Provider interface for Anthropic.
type Provider struct {
	client sdk.Client
	config llm.ProviderConfig
	// modelCaps holds per-model capability overrides. Capabilities(model) looks
	// up this map before falling back to DefaultCapabilities.
	modelCaps map[string]llm.Capabilities
}

// New creates an Anthropic provider with the given API key.
// Use NewProvider for full configuration control.
func New(apiKey string) *Provider {
	return NewProvider(llm.ProviderConfig{ID: "anthropic", APIKey: apiKey})
}

// DefaultModelCaps returns capability entries for Anthropic models that
// support extended thinking. Merge with your own overrides as needed.
func DefaultModelCaps() map[string]llm.Capabilities {
	thinking := func() llm.Capabilities {
		c := llm.DefaultCapabilities()
		c.Reasoning = llm.ReasoningCapabilities{
			Kind:            llm.ReasoningKindBudget,
			BudgetMinTokens: 1024,
		}
		return c
	}
	return map[string]llm.Capabilities{
		"claude-3-7-sonnet-20250219": thinking(),
		"claude-opus-4-5":            thinking(),
		"claude-sonnet-4-5":          thinking(),
		"claude-opus-4-20250514":     thinking(),
		"claude-sonnet-4-20250514":   thinking(),
	}
}

// NewProvider creates a new Anthropic provider from a provider configuration.
func NewProvider(cfg llm.ProviderConfig) *Provider {
	apiKey := cfg.APIKey
	if apiKey == "" || apiKey == "$ANTHROPIC_API_KEY" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if cfg.ID == "" {
		cfg.ID = "anthropic"
	}

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		// Required for tool use in some versions of the API.
		option.WithHeader("anthropic-beta", "tools-2024-05-16"),
	}
	if cfg.APIEndpoint != "" {
		opts = append(opts, option.WithBaseURL(cfg.APIEndpoint))
	}

	return &Provider{
		client:    sdk.NewClient(opts...),
		config:    cfg,
		modelCaps: DefaultModelCaps(),
	}
}

func (p *Provider) ID() string {
	return string(p.config.ID)
}

func (p *Provider) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	prepared, err := llm.PrepareRequestForCapabilities(req, p.Capabilities(req.Model))
	if err != nil {
		return nil, err
	}

	params := p.convertRequest(prepared)
	var opts []option.RequestOption
	if prepared.ThinkingBudget > 0 {
		opts = append(opts, option.WithHeader("anthropic-beta", "interleaved-thinking-2025-05-14"))
	}
	resp, err := p.client.Messages.New(ctx, params, opts...)
	if err != nil {
		return nil, err
	}

	usage := llm.Usage{
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
		TotalTokens:  int(resp.Usage.InputTokens + resp.Usage.OutputTokens),
	}
	usage.Cost = p.Cost(ctx, prepared.Model, usage)

	res := &llm.Response{
		Usage: usage,
	}

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			res.Content += block.Text
		case "thinking":
			res.Reasoning += block.Thinking
			res.ThinkingBlocks = append(res.ThinkingBlocks, llm.ThinkingBlock{
				Type:      "thinking",
				Thinking:  block.Thinking,
				Signature: block.Signature,
			})
		case "redacted_thinking":
			res.Reasoning += "<redacted_thinking />"
			res.ThinkingBlocks = append(res.ThinkingBlocks, llm.ThinkingBlock{
				Type:      "redacted_thinking",
				Signature: block.Signature,
			})
		case "tool_use":
			call := llm.Call{
				ID:   block.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      block.Name,
					Arguments: string(block.Input),
				},
			}
			res.Calls = append(res.Calls, call)

			// If this was a forced structured output, promote its input to Content.
			if rf := prepared.ResponseFormat; rf != nil && rf.Type == llm.ResponseFormatJSONSchema {
				name := rf.Name
				if name == "" {
					name = "json_response"
				}
				if block.Name == name {
					res.Content = string(block.Input)
				}
			}
			// "thinking" and "redacted_thinking" are internal reasoning blocks.
			// They are not exposed in Response to keep the API uniform.
		}
	}

	return res, nil
}

func (p *Provider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	prepared, err := llm.PrepareRequestForCapabilities(req, p.Capabilities(req.Model))
	if err != nil {
		return nil, err
	}

	params := p.convertRequest(prepared)
	var opts []option.RequestOption
	if prepared.ThinkingBudget > 0 {
		opts = append(opts, option.WithHeader("anthropic-beta", "interleaved-thinking-2025-05-14"))
	}
	stream := p.client.Messages.NewStreaming(ctx, params, opts...)

	targetName := ""
	if rf := prepared.ResponseFormat; rf != nil && rf.Type == llm.ResponseFormatJSONSchema {
		targetName = rf.Name
		if targetName == "" {
			targetName = "json_response"
		}
	}

	return &Stream{
		stream:     stream,
		targetName: targetName,
		model:      prepared.Model,
		p:          p,
		ctx:        ctx,
	}, nil
}

func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	return p.config.Models, nil
}

// CountTokens estimates tokens using per-message overhead heuristic.
// Accurate counting requires passing system + tools + messages to the
// Anthropic count_tokens API — deferred until Provider Capabilities are added.
func (p *Provider) CountTokens(_ context.Context, _ string, messages []llm.Message) (int, error) {
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
func (p *Provider) Cost(ctx context.Context, model string, usage llm.Usage) float64 {
	for _, m := range p.config.Models {
		if string(m.ID) == model {
			return (float64(usage.InputTokens) * m.CostPer1MIn / 1_000_000) + (float64(usage.OutputTokens) * m.CostPer1MOut / 1_000_000)
		}
	}
	return 0.0
}

func (p *Provider) convertRequest(req *llm.Request) sdk.MessageNewParams {
	var system []sdk.TextBlockParam
	var messages []sdk.MessageParam

	for i := 0; i < len(req.Messages); i++ {
		m := req.Messages[i]
		if m.Role == llm.RoleSystem {
			block := sdk.TextBlockParam{
				Text: m.Content,
				Type: constant.Text("text"),
			}
			if m.CacheControl != nil {
				block.CacheControl = sdk.NewCacheControlEphemeralParam()
			}
			system = append(system, block)
			continue
		}

		// Group consecutive tool results into one user message
		if m.Role == llm.RoleTool {
			var blocks []sdk.ContentBlockParamUnion
			for j := i; j < len(req.Messages); j++ {
				curr := req.Messages[j]
				if curr.Role != llm.RoleTool {
					i = j - 1
					break
				}
				block := sdk.NewToolResultBlock(curr.ToolID, curr.Content, false)
				if curr.CacheControl != nil {
					block.OfToolResult.CacheControl = sdk.NewCacheControlEphemeralParam()
				}
				blocks = append(blocks, block)
				if j == len(req.Messages)-1 {
					i = j
				}
			}
			messages = append(messages, sdk.NewUserMessage(blocks...))
			continue
		}

		// Handle normal messages and assistant thinking/content/tool calls
		var blocks []sdk.ContentBlockParamUnion
		for _, tb := range m.ThinkingBlocks {
			if tb.Type == "thinking" {
				blocks = append(blocks, sdk.NewThinkingBlock(tb.Thinking, tb.Signature))
			} else if tb.Type == "redacted_thinking" {
				blocks = append(blocks, sdk.NewRedactedThinkingBlock(tb.Signature))
			}
		}
		if m.Content != "" {
			block := sdk.NewTextBlock(m.Content)
			if m.CacheControl != nil {
				block.OfText.CacheControl = sdk.NewCacheControlEphemeralParam()
			}
			blocks = append(blocks, block)
		}
		for _, call := range m.Calls {
			block := sdk.NewToolUseBlock(call.ID, call.Function.Arguments, call.Function.Name)
			if m.CacheControl != nil {
				block.OfToolUse.CacheControl = sdk.NewCacheControlEphemeralParam()
			}
			blocks = append(blocks, block)
		}

		if m.Role == llm.RoleAssistant {
			messages = append(messages, sdk.NewAssistantMessage(blocks...))
		} else {
			messages = append(messages, sdk.NewUserMessage(blocks...))
		}
	}

	var tools []sdk.ToolUnionParam
	for _, t := range req.Tools {
		schema := p.convertSchema(t.Parameters)
		tool := sdk.ToolUnionParamOfTool(schema, t.Name)
		if t.Description != "" {
			tool.OfTool.Description = sdk.String(t.Description)
		}
		if t.CacheControl != nil {
			tool.OfTool.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		tools = append(tools, tool)
	}

	params := sdk.MessageNewParams{
		Model:    sdk.Model(req.Model),
		Messages: messages,
		Tools:    tools,
	}

	// Handle ResponseFormat via tool_choice for JSON Schema
	if rf := req.ResponseFormat; rf != nil && rf.Type == llm.ResponseFormatJSONSchema &&
		rf.Schema != nil {
		name := rf.Name
		if name == "" {
			name = "json_response"
		}
		schema := p.convertSchema(rf.Schema)
		params.Tools = append(params.Tools, sdk.ToolUnionParamOfTool(schema, name))
		params.ToolChoice = sdk.ToolChoiceParamOfTool(name)
	}

	if req.MaxTokens > 0 {
		params.MaxTokens = int64(req.MaxTokens)
	} else {
		params.MaxTokens = 4096
	}

	if len(system) > 0 {
		params.System = system
	}
	if req.ThinkingBudget > 0 {
		params.Thinking = sdk.ThinkingConfigParamOfEnabled(int64(req.ThinkingBudget))
		// Extended thinking requires temperature=1.0.
		params.Temperature = sdk.Float(1.0)
	} else if req.Temperature > 0 {
		params.Temperature = sdk.Float(req.Temperature)
	}

	return params
}

// convertSchema converts a Spec.Parameters value (any JSON-serializable type)
// into the Anthropic SDK's ToolInputSchemaParam. It normalizes the input via a
// JSON round-trip so that map[string]any, json.RawMessage, typed schema structs,
// and any other serializable type are all handled uniformly.
func (p *Provider) convertSchema(params any) sdk.ToolInputSchemaParam {
	schema := sdk.ToolInputSchemaParam{
		Type: constant.Object("object"),
	}

	if params == nil {
		return schema
	}

	// Normalize to map[string]any via JSON round-trip.
	raw, err := json.Marshal(params)
	if err != nil {
		return schema
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		// params is not a JSON object — not usable as a tool schema.
		return schema
	}

	if props, ok := m["properties"]; ok {
		schema.Properties = props
	} else {
		schema.Properties = m
	}

	if req, ok := m["required"]; ok {
		if items, ok := req.([]any); ok {
			for _, item := range items {
				if s, ok := item.(string); ok {
					schema.Required = append(schema.Required, s)
				}
			}
		}
	}

	return schema
}

// Stream implements llm.Stream for Anthropic.
type Stream struct {
	// The SDK returns a pointer to a struct from an internal package.
	// We use the same type returned by MessageService.NewStreaming.
	stream interface {
		Next() bool
		Current() sdk.MessageStreamEventUnion
		Err() error
		Close() error
	}
	err        error
	activeCall *llm.Call
	targetName string // Name of the tool used for ResponseFormatJSONSchema
	model      string
	p          *Provider
	ctx        context.Context
}

func (s *Stream) Next() (*llm.Chunk, bool) {
	for s.stream.Next() {
		event := s.stream.Current()

		switch event.Type {
		case "message_start":
			msg := event.AsMessageStart()
			usage := llm.Usage{
				InputTokens: int(msg.Message.Usage.InputTokens),
			}
			return &llm.Chunk{Usage: &usage}, true
		case "content_block_start":
			start := event.AsContentBlockStart()
			if start.ContentBlock.Type == "tool_use" {
				s.activeCall = &llm.Call{
					ID:   start.ContentBlock.ID,
					Type: "function",
				}
				s.activeCall.Function.Name = start.ContentBlock.Name
				return &llm.Chunk{Calls: []llm.Call{*s.activeCall}}, true
			}
			if start.ContentBlock.Type == "thinking" {
				chunk := &llm.Chunk{
					Reasoning: start.ContentBlock.Thinking,
					ThinkingBlocks: []llm.ThinkingBlock{{
						Type:      "thinking",
						Thinking:  start.ContentBlock.Thinking,
						Signature: start.ContentBlock.Signature,
					}},
				}
				return chunk, true
			}
			if start.ContentBlock.Type == "redacted_thinking" {
				chunk := &llm.Chunk{
					Reasoning: "<redacted_thinking />",
					ThinkingBlocks: []llm.ThinkingBlock{{
						Type:      "redacted_thinking",
						Signature: start.ContentBlock.Signature,
					}},
				}
				return chunk, true
			}
		case "content_block_delta":
			delta := event.AsContentBlockDelta()
			switch delta.Delta.Type {
			case "text_delta":
				return &llm.Chunk{Content: delta.Delta.Text}, true
			case "thinking_delta":
				return &llm.Chunk{
					Reasoning: delta.Delta.Thinking,
					ThinkingBlocks: []llm.ThinkingBlock{{
						Type:     "thinking",
						Thinking: delta.Delta.Thinking,
					}},
				}, true
			case "input_json_delta":
				if s.activeCall != nil {
					s.activeCall.Function.Arguments += delta.Delta.PartialJSON
					chunk := &llm.Chunk{Calls: []llm.Call{*s.activeCall}}
					// Promote tool input to Content if it's the target response tool.
					if s.activeCall.Function.Name == s.targetName {
						chunk.Content = delta.Delta.PartialJSON
					}
					return chunk, true
				}
			}
		case "message_delta":
			delta := event.AsMessageDelta()
			usage := llm.Usage{
				OutputTokens: int(delta.Usage.OutputTokens),
			}
			usage.Cost = s.p.Cost(s.ctx, s.model, usage)
			return &llm.Chunk{Usage: &usage}, true
		case "content_block_stop":
			s.activeCall = nil
		case "message_stop":
			return nil, false
		}
	}

	if err := s.stream.Err(); err != nil {
		s.err = err
	}
	return nil, false
}

func (s *Stream) Err() error {
	return s.err
}

func (s *Stream) Close() error {
	return s.stream.Close()
}

// Capabilities returns the feature set for the given model.
// It consults the model caps map first; unknown models get DefaultCapabilities.
func (p *Provider) Capabilities(model string) llm.Capabilities {
	if p.modelCaps != nil {
		if caps, ok := p.modelCaps[model]; ok {
			return caps
		}
	}
	return llm.DefaultCapabilities()
}

// IsTransient returns true if the error is a rate limit or server error.
func (p *Provider) IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if llm.IsTransientTransportError(err) {
		return true
	}
	var sdkErr *sdk.Error
	if errors.As(err, &sdkErr) {
		switch sdkErr.StatusCode {
		case 429, 500, 502, 503, 504:
			return true
		}
	}
	return false
}

// IsContextOverflow returns true if the error indicates the model's context
// window was exceeded.
func (p *Provider) IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	var sdkErr *sdk.Error
	if errors.As(err, &sdkErr) {
		if sdkErr.StatusCode == 400 {
			msg := sdkErr.Error()
			if strings.Contains(msg, "prompt") && strings.Contains(msg, "token") {
				return true
			}
		}
	}
	return false
}
