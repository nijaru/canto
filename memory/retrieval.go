package memory

import (
	"slices"
	"strings"
)

type RetrievalSourceName string

const (
	RetrievalCore   RetrievalSourceName = "core"
	RetrievalRecent RetrievalSourceName = "recent"
	RetrievalText   RetrievalSourceName = "text"
	RetrievalVector RetrievalSourceName = "vector"
)

type RetrievalRequest struct {
	Source RetrievalSourceName
	Limit  int
}

type RetrievalResultSet struct {
	Source RetrievalSourceName
	Hits   []Memory
}

type RetrievalCapabilities struct {
	Core     bool
	Recent   bool
	Text     bool
	Semantic bool
}

type RetrievalPlanner interface {
	Plan(query Query, caps RetrievalCapabilities) []RetrievalRequest
}

type RetrievalFuser interface {
	Fuse(query Query, sets []RetrievalResultSet, limit int) []Memory
}

type DefaultPlanner struct {
	MaxCore int
}

func (p DefaultPlanner) Plan(
	query Query,
	caps RetrievalCapabilities,
) []RetrievalRequest {
	limit := query.Limit
	if limit <= 0 {
		limit = 5
	}
	maxCore := p.MaxCore
	if maxCore <= 0 {
		maxCore = 8
	}
	includeCore := query.IncludeCore || len(query.Roles) == 0 ||
		slices.Contains(query.Roles, RoleCore)
	memoryRoles := filterRoles(query.Roles, func(role Role) bool { return role != RoleCore })
	var requests []RetrievalRequest
	if includeCore && caps.Core {
		coreLimit := limit
		if len(memoryRoles) > 0 || len(query.Roles) == 0 {
			coreLimit = min(limit, maxCore)
		}
		requests = append(requests, RetrievalRequest{
			Source: RetrievalCore,
			Limit:  coreLimit,
		})
	}
	if query.Text != "" && caps.Text {
		requests = append(requests, RetrievalRequest{
			Source: RetrievalText,
			Limit:  limit,
		})
	}
	if query.IncludeRecent && caps.Recent {
		requests = append(requests, RetrievalRequest{
			Source: RetrievalRecent,
			Limit:  limit,
		})
	}
	if query.UseSemantic && query.Text != "" && caps.Semantic {
		requests = append(requests, RetrievalRequest{
			Source: RetrievalVector,
			Limit:  limit,
		})
	}
	return requests
}

type RRFFuser struct {
	K       int
	Weights map[RetrievalSourceName]float32
}

func DefaultRRFFuser() RRFFuser {
	return RRFFuser{
		K: 60,
		Weights: map[RetrievalSourceName]float32{
			RetrievalCore:   0.9,
			RetrievalRecent: 0.85,
			RetrievalText:   1.15,
			RetrievalVector: 1.0,
		},
	}
}

func (f RRFFuser) Fuse(
	_ Query,
	sets []RetrievalResultSet,
	limit int,
) []Memory {
	if limit <= 0 {
		limit = 5
	}
	k := f.K
	if k <= 0 {
		k = 60
	}
	type aggregate struct {
		memory Memory
		score  float32
	}
	byID := make(map[string]aggregate)
	for _, set := range sets {
		weight := f.weight(set.Source)
		for rank, hit := range set.Hits {
			rrf := weight / float32(k+rank+1)
			current, ok := byID[hit.ID]
			if !ok || hit.UpdatedAt.After(current.memory.UpdatedAt) {
				current.memory = hit
			}
			current.score += rrf
			byID[hit.ID] = current
		}
	}
	results := make([]Memory, 0, len(byID))
	for _, agg := range byID {
		agg.memory.Score = agg.score
		results = append(results, agg.memory)
	}
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
	return results
}

func (f RRFFuser) weight(source RetrievalSourceName) float32 {
	if len(f.Weights) == 0 {
		return 1.0
	}
	weight, ok := f.Weights[source]
	if !ok || weight <= 0 {
		return 1.0
	}
	return weight
}
