package anthropic

import (
	"context"
	"fmt"
	"os"

	"charm.land/catwalk/pkg/catwalk"
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/nijaru/canto/llm"
)

// Provider implements the llm.Provider interface for Anthropic.
type Provider struct {
	client sdk.Client
	config catwalk.Provider
}

// NewProvider creates a new Anthropic provider from a catwalk configuration.
func NewProvider(cfg catwalk.Provider) *Provider {
	apiKey := cfg.APIKey
	if apiKey == "$ANTHROPIC_API_KEY" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if cfg.APIEndpoint != "" {
		opts = append(opts, option.WithBaseURL(cfg.APIEndpoint))
	}

	return &Provider{
		client: sdk.NewClient(opts...),
		config: cfg,
	}
}

func (p *Provider) ID() string {
	return string(p.config.ID)
}

func (p *Provider) Generate(ctx context.Context, req *llm.LLMRequest) (*llm.LLMResponse, error) {
	params := p.convertRequest(req)
	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	res := &llm.LLMResponse{
		Usage: llm.Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
			TotalTokens:  int(resp.Usage.InputTokens + resp.Usage.OutputTokens),
		},
	}

	for _, block := range resp.Content {
		if block.Type == "text" {
			res.Content += block.Text
		} else if block.Type == "tool_use" {
			res.Calls = append(res.Calls, llm.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      block.Name,
					Arguments: string(block.Input),
				},
			})
		}
	}

	return res, nil
}

func (p *Provider) Stream(ctx context.Context, req *llm.LLMRequest) (llm.Stream, error) {
	// TODO: Implement streaming for Anthropic
	return nil, fmt.Errorf("streaming not implemented for anthropic yet")
}

func (p *Provider) Models(ctx context.Context) ([]catwalk.Model, error) {
	return p.config.Models, nil
}

func (p *Provider) convertRequest(req *llm.LLMRequest) sdk.MessageNewParams {
	var system []sdk.TextBlockParam
	var messages []sdk.MessageParam

	for _, m := range req.Messages {
		if m.Role == llm.RoleSystem {
			system = append(system, sdk.TextBlockParam{
				Text: m.Content,
				Type: constant.Text("text"),
			})
			continue
		}

		// Handle Tool results (must be in a user message)
		if m.Role == llm.RoleTool {
			messages = append(messages, sdk.NewUserMessage(
				sdk.NewToolResultBlock(m.ToolID, m.Content, false),
			))
			continue
		}

		// Handle normal messages and assistant tool calls
		var blocks []sdk.ContentBlockParamUnion
		if m.Content != "" {
			blocks = append(blocks, sdk.NewTextBlock(m.Content))
		}
		for _, call := range m.Calls {
			blocks = append(blocks, sdk.NewToolUseBlock(call.ID, call.Function.Arguments, call.Function.Name))
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
		tools = append(tools, tool)
	}

	params := sdk.MessageNewParams{
		Model:    sdk.Model(req.Model),
		Messages: messages,
		Tools:    tools,
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = int64(req.MaxTokens)
	} else {
		params.MaxTokens = 4096
	}

	if len(system) > 0 {
		params.System = system
	}
	if req.Temperature > 0 {
		params.Temperature = sdk.Float(req.Temperature)
	}

	return params
}

func (p *Provider) convertSchema(params any) sdk.ToolInputSchemaParam {
	schema := sdk.ToolInputSchemaParam{
		Type: constant.Object("object"),
	}

	// Canto ToolSpec.Parameters is usually the full JSON Schema object.
	// Anthropic expects the 'properties' and 'required' fields.

	m, ok := params.(map[string]any)
	if !ok {
		// Fallback for simple properties map
		schema.Properties = params
		return schema
	}

	// Case 1: Full JSON Schema object
	if props, ok := m["properties"]; ok {
		schema.Properties = props
		if req, ok := m["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					schema.Required = append(schema.Required, s)
				}
			}
		} else if req, ok := m["required"].([]string); ok {
			schema.Required = req
		}
	} else {
		// Case 2: Just the properties themselves
		schema.Properties = m
	}

	return schema
}

