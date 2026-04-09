package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

type writeItem struct {
	Candidate Candidate
	Mode      WriteMode
}

func (m *Manager) Consolidate(
	ctx context.Context,
	input ConsolidationInput,
	consolidator Consolidator,
) (ConsolidationResult, error) {
	if m == nil || m.store == nil {
		return ConsolidationResult{}, fmt.Errorf("memory manager: store is required")
	}
	if consolidator == nil {
		return ConsolidationResult{}, fmt.Errorf("memory manager: consolidator is required")
	}
	memories, err := m.store.ListMemories(ctx, MemoryListInput{
		Namespaces:        input.Namespaces,
		Roles:             input.Roles,
		Limit:             input.Limit,
		IncludeForgotten:  input.IncludeForgotten,
		IncludeSuperseded: input.IncludeSuperseded,
	})
	if err != nil {
		return ConsolidationResult{}, err
	}
	plan, err := consolidator.Consolidate(ctx, slices.Clone(memories))
	if err != nil {
		return ConsolidationResult{}, err
	}
	result := ConsolidationResult{Examined: len(memories)}
	if len(plan.Upserts) > 0 {
		written, err := m.WriteBatch(ctx, plan.Upserts)
		if err != nil {
			return ConsolidationResult{}, err
		}
		result.Written = written
	}
	for _, forget := range plan.Forgets {
		if err := m.Forget(ctx, forget.ID, forget.Reason); err != nil {
			return ConsolidationResult{}, err
		}
		result.Forgotten++
	}
	return result, nil
}

func (m *Manager) prepareWriteItems(
	ctx context.Context,
	inputs []WriteInput,
) ([]writeItem, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	raw := make([]writeItem, 0, len(inputs))
	for _, input := range inputs {
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
			return nil, err
		}
		mode := input.Mode
		if mode == "" {
			mode = m.policy.DefaultMode
		}
		for _, extracted := range candidates {
			raw = append(raw, writeItem{
				Candidate: extracted,
				Mode:      mode,
			})
		}
	}
	deduped, err := m.dedupeWriteItems(ctx, raw)
	if err != nil {
		return nil, err
	}
	return deduped, nil
}

func (m *Manager) dedupeWriteItems(
	ctx context.Context,
	items []writeItem,
) ([]writeItem, error) {
	if len(items) == 0 {
		return nil, nil
	}
	candidates := make([]Candidate, 0, len(items))
	modeByKey := make(map[string]WriteMode, len(items))
	for _, item := range items {
		candidates = append(candidates, item.Candidate)
		key := candidateIdentity(item.Candidate)
		if modeByKey[key] != WriteSync && item.Mode == WriteSync {
			modeByKey[key] = WriteSync
			continue
		}
		if _, ok := modeByKey[key]; !ok {
			modeByKey[key] = item.Mode
		}
	}
	var deduped []Candidate
	var err error
	if m.policy.Deduper != nil {
		deduped, err = m.policy.Deduper(ctx, candidates)
	} else {
		deduped = dedupeCandidates(candidates, m.policy.ConflictMode)
	}
	if err != nil {
		return nil, err
	}
	result := make([]writeItem, 0, len(deduped))
	for _, candidate := range deduped {
		mode := modeByKey[candidateIdentity(candidate)]
		if mode == "" {
			mode = m.policy.DefaultMode
		}
		result = append(result, writeItem{
			Candidate: candidate,
			Mode:      mode,
		})
	}
	return result, nil
}

func dedupeCandidates(
	candidates []Candidate,
	conflictMode ConflictMode,
) []Candidate {
	if len(candidates) == 0 {
		return nil
	}
	deduped := make(map[string]Candidate, len(candidates))
	order := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		key := candidateIdentity(candidate)
		current, ok := deduped[key]
		if !ok {
			deduped[key] = candidate
			order = append(order, key)
			continue
		}
		deduped[key] = mergeCandidate(current, candidate, conflictMode)
	}
	result := make([]Candidate, 0, len(order))
	for _, key := range order {
		result = append(result, deduped[key])
	}
	return result
}

func candidateIdentity(candidate Candidate) string {
	if candidate.Role == RoleCore {
		name := candidate.Key
		if name == "" {
			name = "default"
		}
		return strings.Join([]string{
			string(candidate.Namespace.Scope),
			candidate.Namespace.ID,
			string(RoleCore),
			name,
		}, ":")
	}
	return memoryID(candidate)
}

func mergeCandidate(
	base Candidate,
	extra Candidate,
	conflictMode ConflictMode,
) Candidate {
	switch conflictMode {
	case ConflictMerge:
		if extra.Content != "" && !strings.Contains(base.Content, extra.Content) {
			if base.Content == "" {
				base.Content = extra.Content
			} else {
				base.Content = strings.TrimSpace(base.Content + "\n" + extra.Content)
			}
		}
	case ConflictIgnore:
	default:
		if extra.Content != "" {
			base.Content = extra.Content
		}
	}
	base.Metadata = mergeMaps(base.Metadata, extra.Metadata)
	if extra.Importance > base.Importance {
		base.Importance = extra.Importance
	}
	if base.ObservedAt == nil {
		base.ObservedAt = cloneTime(extra.ObservedAt)
	}
	if base.ValidFrom == nil {
		base.ValidFrom = cloneTime(extra.ValidFrom)
	}
	if base.ValidTo == nil {
		base.ValidTo = cloneTime(extra.ValidTo)
	}
	if base.Supersedes == "" {
		base.Supersedes = extra.Supersedes
	}
	return base
}
