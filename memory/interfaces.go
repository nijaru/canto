package memory

import "context"

type BlockStore interface {
	UpsertBlock(ctx context.Context, block Block) error
	ListBlocks(ctx context.Context, namespaces []Namespace) ([]Block, error)
}

type MemoryStore interface {
	UpsertMemory(ctx context.Context, memory Memory) error
	GetMemory(ctx context.Context, id string) (*Memory, error)
	SearchMemories(
		ctx context.Context,
		namespaces []Namespace,
		roles []Role,
		query string,
		limit int,
		filters map[string]any,
		includeRecent bool,
	) ([]Memory, error)
}

type Store interface {
	BlockStore
	MemoryStore
}

type Writer interface {
	Write(ctx context.Context, input WriteInput) (WriteResult, error)
}

type Retriever interface {
	Retrieve(ctx context.Context, query Query) ([]Memory, error)
}
