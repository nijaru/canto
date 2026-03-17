package tools

import (
	"context"
	"strings"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

// SearchTool implements the search_tools meta-tool. It lets agents discover
// and unlock specific tools from a large registry without loading all specs
// into every LLM request. Use with LazyTools.
type SearchTool struct {
	Registry *tool.Registry
}

// Spec returns the search_tools tool specification.
func (s *SearchTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "search_tools",
		Description: "Search for available tools by name or description keyword. Returns matching tool specifications so you can use them in subsequent calls.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Keyword to search for in tool names and descriptions.",
				},
			},
			"required": []string{"query"},
		},
	}
}

// Execute searches the registry for tools matching the query and returns
// their full specifications as a JSON array.
func (s *SearchTool) Execute(_ context.Context, args string) (string, error) {
	var input struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", err
	}

	query := strings.ToLower(input.Query)
	var matches []llm.Spec

	for _, spec := range s.Registry.Specs() {
		name := strings.ToLower(spec.Name)
		desc := strings.ToLower(spec.Description)
		if strings.Contains(name, query) || strings.Contains(desc, query) {
			matches = append(matches, *spec)
		}
	}

	if len(matches) == 0 {
		all := s.Registry.Names()
		return "No tools matched. Available tools: " + strings.Join(all, ", "), nil
	}

	data, err := json.Marshal(matches)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
