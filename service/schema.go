package service

import (
	stdjson "encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// SchemaFor infers a JSON Schema for A and returns it as a JSON-compatible map.
func SchemaFor[A any]() (map[string]any, error) {
	schema, err := jsonschema.For[A](nil)
	if err != nil {
		return nil, fmt.Errorf("service schema: %w", err)
	}
	data, err := stdjson.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("service schema: marshal: %w", err)
	}
	var out map[string]any
	if err := stdjson.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("service schema: unmarshal: %w", err)
	}
	return out, nil
}
