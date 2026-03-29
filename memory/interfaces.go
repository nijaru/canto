package memory

import (
	"context"
	"time"
)

type SearchInput struct {
	Namespaces        []Namespace
	Roles             []Role
	Text              string
	Limit             int
	Filters           map[string]any
	IncludeRecent     bool
	ValidAt           *time.Time
	ObservedAfter     *time.Time
	ObservedBefore    *time.Time
	IncludeForgotten  bool
	IncludeSuperseded bool
}

type BlockStore interface {
	UpsertBlock(ctx context.Context, block Block) error
	ListBlocks(ctx context.Context, namespaces []Namespace) ([]Block, error)
}

type MemoryStore interface {
	UpsertMemory(ctx context.Context, memory Memory) error
	GetMemory(ctx context.Context, id string) (*Memory, error)
	SearchMemories(ctx context.Context, input SearchInput) ([]Memory, error)
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
