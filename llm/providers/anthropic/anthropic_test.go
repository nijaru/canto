package anthropic

import (
	"encoding/json"
	"testing"
)

// provider returns a zero-value Provider sufficient for testing convertSchema,
// which does not use the client or config fields.
func provider() *Provider { return &Provider{} }

func TestConvertSchema_Nil(t *testing.T) {
	schema := provider().convertSchema(nil)
	if schema.Properties != nil {
		t.Errorf("nil params: Properties must be nil, got %v", schema.Properties)
	}
	if len(schema.Required) != 0 {
		t.Errorf("nil params: Required must be empty, got %v", schema.Required)
	}
}

func TestConvertSchema_MapWithProperties(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []any{"query"},
	}
	schema := provider().convertSchema(params)

	if schema.Properties == nil {
		t.Fatal("Properties must not be nil")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "query" {
		t.Errorf("Required: got %v, want [query]", schema.Required)
	}
}

func TestConvertSchema_MapWithoutProperties(t *testing.T) {
	// When the schema has no "properties" key, the whole map becomes Properties.
	params := map[string]any{
		"query": map[string]any{"type": "string"},
	}
	schema := provider().convertSchema(params)
	if schema.Properties == nil {
		t.Fatal("Properties must not be nil when schema is a flat map")
	}
}

func TestConvertSchema_JSONRawMessage(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {"path": {"type": "string"}},
		"required": ["path"]
	}`)
	schema := provider().convertSchema(raw)
	if schema.Properties == nil {
		t.Fatal("Properties must not be nil for json.RawMessage input")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "path" {
		t.Errorf("Required: got %v, want [path]", schema.Required)
	}
}

func TestConvertSchema_TypedStruct(t *testing.T) {
	type Params struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	params := Params{
		Type:       "object",
		Properties: map[string]any{"n": map[string]any{"type": "integer"}},
		Required:   []string{"n"},
	}
	schema := provider().convertSchema(params)
	if schema.Properties == nil {
		t.Fatal("Properties must not be nil for typed struct input")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "n" {
		t.Errorf("Required: got %v, want [n]", schema.Required)
	}
}

func TestConvertSchema_RequiredStrings(t *testing.T) {
	// Verify all string items in a required array are collected.
	params := map[string]any{
		"properties": map[string]any{},
		"required":   []any{"a", "b", 42, "c"}, // 42 is non-string, must be skipped
	}
	schema := provider().convertSchema(params)
	if len(schema.Required) != 3 {
		t.Errorf("Required: got %v, want [a b c]", schema.Required)
	}
}

func TestConvertSchema_NonObjectParams(t *testing.T) {
	// A non-object JSON value (e.g., a string) must not panic and returns empty schema.
	schema := provider().convertSchema("not-an-object")
	if schema.Properties != nil {
		t.Errorf("non-object: Properties must be nil, got %v", schema.Properties)
	}
}
