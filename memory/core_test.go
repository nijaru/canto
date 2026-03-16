package memory

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nijaru/canto/session"
)

func TestCoreStore_Persona(t *testing.T) {
	store, err := NewCoreStore(filepath.Join(t.TempDir(), "core.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := t.Context()

	// nil when not set
	p, err := store.GetPersona(ctx, "sess-1")
	if err != nil || p != nil {
		t.Fatalf("expected nil persona, got %v %v", p, err)
	}

	want := &Persona{Name: "Aria", Description: "Research agent", Directives: "Be concise."}
	if err := store.SetPersona(ctx, "sess-1", want); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetPersona(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != want.Name || got.Description != want.Description {
		t.Errorf("persona mismatch: got %+v", got)
	}
}

func TestCoreStore_Episodes(t *testing.T) {
	store, err := NewCoreStore(filepath.Join(t.TempDir(), "core.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := t.Context()
	now := time.Now()

	ep1 := &session.Episode{
		ID:         "ep-1",
		SessionID:  "sess-1",
		AgentID:    "agent-1",
		StartTime:  now,
		EndTime:    now.Add(time.Minute),
		Conclusion: "Go is a compiled language.",
		Calls: []session.EpisodeCall{
			{Tool: "search", Args: `{"q":"golang"}`, Result: "Go is statically typed."},
		},
		TotalCost: 0.01,
	}
	ep2 := &session.Episode{
		ID:         "ep-2",
		SessionID:  "sess-1",
		AgentID:    "agent-1",
		StartTime:  now.Add(time.Minute),
		EndTime:    now.Add(2 * time.Minute),
		Conclusion: "Rust is a systems language with ownership.",
		Calls: []session.EpisodeCall{
			{Tool: "search", Args: `{"q":"rust"}`, Result: "Rust has no GC."},
		},
		TotalCost: 0.02,
	}

	if err := store.SaveEpisode(ctx, ep1); err != nil {
		t.Fatalf("SaveEpisode ep1: %v", err)
	}
	if err := store.SaveEpisode(ctx, ep2); err != nil {
		t.Fatalf("SaveEpisode ep2: %v", err)
	}

	// LoadEpisodes returns both, ordered by insertion.
	episodes, err := store.LoadEpisodes(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 2 {
		t.Fatalf("LoadEpisodes: got %d, want 2", len(episodes))
	}
	if episodes[0].ID != "ep-1" || episodes[1].ID != "ep-2" {
		t.Errorf("order wrong: got %q %q", episodes[0].ID, episodes[1].ID)
	}

	// LoadEpisodes for unknown session returns empty slice, not error.
	other, err := store.LoadEpisodes(ctx, "no-such-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("expected 0 episodes for unknown session, got %d", len(other))
	}

	// SaveEpisode is idempotent (upsert by ID).
	ep1.Conclusion = "Go is compiled and garbage-collected."
	if err := store.SaveEpisode(ctx, ep1); err != nil {
		t.Fatalf("SaveEpisode upsert: %v", err)
	}
	reloaded, err := store.LoadEpisodes(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 2 {
		t.Fatalf("after upsert: got %d episodes, want 2", len(reloaded))
	}
	if reloaded[0].Conclusion != "Go is compiled and garbage-collected." {
		t.Errorf("upsert did not update: %q", reloaded[0].Conclusion)
	}
}

func TestCoreStore_SearchEpisodes(t *testing.T) {
	store, err := NewCoreStore(filepath.Join(t.TempDir(), "core.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := t.Context()
	now := time.Now()

	episodes := []*session.Episode{
		{
			ID: "ep-golang", SessionID: "s1", AgentID: "a1",
			StartTime: now, EndTime: now.Add(time.Minute),
			Conclusion: "Go is a compiled, statically typed language.",
			Calls:      []session.EpisodeCall{{Tool: "search"}},
		},
		{
			ID: "ep-python", SessionID: "s1", AgentID: "a1",
			StartTime: now, EndTime: now.Add(time.Minute),
			Conclusion: "Python is a dynamically typed interpreted language.",
			Calls:      []session.EpisodeCall{{Tool: "search"}},
		},
	}
	for _, ep := range episodes {
		if err := store.SaveEpisode(ctx, ep); err != nil {
			t.Fatal(err)
		}
	}

	results, err := store.SearchEpisodes(ctx, "compiled", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchEpisodes: got %d results, want 1", len(results))
	}
	if results[0].ID != "ep-golang" {
		t.Errorf("wrong result: got %q, want %q", results[0].ID, "ep-golang")
	}
}
