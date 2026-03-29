package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nijaru/canto/llm"
)

type Scope string

const (
	ScopeThread    Scope = "thread"
	ScopeUser      Scope = "user"
	ScopeAgent     Scope = "agent"
	ScopeWorkspace Scope = "workspace"
	ScopeApp       Scope = "app"
)

type Role string

const (
	RoleCore       Role = "core"
	RoleEpisodic   Role = "episodic"
	RoleSemantic   Role = "semantic"
	RoleProcedural Role = "procedural"
)

type Namespace struct {
	Scope Scope  `json:"scope"`
	ID    string `json:"id"`
}

type Block struct {
	Namespace Namespace      `json:"namespace"`
	Name      string         `json:"name"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitzero"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Memory struct {
	ID        string         `json:"id"`
	Namespace Namespace      `json:"namespace"`
	Role      Role           `json:"role"`
	Key       string         `json:"key,omitzero"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitzero"`
	UpdatedAt time.Time      `json:"updated_at"`
	Score     float32        `json:"score,omitzero"`
}

type ConflictMode string

const (
	ConflictReplace ConflictMode = "replace"
	ConflictIgnore  ConflictMode = "ignore"
	ConflictMerge   ConflictMode = "merge"
)

type WriteMode string

const (
	WriteSync  WriteMode = "sync"
	WriteAsync WriteMode = "async"
)

type Candidate struct {
	Namespace  Namespace      `json:"namespace"`
	Role       Role           `json:"role"`
	Key        string         `json:"key,omitzero"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata,omitzero"`
	Importance float64        `json:"importance,omitzero"`
}

type CandidateExtractor func(ctx context.Context, candidate Candidate) ([]Candidate, error)

type WritePolicy struct {
	Extractor           CandidateExtractor
	ConflictMode        ConflictMode
	ImportanceThreshold float64
	DefaultMode         WriteMode
}

type WriteInput struct {
	Namespace  Namespace
	Role       Role
	Key        string
	Content    string
	Metadata   map[string]any
	Importance float64
	Mode       WriteMode
}

type WriteResult struct {
	Stored  int
	Pending int
	IDs     []string
}

type Query struct {
	Namespaces    []Namespace
	Roles         []Role
	Text          string
	Limit         int
	Filters       map[string]any
	UseSemantic   bool
	IncludeCore   bool
	IncludeRecent bool
}

type Manager struct {
	store    *CoreStore
	vector   VectorStore
	embedder llm.Embedder
	policy   WritePolicy

	asyncWG sync.WaitGroup
}

func NewManager(
	store *CoreStore,
	vector VectorStore,
	embedder llm.Embedder,
	policy WritePolicy,
) *Manager {
	if policy.ConflictMode == "" {
		policy.ConflictMode = ConflictReplace
	}
	if policy.DefaultMode == "" {
		policy.DefaultMode = WriteSync
	}
	return &Manager{
		store:    store,
		vector:   vector,
		embedder: embedder,
		policy:   policy,
	}
}

func (m *Manager) Close() error {
	m.asyncWG.Wait()
	return nil
}

func (m *Manager) Write(ctx context.Context, input WriteInput) (WriteResult, error) {
	if m == nil || m.store == nil {
		return WriteResult{}, fmt.Errorf("memory manager: store is required")
	}
	candidate := Candidate{
		Namespace:  input.Namespace,
		Role:       input.Role,
		Key:        input.Key,
		Content:    input.Content,
		Metadata:   cloneMap(input.Metadata),
		Importance: input.Importance,
	}
	candidates, err := m.extract(ctx, candidate)
	if err != nil {
		return WriteResult{}, err
	}
	mode := input.Mode
	if mode == "" {
		mode = m.policy.DefaultMode
	}

	var result WriteResult
	for _, candidate := range candidates {
		if candidate.Content == "" {
			continue
		}
		if candidate.Importance < m.policy.ImportanceThreshold {
			continue
		}
		id := memoryID(candidate)
		result.IDs = append(result.IDs, id)
		if mode == WriteAsync {
			result.Pending++
			m.asyncWG.Add(1)
			go func(candidate Candidate, id string) {
				defer m.asyncWG.Done()
				_, _ = m.storeCandidate(context.Background(), id, candidate)
			}(candidate, id)
			continue
		}
		stored, err := m.storeCandidate(ctx, id, candidate)
		if err != nil {
			return WriteResult{}, err
		}
		if stored {
			result.Stored++
		}
	}
	return result, nil
}

func (m *Manager) UpsertBlock(
	ctx context.Context,
	namespace Namespace,
	name, content string,
	metadata map[string]any,
) error {
	return m.store.UpsertBlock(ctx, Block{
		Namespace: namespace,
		Name:      name,
		Content:   content,
		Metadata:  metadata,
	})
}

func (m *Manager) Retrieve(ctx context.Context, query Query) ([]Memory, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("memory manager: store is required")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 5
	}

	roles := query.Roles
	includeCore := query.IncludeCore || len(roles) == 0 || slices.Contains(roles, RoleCore)

	var results []Memory
	if includeCore {
		blocks, err := m.store.ListBlocks(ctx, query.Namespaces)
		if err != nil {
			return nil, err
		}
		for _, block := range blocks {
			results = append(results, Memory{
				ID:        block.Name,
				Namespace: block.Namespace,
				Role:      RoleCore,
				Key:       block.Name,
				Content:   block.Content,
				Metadata:  cloneMap(block.Metadata),
				UpdatedAt: block.UpdatedAt,
				Score:     1.0,
			})
		}
	}

	ftsRoles := filterRoles(roles, func(role Role) bool { return role != RoleCore })
	ftsHits, err := m.store.SearchMemories(
		ctx,
		query.Namespaces,
		ftsRoles,
		query.Text,
		limit,
		query.Filters,
		query.IncludeRecent,
	)
	if err != nil {
		return nil, err
	}
	results = append(results, ftsHits...)

	if query.UseSemantic && query.Text != "" && m.vector != nil && m.embedder != nil {
		vector, err := m.embedder.EmbedContent(ctx, query.Text)
		if err != nil {
			return nil, err
		}
		filter := cloneMap(query.Filters)
		if filter == nil {
			filter = map[string]any{}
		}
		if len(query.Namespaces) == 1 {
			filter["scope"] = string(query.Namespaces[0].Scope)
			filter["scope_id"] = query.Namespaces[0].ID
		}
		if len(ftsRoles) == 1 {
			filter["role"] = string(ftsRoles[0])
		}
		vectorHits, err := m.vector.Search(ctx, vector, limit, filter)
		if err != nil {
			return nil, err
		}
		for _, hit := range vectorHits {
			mem, ok := memoryFromVector(hit)
			if !ok {
				continue
			}
			results = append(results, mem)
		}
	}

	results = dedupeMemories(results)
	slices.SortFunc(results, func(a, b Memory) int {
		if a.Score > b.Score {
			return -1
		}
		if a.Score < b.Score {
			return 1
		}
		if a.UpdatedAt.After(b.UpdatedAt) {
			return -1
		}
		if a.UpdatedAt.Before(b.UpdatedAt) {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (m *Manager) extract(ctx context.Context, candidate Candidate) ([]Candidate, error) {
	if m.policy.Extractor == nil {
		return []Candidate{candidate}, nil
	}
	return m.policy.Extractor(ctx, candidate)
}

func (m *Manager) storeCandidate(
	ctx context.Context,
	id string,
	candidate Candidate,
) (bool, error) {
	if candidate.Role == RoleCore {
		name := candidate.Key
		if name == "" {
			name = "default"
		}
		return true, m.store.UpsertBlock(ctx, Block{
			Namespace: candidate.Namespace,
			Name:      name,
			Content:   candidate.Content,
			Metadata:  candidate.Metadata,
		})
	}

	record := Memory{
		ID:        id,
		Namespace: candidate.Namespace,
		Role:      candidate.Role,
		Key:       candidate.Key,
		Content:   candidate.Content,
		Metadata:  cloneMap(candidate.Metadata),
		UpdatedAt: time.Now().UTC(),
	}
	existing, err := m.store.GetMemory(ctx, id)
	if err != nil {
		return false, err
	}
	if existing != nil {
		switch m.policy.ConflictMode {
		case ConflictIgnore:
			return false, nil
		case ConflictMerge:
			if !strings.Contains(existing.Content, candidate.Content) {
				record.Content = strings.TrimSpace(existing.Content + "\n" + candidate.Content)
			} else {
				record.Content = existing.Content
			}
			record.Metadata = mergeMaps(existing.Metadata, candidate.Metadata)
		case ConflictReplace:
		}
	}

	if err := m.store.UpsertMemory(ctx, record); err != nil {
		return false, err
	}
	if m.vector != nil && m.embedder != nil &&
		(candidate.Role == RoleSemantic || candidate.Role == RoleProcedural) {
		vector, err := m.embedder.EmbedContent(ctx, record.Content)
		if err != nil {
			return false, err
		}
		metadata := cloneMap(record.Metadata)
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["id"] = record.ID
		metadata["content"] = record.Content
		metadata["scope"] = string(record.Namespace.Scope)
		metadata["scope_id"] = record.Namespace.ID
		metadata["role"] = string(record.Role)
		metadata["updated_at"] = record.UpdatedAt.Format(time.RFC3339Nano)
		if err := m.vector.Upsert(ctx, record.ID, vector, metadata); err != nil {
			return false, err
		}
	}
	return true, nil
}

func filterRoles(roles []Role, keep func(Role) bool) []Role {
	if len(roles) == 0 {
		return nil
	}
	var out []Role
	for _, role := range roles {
		if keep(role) {
			out = append(out, role)
		}
	}
	return out
}

func memoryID(candidate Candidate) string {
	key := candidate.Key
	if key == "" {
		key = candidate.Content
	}
	sum := sha256.Sum256([]byte(
		string(
			candidate.Namespace.Scope,
		) + ":" + candidate.Namespace.ID + ":" + string(
			candidate.Role,
		) + ":" + key,
	))
	return hex.EncodeToString(sum[:])
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mergeMaps(base, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := cloneMap(base)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func dedupeMemories(memories []Memory) []Memory {
	byID := make(map[string]Memory, len(memories))
	for _, memory := range memories {
		existing, ok := byID[memory.ID]
		if !ok || memory.Score > existing.Score || memory.UpdatedAt.After(existing.UpdatedAt) {
			byID[memory.ID] = memory
		}
	}
	out := make([]Memory, 0, len(byID))
	for _, memory := range byID {
		out = append(out, memory)
	}
	return out
}

func memoryFromVector(hit SearchResult) (Memory, bool) {
	scope, ok := hit.Metadata["scope"].(string)
	if !ok {
		return Memory{}, false
	}
	scopeID, ok := hit.Metadata["scope_id"].(string)
	if !ok {
		return Memory{}, false
	}
	role, ok := hit.Metadata["role"].(string)
	if !ok {
		return Memory{}, false
	}
	content, ok := hit.Metadata["content"].(string)
	if !ok {
		return Memory{}, false
	}
	var updatedAt time.Time
	if raw, ok := hit.Metadata["updated_at"].(string); ok {
		updatedAt, _ = time.Parse(time.RFC3339Nano, raw)
	}
	return Memory{
		ID: hit.ID,
		Namespace: Namespace{
			Scope: Scope(scope),
			ID:    scopeID,
		},
		Role:      Role(role),
		Content:   content,
		Metadata:  cloneMap(hit.Metadata),
		UpdatedAt: updatedAt,
		Score:     hit.Score,
	}, true
}
