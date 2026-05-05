package memory

import (
	"context"
	"fmt"
	"slices"
	"time"
)

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
