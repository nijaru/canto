package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/memory"
)

// ArchivalMemoryInsertTool allows an agent to explicitly write data into archival memory.
type ArchivalMemoryInsertTool struct {
	Store    memory.VectorStore
	Embedder llm.Embedder
}

func (t *ArchivalMemoryInsertTool) Spec() llm.Spec {
	return llm.Spec{
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

func (t *ArchivalMemorySearchTool) Spec() llm.Spec {
	return llm.Spec{
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
		Source  string  `json:"source,omitzero"`
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

	out, err := json.Marshal(hits, jsontext.WithIndent("  "))
	if err != nil {
		return "", fmt.Errorf("failed to marshal search results: %w", err)
	}

	return string(out), nil
}

// MemorizeKnowledgeTool allows an agent to explicitly write data into FTS5-backed knowledge memory.
type MemorizeKnowledgeTool struct {
	Store     *memory.CoreStore
	SessionID string
}

func (t *MemorizeKnowledgeTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "memorize_knowledge",
		Description: "Store important information, facts, or document excerpts into long-term text-based memory. Use this for crucial content you need to find later via keyword search.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{
					"type":        "string",
					"description": "The exact text content to memorize.",
				},
				"metadata": map[string]any{
					"type":        "object",
					"description": "Optional arbitrary metadata to store with this knowledge.",
				},
			},
			"required": []string{"content"},
		},
	}
}

type memorizeKnowledgeArgs struct {
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata"`
}

func (t *MemorizeKnowledgeTool) Execute(ctx context.Context, args string) (string, error) {
	var parsed memorizeKnowledgeArgs
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if parsed.Content == "" {
		return "Error: content to memorize cannot be empty", nil
	}

	hash := sha256.Sum256([]byte(parsed.Content))
	id := hex.EncodeToString(hash[:])

	item := &memory.KnowledgeItem{
		ID:        id,
		SessionID: t.SessionID,
		Content:   parsed.Content,
		Metadata:  parsed.Metadata,
	}

	if err := t.Store.SaveKnowledge(ctx, item); err != nil {
		return "", fmt.Errorf("failed to store knowledge: %w", err)
	}

	return fmt.Sprintf("Successfully memorized knowledge with ID: %s", id), nil
}

// RecallKnowledgeTool permits an agent to search its knowledge base via FTS5 keyword search.
type RecallKnowledgeTool struct {
	Store *memory.CoreStore
	Limit int
}

func (t *RecallKnowledgeTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "recall_knowledge",
		Description: "Search your long-term text-based memory using keyword search (FTS5). Returns matching knowledge items.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query (keywords or FTS5 syntax).",
				},
			},
			"required": []string{"query"},
		},
	}
}

type recallKnowledgeArgs struct {
	Query string `json:"query"`
}

func (t *RecallKnowledgeTool) Execute(ctx context.Context, args string) (string, error) {
	var parsed recallKnowledgeArgs
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if parsed.Query == "" {
		return "Error: search query cannot be empty", nil
	}

	k := t.Limit
	if k <= 0 {
		k = 5
	}

	results, err := t.Store.SearchKnowledge(ctx, parsed.Query, k)
	if err != nil {
		return "", fmt.Errorf("failed to search knowledge memory: %w", err)
	}

	if len(results) == 0 {
		return "No matching knowledge found.", nil
	}

	out, err := json.Marshal(results, jsontext.WithIndent("  "))
	if err != nil {
		return "", fmt.Errorf("failed to marshal search results: %w", err)
	}

	return string(out), nil
}
