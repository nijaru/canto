package memory

import "github.com/nijaru/canto/llm"

// RetrievalPostprocessor can reorder, filter, or otherwise adjust retrieved
// memories after the manager's built-in retrieval pipeline.
type RetrievalPostprocessor func(query Query, results []Memory) ([]Memory, error)

type RetrievePolicy struct {
	Planner     RetrievalPlanner
	Fuser       RetrievalFuser
	Postprocess RetrievalPostprocessor
}

type ManagerOption func(*Manager)

// WithVectorStore enables semantic retrieval and vector indexing on writes.
func WithVectorStore(vector VectorStore) ManagerOption {
	return func(m *Manager) {
		m.vector = vector
	}
}

// WithEmbedder configures the embedder used for semantic indexing and search.
func WithEmbedder(embedder llm.Embedder) ManagerOption {
	return func(m *Manager) {
		m.embedder = embedder
	}
}

// WithWritePolicy configures extraction, conflict handling, and write mode.
func WithWritePolicy(policy WritePolicy) ManagerOption {
	return func(m *Manager) {
		m.policy = policy
	}
}

// WithRetrievePolicy configures post-retrieval filtering or reranking hooks.
func WithRetrievePolicy(policy RetrievePolicy) ManagerOption {
	return func(m *Manager) {
		m.retrievePolicy = policy
	}
}
