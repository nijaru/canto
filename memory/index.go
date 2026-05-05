package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

type IndexEntryKind string

const (
	IndexBlock  IndexEntryKind = "block"
	IndexMemory IndexEntryKind = "memory"
)

type IndexEntry struct {
	Kind      IndexEntryKind `json:"kind"`
	Path      string         `json:"path"`
	Ref       IndexRef       `json:"ref"`
	Namespace Namespace      `json:"namespace"`
	Role      Role           `json:"role"`
	Summary   string         `json:"summary"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type IndexQuery struct {
	Namespaces        []Namespace
	Roles             []Role
	Limit             int
	ValidAt           *time.Time
	ObservedAfter     *time.Time
	ObservedBefore    *time.Time
	IncludeForgotten  bool
	IncludeSuperseded bool
}

type Index struct {
	repo            Repository
	maxEntries      int
	maxBlockEntries int
	maxSummaryRunes int
}

func NewIndex(repo Repository, opts ...IndexOption) *Index {
	idx := &Index{
		repo:            repo,
		maxEntries:      64,
		maxBlockEntries: 16,
		maxSummaryRunes: 120,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(idx)
		}
	}
	return idx
}

type IndexOption func(*Index)

func WithIndexMaxEntries(limit int) IndexOption {
	return func(i *Index) {
		if limit > 0 {
			i.maxEntries = limit
		}
	}
}

func WithIndexMaxBlockEntries(limit int) IndexOption {
	return func(i *Index) {
		if limit > 0 {
			i.maxBlockEntries = limit
		}
	}
}

func WithIndexMaxSummaryRunes(limit int) IndexOption {
	return func(i *Index) {
		if limit > 0 {
			i.maxSummaryRunes = limit
		}
	}
}

func (i *Index) Snapshot(
	ctx context.Context,
	query IndexQuery,
) (IndexSnapshot, error) {
	if i == nil || i.repo == nil {
		return IndexSnapshot{}, fmt.Errorf("memory index: repository is required")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = i.maxEntries
	}
	includeCore := len(query.Roles) == 0 || slices.Contains(query.Roles, RoleCore)
	memoryRoles := filterRoles(query.Roles, func(role Role) bool { return role != RoleCore })

	var entries []IndexEntry
	if includeCore {
		blockLimit := min(limit, i.maxBlockEntries)
		if len(memoryRoles) == 0 && len(query.Roles) == 1 && query.Roles[0] == RoleCore {
			blockLimit = limit
		}
		blocks, err := i.repo.ListBlocks(ctx, BlockListInput{
			Namespaces: query.Namespaces,
			Limit:      blockLimit,
		})
		if err != nil {
			return IndexSnapshot{}, err
		}
		for _, block := range blocks {
			ref := IndexRef{
				Kind:      IndexBlock,
				Namespace: block.Namespace,
				Role:      RoleCore,
				Name:      block.Name,
			}
			entries = append(entries, IndexEntry{
				Kind:      IndexBlock,
				Path:      ref.Path(),
				Ref:       ref,
				Namespace: block.Namespace,
				Role:      RoleCore,
				Summary:   summarizeIndexText(block.Metadata, block.Content, i.maxSummaryRunes),
				UpdatedAt: block.UpdatedAt,
			})
		}
	}

	remaining := limit - len(entries)
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 0 && (len(memoryRoles) > 0 || len(query.Roles) == 0) {
		memories, err := i.repo.ListMemories(ctx, MemoryListInput{
			Namespaces:        query.Namespaces,
			Roles:             memoryRoles,
			Limit:             remaining,
			ValidAt:           query.ValidAt,
			ObservedAfter:     query.ObservedAfter,
			ObservedBefore:    query.ObservedBefore,
			IncludeForgotten:  query.IncludeForgotten,
			IncludeSuperseded: query.IncludeSuperseded,
		})
		if err != nil {
			return IndexSnapshot{}, err
		}
		for _, memory := range memories {
			ref := IndexRef{
				Kind:      IndexMemory,
				Namespace: memory.Namespace,
				Role:      memory.Role,
				Name:      memoryLeafName(memory),
				ID:        memory.ID,
			}
			entries = append(entries, IndexEntry{
				Kind:      IndexMemory,
				Path:      ref.Path(),
				Ref:       ref,
				Namespace: memory.Namespace,
				Role:      memory.Role,
				Summary:   summarizeIndexText(memory.Metadata, memory.Content, i.maxSummaryRunes),
				UpdatedAt: memory.UpdatedAt,
			})
		}
	}

	slices.SortFunc(entries, func(a, b IndexEntry) int {
		return strings.Compare(a.Path, b.Path)
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return IndexSnapshot{Entries: entries}, nil
}
