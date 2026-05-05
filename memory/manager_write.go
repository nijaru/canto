package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (m *Manager) Write(ctx context.Context, input WriteInput) (WriteResult, error) {
	return m.WriteBatch(ctx, []WriteInput{input})
}

func (m *Manager) WriteBatch(
	ctx context.Context,
	inputs []WriteInput,
) (WriteResult, error) {
	if m == nil || m.store == nil {
		return WriteResult{}, fmt.Errorf("memory manager: store is required")
	}
	items, err := m.prepareWriteItems(ctx, inputs)
	if err != nil {
		return WriteResult{}, err
	}
	var result WriteResult
	for _, item := range items {
		candidate := item.Candidate
		if candidate.Content == "" {
			continue
		}
		if candidate.Importance < m.policy.ImportanceThreshold {
			continue
		}
		id := memoryID(candidate)
		result.IDs = append(result.IDs, id)
		if item.Mode == WriteAsync {
			result.Pending++
			m.asyncWG.Go(func() {
				_, _ = m.storeCandidate(context.Background(), id, candidate)
			})
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
	if m == nil || m.store == nil {
		return fmt.Errorf("memory manager: store is required")
	}
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
