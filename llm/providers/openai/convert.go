package openai

import (
	"strings"

	"github.com/go-json-experiment/json"
	"github.com/nijaru/canto/llm"
	"github.com/sashabaranov/go-openai"
)

// ConvertRequest transforms the unified Request into OpenAI's format.
func (b *Base) ConvertRequest(req *llm.Request) openai.ChatCompletionRequest {
	messages := make([]openai.ChatCompletionMessage, len(req.Messages))
	for i, m := range req.Messages {
		content, multiContent := b.convertMessageContent(m)
		msg := openai.ChatCompletionMessage{
			Role:         string(m.Role),
			Content:      content,
			MultiContent: multiContent,
			Name:         m.Name,
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
		Model:         req.Model,
		Messages:      messages,
		Tools:         tools,
		StreamOptions: &openai.StreamOptions{IncludeUsage: true},
	}
	if caps.Temperature {
		cr.Temperature = float32(req.Temperature)
		cr.MaxTokens = req.MaxTokens
	} else {
		// Models without temperature control require max_completion_tokens,
		// which counts both visible output and internal reasoning tokens.
		cr.MaxCompletionTokens = req.MaxTokens
	}
	if caps.ReasoningCaps().Kind == llm.ReasoningKindBoolean &&
		caps.SupportsReasoningToggle(req.ReasoningEffort) {
		cr.ChatTemplateKwargs = map[string]any{
			"enable_thinking":   reasoningToggleEnabled(req.ReasoningEffort),
			"preserve_thinking": true,
		}
	} else if caps.SupportsReasoningEffort(req.ReasoningEffort) {
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

func (b *Base) convertMessageContent(m llm.Message) (string, []openai.ChatMessagePart) {
	if !hasImageParts(m.Parts) {
		return m.TextContent(), nil
	}

	parts := make([]openai.ChatMessagePart, 0, len(m.Parts))
	sawText := false
	for _, part := range m.Parts {
		switch part.Type {
		case "", llm.ContentPartText:
			if part.Text == "" {
				continue
			}
			sawText = true
			parts = append(parts, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeText,
				Text: part.Text,
			})
		case llm.ContentPartImage:
			imageURL := imagePartURL(part)
			if imageURL == "" {
				continue
			}
			parts = append(parts, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeImageURL,
				ImageURL: &openai.ChatMessageImageURL{
					URL:    imageURL,
					Detail: openai.ImageURLDetailAuto,
				},
			})
		}
	}
	if !sawText && m.Content != "" {
		parts = append([]openai.ChatMessagePart{{
			Type: openai.ChatMessagePartTypeText,
			Text: m.Content,
		}}, parts...)
	}
	if len(parts) == 0 {
		return m.TextContent(), nil
	}
	return "", parts
}

func hasImageParts(parts []llm.ContentPart) bool {
	for _, part := range parts {
		if part.Type == llm.ContentPartImage {
			return true
		}
	}
	return false
}

func imagePartURL(part llm.ContentPart) string {
	if part.URL != "" {
		return part.URL
	}
	if part.Data == "" {
		return ""
	}
	mimeType := part.MIMEType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + part.Data
}

func reasoningToggleEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off", "none", "disabled":
		return false
	default:
		return true
	}
}

// schemaMarshaler wraps a map[string]any to implement json.Marshaler,
// as required by the OpenAI SDK's JSONSchema field.
type schemaMarshaler map[string]any

func (s schemaMarshaler) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(s))
}

// ConvertToolCalls transforms OpenAI tool calls into the unified format.
func (b *Base) ConvertToolCalls(calls []openai.ToolCall) []llm.Call {
	if len(calls) == 0 {
		return nil
	}
	res := make([]llm.Call, len(calls))
	for i, call := range calls {
		res[i] = llm.Call{
			ID:   call.ID,
			Type: string(call.Type),
		}
		res[i].Function.Name = call.Function.Name
		res[i].Function.Arguments = call.Function.Arguments
	}
	return res
}
