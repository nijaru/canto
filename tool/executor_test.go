package tool

import (
	"context"
	"os/exec"
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

func TestCodeExecutionTool_Python(t *testing.T) {
	// Skip if python3 is not available
	_, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found in PATH")
	}

	c := NewCodeExecutionTool("python")
	ctx := context.Background()

	out, err := c.Execute(ctx, `{"code": "print('hello from python')"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.TrimSpace(out) != "hello from python" {
		t.Errorf("expected %q, got %q", "hello from python", out)
	}
}
