package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellTool_Spec(t *testing.T) {
	b := &ShellTool{}
	spec := b.Spec()
	if spec.Name != "shell" {
		t.Errorf("expected name 'shell', got %q", spec.Name)
	}
	if spec.Description == "" {
		t.Error("expected non-empty description")
	}
	if spec.Parameters == nil {
		t.Error("expected non-nil parameters")
	}
}

func TestShellTool_Execute_Echo(t *testing.T) {
	b := &ShellTool{}
	out, err := b.Execute(context.Background(), `{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in output, got: %q", out)
	}
}

func TestShellTool_Execute_InvalidJSON(t *testing.T) {
	b := &ShellTool{}
	_, err := b.Execute(context.Background(), `not-json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestShellTool_Execute_CommandFailure(t *testing.T) {
	b := &ShellTool{}
	// Non-zero exit: error is returned; output contains only command stdout/stderr.
	_, err := b.Execute(context.Background(), `{"command": "exit 1"}`)
	if err == nil {
		t.Fatal("expected error for failing command, got nil")
	}
}

func TestShellTool_ExecuteUsesDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	b := &ShellTool{Dir: dir}
	out, err := b.Execute(t.Context(), `{"command": "cat marker.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Fatalf("output = %q, want ok", out)
	}
}

func TestShellTool_ExecuteUsesConfiguredShell(t *testing.T) {
	dir := t.TempDir()
	shellPath := filepath.Join(dir, "test-shell")
	if err := os.WriteFile(
		shellPath,
		[]byte("#!/bin/sh\nprintf '%s:%s' \"$1\" \"$2\"\n"),
		0o755,
	); err != nil {
		t.Fatalf("write shell: %v", err)
	}

	out, err := (&ShellTool{
		Shell:       shellPath,
		CommandFlag: "--run",
	}).Execute(t.Context(), `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "--run:echo hello" {
		t.Fatalf("output = %q, want configured shell invocation", out)
	}
}

func TestShellTool_ExecuteStreamingUsesDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	b := &ShellTool{Dir: dir}
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

func TestShellTool_ExecuteStreaming(t *testing.T) {
	b := &ShellTool{}
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

func TestShellTool_ExecuteStreamingStopsCommandWhenConsumerStops(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "survived")
	b := &ShellTool{Dir: dir}

	for delta, err := range b.ExecuteStreaming(t.Context(),
		`{"command": "printf 'start\n'; sleep 1; touch survived"}`,
	) {
		if err != nil {
			t.Fatalf("ExecuteStreaming: %v", err)
		}
		if !strings.Contains(delta, "start") {
			t.Fatalf("first delta = %q, want start", delta)
		}
		break
	}

	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker stat err = %v, want not exist", err)
	}
}
