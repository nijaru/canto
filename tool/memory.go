package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/memory"
)

// ArchivalMemoryInsertTool allows an agent to explicitly write data into archival memory.
type ArchivalMemoryInsertTool struct {
	Store    memory.VectorStore
	Embedder llm.Embedder
}

func (t *ArchivalMemoryInsertTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "archival_memory_insert",
		Description: "Store important information, facts, or document excerpts into long-term archival memory. Use this to remember crucial context that extends beyond your current active window.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{
					"type":        "string",
					"description": "The exact text content to memorize.",
				},
				"source": map[string]any{
					"type":        "string",
					"description": "Optional keyword or title indicating the source of this information.",
				},
			},
			"required": []string{"content"},
		},
	}
}

type archivalMemoryInsertArgs struct {
	Content string `json:"content"`
	Source  string `json:"source"`
}

func (t *ArchivalMemoryInsertTool) Execute(ctx context.Context, args string) (string, error) {
	var parsed archivalMemoryInsertArgs
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if parsed.Content == "" {
		return "Error: content to memorize cannot be empty", nil
	}

	vector, err := t.Embedder.EmbedContent(ctx, parsed.Content)
	if err != nil {
		return "", fmt.Errorf("failed to generate embedding: %w", err)
	}

	hash := sha256.Sum256([]byte(parsed.Content))
	id := hex.EncodeToString(hash[:])

	metadata := map[string]any{
		"content": parsed.Content,
		"source":  parsed.Source,
	}

	if err := t.Store.Upsert(ctx, id, vector, metadata); err != nil {
		return "", fmt.Errorf("failed to store vector: %w", err)
	}

	return fmt.Sprintf("Successfully memorized content into archival memory with ID: %s", id), nil
}

// ArchivalMemorySearchTool permits an agent to search its knowledge base via cosine similarity.
type ArchivalMemorySearchTool struct {
	Store    memory.VectorStore
	Embedder llm.Embedder
	TopK     int
}

func (t *ArchivalMemorySearchTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "archival_memory_search",
		Description: "Search your unbounded long-term archival memory for previously stored information using semantic similarity. Returns the most relevant stored memories.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The semantic concept or question you want to find information about.",
				},
			},
			"required": []string{"query"},
		},
	}
}

type archivalMemorySearchArgs struct {
	Query string `json:"query"`
}

func (t *ArchivalMemorySearchTool) Execute(ctx context.Context, args string) (string, error) {
	var parsed archivalMemorySearchArgs
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if parsed.Query == "" {
		return "Error: search query cannot be empty", nil
	}

	vector, err := t.Embedder.EmbedContent(ctx, parsed.Query)
	if err != nil {
		return "", fmt.Errorf("failed to generate embedding: %w", err)
	}

	k := t.TopK
	if k <= 0 {
		k = 5 // reasonable default for LLM context limits
	}

	results, err := t.Store.Search(ctx, vector, k, nil)
	if err != nil {
		return "", fmt.Errorf("failed to search archival memory: %w", err)
	}

	if len(results) == 0 {
		return "No relevant memories found in archival storage.", nil
	}

	// Prepare results for LLM consumption
	type hit struct {
		Content string  `json:"content"`
		Source  string  `json:"source,omitempty"`
		Score   float32 `json:"relevance_score"`
	}

	hits := make([]hit, 0, len(results))
	for _, r := range results {
		contentVal, ok := r.Metadata["content"]
		if !ok {
			continue
		}
		
		content, ok := contentVal.(string)
		if !ok {
			content = fmt.Sprintf("%v", contentVal)
		}
		
		source, _ := r.Metadata["source"].(string)
		
		hits = append(hits, hit{
			Content: content,
			Source:  source,
			Score:   r.Score,
		})
	}

	out, err := json.MarshalIndent(hits, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal search results: %w", err)
	}

	return string(out), nil
}
