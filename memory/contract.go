package memory

import "context"

// MemoryCapabilities reports which higher-level memory behaviors a manager can
// provide without exposing backend-specific implementation details.
type MemoryCapabilities struct {
	Namespaced     bool
	Blocks         bool
	Memories       bool
	Search         bool
	Forget         bool
	BatchWrite     bool
	Consolidate    bool
	SemanticSearch bool
	Temporal       bool
	AsyncWrite     bool
	Link           bool
	Traverse       bool
	BuildContext   bool
}

// MemoryService is the current memory-shaped public contract.
//
// It stays intentionally small: expose the future verbs without replacing the
// existing experimental surface or freezing backend details too early.
type MemoryService interface {
	Remember(ctx context.Context, input WriteInput) (WriteResult, error)
	RememberBatch(ctx context.Context, inputs []WriteInput) (WriteResult, error)
	Search(ctx context.Context, query Query) ([]Memory, error)
	Forget(ctx context.Context, id, reason string) error
	Capabilities() MemoryCapabilities
}

var _ MemoryService = (*Manager)(nil)

// Capabilities reports the current manager's supported memory behaviors.
func (m *Manager) Capabilities() MemoryCapabilities {
	if m == nil || m.store == nil {
		return MemoryCapabilities{}
	}
	return MemoryCapabilities{
		Namespaced:     true,
		Blocks:         true,
		Memories:       true,
		Search:         true,
		Forget:         true,
		BatchWrite:     true,
		Consolidate:    true,
		SemanticSearch: m.vector != nil && m.embedder != nil,
		Temporal:       true,
		AsyncWrite:     true,
		Link:           false,
		Traverse:       false,
		BuildContext:   false,
	}
}

// Remember stores a memory record. It is a semantic alias for Write.
func (m *Manager) Remember(ctx context.Context, input WriteInput) (WriteResult, error) {
	return m.Write(ctx, input)
}

// RememberBatch stores multiple memory records through the manager write pipeline.
func (m *Manager) RememberBatch(
	ctx context.Context,
	inputs []WriteInput,
) (WriteResult, error) {
	return m.WriteBatch(ctx, inputs)
}

// Search retrieves memories. It is a semantic alias for Retrieve.
func (m *Manager) Search(ctx context.Context, query Query) ([]Memory, error) {
	return m.Retrieve(ctx, query)
}
