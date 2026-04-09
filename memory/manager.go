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
	ID           string         `json:"id"`
	Namespace    Namespace      `json:"namespace"`
	Role         Role           `json:"role"`
	Key          string         `json:"key,omitzero"`
	Content      string         `json:"content"`
	Metadata     map[string]any `json:"metadata,omitzero"`
	ObservedAt   *time.Time     `json:"observed_at,omitzero"`
	ValidFrom    *time.Time     `json:"valid_from,omitzero"`
	ValidTo      *time.Time     `json:"valid_to,omitzero"`
	Supersedes   string         `json:"supersedes,omitzero"`
	SupersededBy string         `json:"superseded_by,omitzero"`
	ForgottenAt  *time.Time     `json:"forgotten_at,omitzero"`
	UpdatedAt    time.Time      `json:"updated_at"`
	Score        float32        `json:"score,omitzero"`
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
	ObservedAt *time.Time     `json:"observed_at,omitzero"`
	ValidFrom  *time.Time     `json:"valid_from,omitzero"`
	ValidTo    *time.Time     `json:"valid_to,omitzero"`
	Supersedes string         `json:"supersedes,omitzero"`
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
	ObservedAt *time.Time
	ValidFrom  *time.Time
	ValidTo    *time.Time
	Supersedes string
	Importance float64
	Mode       WriteMode
}

type WriteResult struct {
	Stored  int
	Pending int
	IDs     []string
}

type Query struct {
	Namespaces        []Namespace
	Roles             []Role
	Text              string
	Limit             int
	Filters           map[string]any
	UseSemantic       bool
	IncludeCore       bool
	IncludeRecent     bool
	ValidAt           *time.Time
	ObservedAfter     *time.Time
	ObservedBefore    *time.Time
	IncludeForgotten  bool
	IncludeSuperseded bool
}

type Manager struct {
	store          Store
	vector         VectorStore
	embedder       llm.Embedder
	policy         WritePolicy
	retrievePolicy RetrievePolicy

	asyncWG sync.WaitGroup
}

// NewManager builds a memory manager around the provided durable store.
func NewManager(store Store, opts ...ManagerOption) *Manager {
	manager := &Manager{
		store: store,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(manager)
		}
	}
	if manager.policy.ConflictMode == "" {
		manager.policy.ConflictMode = ConflictReplace
	}
	if manager.policy.DefaultMode == "" {
		manager.policy.DefaultMode = WriteSync
	}
	return manager
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
		ObservedAt: cloneTime(input.ObservedAt),
		ValidFrom:  cloneTime(input.ValidFrom),
		ValidTo:    cloneTime(input.ValidTo),
		Supersedes: input.Supersedes,
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

func (m *Manager) Forget(ctx context.Context, id, reason string) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("memory manager: store is required")
	}
	record, err := m.store.GetMemory(ctx, id)
	if err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("memory manager: memory %q not found", id)
	}
	now := time.Now().UTC()
	record.ForgottenAt = &now
	record.UpdatedAt = now
	if reason != "" {
		record.Metadata = mergeMaps(record.Metadata, map[string]any{
			"forgotten_reason": reason,
		})
	}
	if err := m.store.UpsertMemory(ctx, *record); err != nil {
		return err
	}
	return m.syncVectorMemory(ctx, *record)
}

func (m *Manager) Retrieve(ctx context.Context, query Query) ([]Memory, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("memory manager: store is required")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 5
	}
	planner := m.retrievePolicy.Planner
	if planner == nil {
		planner = DefaultPlanner{}
	}
	requests := planner.Plan(query, RetrievalCapabilities{
		Core:     true,
		Recent:   true,
		Text:     true,
		Semantic: m.vector != nil && m.embedder != nil,
	})
	var sets []RetrievalResultSet
	for _, request := range requests {
		hits, err := m.retrieveSource(ctx, request, query)
		if err != nil {
			return nil, err
		}
		if len(hits) == 0 {
			continue
		}
		sets = append(sets, RetrievalResultSet{
			Source: request.Source,
			Hits:   hits,
		})
	}
	fuser := m.retrievePolicy.Fuser
	if fuser == nil {
		fuser = DefaultRRFFuser()
	}
	results := fuser.Fuse(query, sets, limit)
	results = filterMemories(results, query)
	if m.retrievePolicy.Postprocess != nil {
		var err error
		results, err = m.retrievePolicy.Postprocess(query, slices.Clone(results))
		if err != nil {
			return nil, err
		}
		if len(results) > limit {
			results = results[:limit]
		}
	}
	return results, nil
}

func (m *Manager) retrieveSource(
	ctx context.Context,
	request RetrievalRequest,
	query Query,
) ([]Memory, error) {
	switch request.Source {
	case RetrievalCore:
		return m.retrieveCoreBlocks(ctx, query, request.Limit)
	case RetrievalRecent:
		return m.retrieveRecentMemories(ctx, query, request.Limit)
	case RetrievalText:
		return m.retrieveTextMemories(ctx, query, request.Limit)
	case RetrievalVector:
		return m.retrieveVectorMemories(ctx, query, request.Limit)
	default:
		return nil, fmt.Errorf("memory manager: unknown retrieval source %q", request.Source)
	}
}

func (m *Manager) retrieveCoreBlocks(
	ctx context.Context,
	query Query,
	limit int,
) ([]Memory, error) {
	blocks, err := m.store.ListBlocks(ctx, BlockListInput{
		Namespaces: query.Namespaces,
		Limit:      limit,
	})
	if err != nil {
		return nil, err
	}
	results := make([]Memory, 0, len(blocks))
	for _, block := range blocks {
		if !matchesFilters(block.Metadata, query.Filters) {
			continue
		}
		results = append(results, Memory{
			ID:        block.Name,
			Namespace: block.Namespace,
			Role:      RoleCore,
			Key:       block.Name,
			Content:   block.Content,
			Metadata:  cloneMap(block.Metadata),
			UpdatedAt: block.UpdatedAt,
		})
	}
	return results, nil
}

func (m *Manager) retrieveRecentMemories(
	ctx context.Context,
	query Query,
	limit int,
) ([]Memory, error) {
	roles := filterRoles(query.Roles, func(role Role) bool { return role != RoleCore })
	results, err := m.store.ListMemories(ctx, MemoryListInput{
		Namespaces:        query.Namespaces,
		Roles:             roles,
		Limit:             limit,
		Filters:           query.Filters,
		ValidAt:           query.ValidAt,
		ObservedAfter:     query.ObservedAfter,
		ObservedBefore:    query.ObservedBefore,
		IncludeForgotten:  query.IncludeForgotten,
		IncludeSuperseded: query.IncludeSuperseded,
	})
	if err != nil {
		return nil, err
	}
	return filterMemories(results, query), nil
}

func (m *Manager) retrieveTextMemories(
	ctx context.Context,
	query Query,
	limit int,
) ([]Memory, error) {
	roles := filterRoles(query.Roles, func(role Role) bool { return role != RoleCore })
	results, err := m.store.SearchMemories(ctx, SearchInput{
		Namespaces:        query.Namespaces,
		Roles:             roles,
		Text:              query.Text,
		Limit:             limit,
		Filters:           query.Filters,
		IncludeRecent:     query.IncludeRecent,
		ValidAt:           query.ValidAt,
		ObservedAfter:     query.ObservedAfter,
		ObservedBefore:    query.ObservedBefore,
		IncludeForgotten:  query.IncludeForgotten,
		IncludeSuperseded: query.IncludeSuperseded,
	})
	if err != nil {
		return nil, err
	}
	return filterMemories(results, query), nil
}

func (m *Manager) retrieveVectorMemories(
	ctx context.Context,
	query Query,
	limit int,
) ([]Memory, error) {
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
	roles := filterRoles(query.Roles, func(role Role) bool { return role != RoleCore })
	if len(roles) == 1 {
		filter["role"] = string(roles[0])
	}
	vectorHits, err := m.vector.Search(ctx, vector, limit, filter)
	if err != nil {
		return nil, err
	}
	results := make([]Memory, 0, len(vectorHits))
	for _, hit := range vectorHits {
		memory, ok := memoryFromVector(hit)
		if !ok {
			continue
		}
		if !matchesFilters(memory.Metadata, query.Filters) {
			continue
		}
		results = append(results, memory)
	}
	return filterMemories(results, query), nil
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
		ID:         id,
		Namespace:  candidate.Namespace,
		Role:       candidate.Role,
		Key:        candidate.Key,
		Content:    candidate.Content,
		Metadata:   cloneMap(candidate.Metadata),
		ObservedAt: cloneTime(candidate.ObservedAt),
		ValidFrom:  cloneTime(candidate.ValidFrom),
		ValidTo:    cloneTime(candidate.ValidTo),
		Supersedes: candidate.Supersedes,
		UpdatedAt:  time.Now().UTC(),
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
		inheritMemoryLifecycle(&record, existing)
	}

	if err := m.store.UpsertMemory(ctx, record); err != nil {
		return false, err
	}
	if err := m.syncVectorMemory(ctx, record); err != nil {
		return false, err
	}
	if candidate.Supersedes != "" {
		if err := m.markSuperseded(ctx, candidate.Supersedes, record); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (m *Manager) markSuperseded(
	ctx context.Context,
	predecessorID string,
	successor Memory,
) error {
	predecessor, err := m.store.GetMemory(ctx, predecessorID)
	if err != nil {
		return err
	}
	if predecessor == nil {
		return nil
	}
	predecessor.SupersededBy = successor.ID
	predecessor.UpdatedAt = successor.UpdatedAt
	if predecessor.ValidTo == nil && successor.ValidFrom != nil {
		predecessor.ValidTo = cloneTime(successor.ValidFrom)
	}
	if err := m.store.UpsertMemory(ctx, *predecessor); err != nil {
		return err
	}
	return m.syncVectorMemory(ctx, *predecessor)
}

func (m *Manager) syncVectorMemory(ctx context.Context, record Memory) error {
	if m.vector == nil || m.embedder == nil {
		return nil
	}
	if record.Role != RoleSemantic && record.Role != RoleProcedural {
		return nil
	}
	vector, err := m.embedder.EmbedContent(ctx, record.Content)
	if err != nil {
		return err
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
	if record.ObservedAt != nil {
		metadata["observed_at"] = record.ObservedAt.Format(time.RFC3339Nano)
	}
	if record.ValidFrom != nil {
		metadata["valid_from"] = record.ValidFrom.Format(time.RFC3339Nano)
	}
	if record.ValidTo != nil {
		metadata["valid_to"] = record.ValidTo.Format(time.RFC3339Nano)
	}
	if record.Supersedes != "" {
		metadata["supersedes"] = record.Supersedes
	}
	if record.SupersededBy != "" {
		metadata["superseded_by"] = record.SupersededBy
	}
	if record.ForgottenAt != nil {
		metadata["forgotten_at"] = record.ForgottenAt.Format(time.RFC3339Nano)
	}
	return m.vector.Upsert(ctx, record.ID, vector, metadata)
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

func cloneTime(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func inheritMemoryLifecycle(dst *Memory, existing *Memory) {
	if dst.ObservedAt == nil {
		dst.ObservedAt = cloneTime(existing.ObservedAt)
	}
	if dst.ValidFrom == nil {
		dst.ValidFrom = cloneTime(existing.ValidFrom)
	}
	if dst.ValidTo == nil {
		dst.ValidTo = cloneTime(existing.ValidTo)
	}
	if dst.Supersedes == "" {
		dst.Supersedes = existing.Supersedes
	}
	if dst.SupersededBy == "" {
		dst.SupersededBy = existing.SupersededBy
	}
	if dst.ForgottenAt == nil {
		dst.ForgottenAt = cloneTime(existing.ForgottenAt)
	}
}

func filterMemories(memories []Memory, query Query) []Memory {
	out := memories[:0]
	for _, memory := range memories {
		if !matchesLifecycle(memory, query) {
			continue
		}
		out = append(out, memory)
	}
	return out
}

func matchesLifecycle(memory Memory, query Query) bool {
	if !query.IncludeForgotten && memory.ForgottenAt != nil {
		return false
	}
	if !query.IncludeSuperseded && memory.SupersededBy != "" {
		return false
	}
	if query.ValidAt != nil {
		if memory.ValidFrom != nil && memory.ValidFrom.After(*query.ValidAt) {
			return false
		}
		if memory.ValidTo != nil && memory.ValidTo.Before(*query.ValidAt) {
			return false
		}
	}
	if query.ObservedAfter != nil {
		if memory.ObservedAt == nil || memory.ObservedAt.Before(*query.ObservedAfter) {
			return false
		}
	}
	if query.ObservedBefore != nil {
		if memory.ObservedAt == nil || memory.ObservedAt.After(*query.ObservedBefore) {
			return false
		}
	}
	return true
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
	observedAt := parseMetadataTime(hit.Metadata, "observed_at")
	validFrom := parseMetadataTime(hit.Metadata, "valid_from")
	validTo := parseMetadataTime(hit.Metadata, "valid_to")
	forgottenAt := parseMetadataTime(hit.Metadata, "forgotten_at")
	supersedes, _ := hit.Metadata["supersedes"].(string)
	supersededBy, _ := hit.Metadata["superseded_by"].(string)
	return Memory{
		ID: hit.ID,
		Namespace: Namespace{
			Scope: Scope(scope),
			ID:    scopeID,
		},
		Role:         Role(role),
		Content:      content,
		Metadata:     cloneMap(hit.Metadata),
		ObservedAt:   observedAt,
		ValidFrom:    validFrom,
		ValidTo:      validTo,
		Supersedes:   supersedes,
		SupersededBy: supersededBy,
		ForgottenAt:  forgottenAt,
		UpdatedAt:    updatedAt,
		Score:        hit.Score,
	}, true
}

func parseMetadataTime(metadata map[string]any, key string) *time.Time {
	raw, ok := metadata[key].(string)
	if !ok || raw == "" {
		return nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil
	}
	return &value
}
