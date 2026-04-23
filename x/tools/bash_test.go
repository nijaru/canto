package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashTool_Spec(t *testing.T) {
	b := &BashTool{}
	spec := b.Spec()
	if spec.Name != "bash" {
		t.Errorf("expected name 'bash', got %q", spec.Name)
	}
	if spec.Description == "" {
		t.Error("expected non-empty description")
	}
	if spec.Parameters == nil {
		t.Error("expected non-nil parameters")
	}
}

func TestBashTool_Execute_Echo(t *testing.T) {
	b := &BashTool{}
	out, err := b.Execute(context.Background(), `{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in output, got: %q", out)
	}
}

func TestBashTool_Execute_InvalidJSON(t *testing.T) {
	b := &BashTool{}
	_, err := b.Execute(context.Background(), `not-json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestBashTool_Execute_CommandFailure(t *testing.T) {
	b := &BashTool{}
	// Non-zero exit: error is returned; output contains only command stdout/stderr.
	_, err := b.Execute(context.Background(), `{"command": "exit 1"}`)
	if err == nil {
		t.Fatal("expected error for failing command, got nil")
	}
}

func TestBashTool_ExecuteUsesDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	b := &BashTool{Dir: dir}
	out, err := b.Execute(t.Context(), `{"command": "cat marker.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Fatalf("output = %q, want ok", out)
	}
}

func TestBashTool_ExecuteStreamingUsesDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	b := &BashTool{Dir: dir}
	var deltas []string
	for delta, err := range b.ExecuteStreaming(t.Context(), `{"command": "cat marker.txt"}`) {
		if err != nil {
			t.Fatalf("ExecuteStreaming: %v", err)
		}
		deltas = append(deltas, delta)
	}
	if strings.TrimSpace(strings.Join(deltas, "")) != "ok" {
		t.Fatalf("output = %q, want ok", strings.Join(deltas, ""))
	}
}

func TestBashTool_ExecuteStreaming(t *testing.T) {
	b := &BashTool{}
	ctx := context.Background()
	args := `{"command": "echo hello"}`

	var deltas []string
	var execErr error
	for delta, err := range b.ExecuteStreaming(ctx, args) {
		if err != nil {
			execErr = err
			break
		}
		deltas = append(deltas, delta)
	}

	if execErr != nil {
		t.Fatalf("unexpected error: %v", execErr)
	}
	if len(deltas) == 0 {
		t.Error("expected at least one delta, got 0")
	}
	combined := strings.Join(deltas, "")
	if !strings.Contains(combined, "hello") {
		t.Errorf("expected 'hello' in combined deltas, got: %q", combined)
	}
}
