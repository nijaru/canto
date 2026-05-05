package llm

// GenerateFromStream collects chunks from a stream and assembles an Response.
// It is intended for use by Provider implementations to avoid duplicating
// the complex logic of assembling streaming chunks.
func GenerateFromStream(s Stream) (*Response, error) {
	defer s.Close()
	var resp Response
	// toolCallIndices tracks tool calls by their ID to handle deltas correctly.
	toolCallIndices := make(map[string]int)
	// thinkingBlockIndices tracks thinking blocks by their index to handle deltas if needed.
	// For now, most streaming thinking blocks don't have a unique ID, but Anthropic
	// may emit multiple blocks.
	thinkingBlockIndices := make(map[int]int)

	for {
		chunk, ok := s.Next()
		if !ok {
			break
		}
		resp.Content += chunk.Content
		resp.Reasoning += chunk.Reasoning
		for i, block := range chunk.ThinkingBlocks {
			if idx, ok := thinkingBlockIndices[i]; ok {
				resp.ThinkingBlocks[idx].Thinking += block.Thinking
				if block.Signature != "" {
					resp.ThinkingBlocks[idx].Signature = block.Signature
				}
			} else {
				thinkingBlockIndices[i] = len(resp.ThinkingBlocks)
				resp.ThinkingBlocks = append(resp.ThinkingBlocks, block)
			}
		}
		for _, call := range chunk.Calls {
			if idx, ok := toolCallIndices[call.ID]; ok {
				// Update existing call. If the chunk contains the full state,
				// we overwrite; if it's a delta, we should append.
				// For now, we assume the provider normalization layer (like
				// OpenAIStream) handles the delta-to-full-state conversion
				// and we just take the latest state.
				resp.Calls[idx] = call
			} else {
				// New call
				toolCallIndices[call.ID] = len(resp.Calls)
				resp.Calls = append(resp.Calls, call)
			}
		}
		if chunk.Usage != nil {
			resp.Usage = *chunk.Usage
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Stream defines the interface for a streaming LLM response.
type Stream interface {
	// Next returns the next chunk of the response.
	// It returns (nil, false) when the stream is exhausted.
	Next() (*Chunk, bool)
	// Err returns the first error encountered during streaming.
	Err() error
	// Close closes the stream.
	Close() error
}

// Chunk represents a single piece of a streaming response.
type Chunk struct {
	Content        string          `json:"content"`
	Reasoning      string          `json:"reasoning,omitempty"`
	ThinkingBlocks []ThinkingBlock `json:"thinking_blocks,omitempty"`
	Calls          []Call          `json:"tool_calls,omitempty"`
	// Usage is populated in the final chunk(s) if supported by the provider.
	Usage *Usage `json:"usage,omitempty"`
}
