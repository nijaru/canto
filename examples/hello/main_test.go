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
	if got, want := out.String(), "Hello from Canto.\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
