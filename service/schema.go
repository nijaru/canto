package service

import "github.com/nijaru/canto/tool"

// SchemaFor infers a JSON Schema for A and returns it as a JSON-compatible map.
func SchemaFor[A any]() (map[string]any, error) {
	return tool.SchemaFor[A]()
}
