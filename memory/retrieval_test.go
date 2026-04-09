package memory

import (
	"slices"
	"testing"
	"time"
)

func TestDefaultPlanner(t *testing.T) {
	planner := DefaultPlanner{MaxCore: 4}
	requests := planner.Plan(Query{
		Roles:         []Role{RoleCore, RoleSemantic},
		Text:          "bun",
		IncludeRecent: true,
		UseSemantic:   true,
		Limit:         6,
	}, RetrievalCapabilities{
		Core:     true,
		Recent:   true,
		Text:     true,
		Semantic: true,
	})
	if len(requests) != 4 {
		t.Fatalf("Plan returned %d requests, want 4", len(requests))
	}
	got := make([]RetrievalSourceName, 0, len(requests))
	for _, request := range requests {
		got = append(got, request.Source)
	}
	want := []RetrievalSourceName{
		RetrievalCore,
		RetrievalText,
		RetrievalRecent,
		RetrievalVector,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Plan sources = %#v, want %#v", got, want)
	}
	if requests[0].Limit != 4 {
		t.Fatalf("core limit = %d, want 4", requests[0].Limit)
	}
}

func TestRRFFuser_FusesAcrossSources(t *testing.T) {
	now := time.Now().UTC()
	results := DefaultRRFFuser().Fuse(Query{}, []RetrievalResultSet{
		{
			Source: RetrievalText,
			Hits: []Memory{
				{ID: "shared", UpdatedAt: now},
				{ID: "text-only", UpdatedAt: now.Add(-time.Minute)},
			},
		},
		{
			Source: RetrievalVector,
			Hits: []Memory{
				{ID: "shared", UpdatedAt: now.Add(time.Minute)},
				{ID: "vector-only", UpdatedAt: now},
			},
		},
	}, 4)
	if len(results) != 3 {
		t.Fatalf("Fuse returned %d results, want 3", len(results))
	}
	if results[0].ID != "shared" {
		t.Fatalf("first fused hit = %q, want shared", results[0].ID)
	}
	if !results[0].UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("shared hit should keep freshest payload, got %v", results[0].UpdatedAt)
	}
	if results[0].Score <= results[1].Score {
		t.Fatalf(
			"shared fused score %f should exceed single-source score %f",
			results[0].Score,
			results[1].Score,
		)
	}
}
