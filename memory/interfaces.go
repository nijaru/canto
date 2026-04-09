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

type BlockListInput struct {
	Namespaces []Namespace
	Limit      int
}

type MemoryListInput struct {
	Namespaces        []Namespace
	Roles             []Role
	Limit             int
	Filters           map[string]any
	ValidAt           *time.Time
	ObservedAfter     *time.Time
	ObservedBefore    *time.Time
	IncludeForgotten  bool
	IncludeSuperseded bool
}

type BlockRepository interface {
	GetBlock(ctx context.Context, namespace Namespace, name string) (*Block, error)
	ListBlocks(ctx context.Context, input BlockListInput) ([]Block, error)
}

type MemoryRepository interface {
	GetMemory(ctx context.Context, id string) (*Memory, error)
	ListMemories(ctx context.Context, input MemoryListInput) ([]Memory, error)
}

type Repository interface {
	BlockRepository
	MemoryRepository
}

type BlockStore interface {
	BlockRepository
	UpsertBlock(ctx context.Context, block Block) error
}

type MemoryStore interface {
	MemoryRepository
	UpsertMemory(ctx context.Context, memory Memory) error
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
