package llm

import "testing"

func TestStreamAccumulatorKeepsLatestUsage(t *testing.T) {
	var acc StreamAccumulator
	acc.Add(&Chunk{Usage: &Usage{InputTokens: 10, TotalTokens: 10}})
	acc.Add(&Chunk{Usage: &Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}})

	got := acc.Response().Usage
	if got.TotalTokens != 15 {
		t.Fatalf("TotalTokens = %d, want latest cumulative value 15", got.TotalTokens)
	}
	if got.InputTokens != 10 || got.OutputTokens != 5 {
		t.Fatalf("usage = %+v, want input 10/output 5", got)
	}
}

func TestStreamAccumulatorUpdatesToolCallsByID(t *testing.T) {
	var partial Call
	partial.ID = "call-1"
	partial.Type = "function"
	partial.Function.Name = "read"

	final := partial
	final.Function.Arguments = `{"path":"README.md"}`

	var acc StreamAccumulator
	acc.Add(&Chunk{Calls: []Call{partial}})
	acc.Add(&Chunk{Calls: []Call{final}})

	calls := acc.Response().Calls
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].Function.Arguments != final.Function.Arguments {
		t.Fatalf("arguments = %q, want %q", calls[0].Function.Arguments, final.Function.Arguments)
	}
}
