package tools

import (
	"context"
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
