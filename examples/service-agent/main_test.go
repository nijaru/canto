package main

import (
	"bytes"
	"testing"
)

func TestRun(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.Context(), &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	want := "Canto can expose typed service/API tools with explicit schema, approval, and metadata boundaries.\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
