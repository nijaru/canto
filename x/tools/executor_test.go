package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestExecutor_Timeout(t *testing.T) {
	e := NewExecutor(100*time.Millisecond, 1000)
	ctx := context.Background()

	// Command that sleeps longer than timeout
	_, err := e.Execute(ctx, "sleep", "1")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestExecutor_Truncation(t *testing.T) {
	e := NewExecutor(time.Second, 10)
	ctx := context.Background()

	// Command that outputs more than 10 bytes
	out, err := e.Execute(ctx, "echo", "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "[Output truncated due to size limits]") {
		t.Errorf("expected output to be truncated, got: %q", out)
	}
}
