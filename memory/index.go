package memory

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
)

type IndexEntryKind string

const (
	IndexBlock  IndexEntryKind = "block"
	IndexMemory IndexEntryKind = "memory"
)

type IndexRef struct {
	Kind      IndexEntryKind `json:"kind"`
	Namespace Namespace      `json:"namespace"`
	Role      Role           `json:"role,omitzero"`
	Name      string         `json:"name,omitzero"`
	ID        string         `json:"id,omitzero"`
}

func (r IndexRef) Path() string {
	scope := sanitizeIndexSegment(string(r.Namespace.Scope))
	scopeID := sanitizeIndexSegment(r.Namespace.ID)
	switch r.Kind {
	case IndexBlock:
		return strings.Join([]string{
			scope,
			scopeID,
			string(RoleCore),
			sanitizeIndexSegment(r.Name),
		}, "/")
	case IndexMemory:
		leaf := sanitizeIndexSegment(r.Name)
		if leaf == "" {
			leaf = "memory-" + shortIndexID(r.ID)
		} else if r.ID != "" {
			leaf += "--" + shortIndexID(r.ID)
		}
		return strings.Join([]string{
			scope,
			scopeID,
			sanitizeIndexSegment(string(r.Role)),
			leaf,
		}, "/")
	default:
		return strings.Join([]string{scope, scopeID}, "/")
	}
}

type IndexEntry struct {
	Kind      IndexEntryKind `json:"kind"`
	Path      string         `json:"path"`
	Ref       IndexRef       `json:"ref"`
	Namespace Namespace      `json:"namespace"`
	Role      Role           `json:"role"`
	Summary   string         `json:"summary"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type IndexSnapshot struct {
	Entries []IndexEntry `json:"entries"`
}

func (s IndexSnapshot) String() string {
	if len(s.Entries) == 0 {
		return ""
	}
	root := indexTreeNode{
		children: make(map[string]*indexTreeNode),
	}
	for _, entry := range s.Entries {
		parts := strings.Split(entry.Path, "/")
		node := &root
		for _, part := range parts[:len(parts)-1] {
			child := node.children[part]
			if child == nil {
				child = &indexTreeNode{children: make(map[string]*indexTreeNode)}
				node.children[part] = child
			}
			node = child
		}
		node.children[parts[len(parts)-1]] = &indexTreeNode{
			summary: entry.Summary,
		}
	}
	var sb strings.Builder
	renderIndexTree(&sb, root.children, 0)
	return strings.TrimRight(sb.String(), "\n")
}

type indexTreeNode struct {
	summary  string
	children map[string]*indexTreeNode
}

func renderIndexTree(sb *strings.Builder, nodes map[string]*indexTreeNode, depth int) {
	names := slices.Collect(maps.Keys(nodes))
	slices.Sort(names)
	indent := strings.Repeat("  ", depth)
	for _, name := range names {
		node := nodes[name]
		if len(node.children) == 0 {
			fmt.Fprintf(sb, "%s%s", indent, name)
			if node.summary != "" {
				fmt.Fprintf(sb, " -- %s", node.summary)
			}
			sb.WriteString("\n")
			continue
		}
		fmt.Fprintf(sb, "%s%s/\n", indent, name)
		renderIndexTree(sb, node.children, depth+1)
	}
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

func summarizeIndexText(metadata map[string]any, content string, maxRunes int) string {
	if summary, ok := metadata["summary"].(string); ok && summary != "" {
		return clipIndexText(summary, maxRunes)
	}
	return clipIndexText(content, maxRunes)
}

func clipIndexText(content string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 120
	}
	collapsed := strings.Join(strings.Fields(content), " ")
	runes := []rune(collapsed)
	if len(runes) <= maxRunes {
		return collapsed
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func memoryLeafName(memory Memory) string {
	if memory.Key != "" {
		return memory.Key
	}
	if title, ok := memory.Metadata["title"].(string); ok && title != "" {
		return title
	}
	return "memory-" + shortIndexID(memory.ID)
}

func shortIndexID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func sanitizeIndexSegment(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.':
			if !lastDash {
				b.WriteRune(r)
				lastDash = r == '-'
			}
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}
