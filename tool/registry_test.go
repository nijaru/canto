package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
)

// staticTool is a minimal Tool implementation for registry tests.
type staticTool struct {
	name   string
	result string
}

func (s *staticTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        s.name,
		Description: "A static test tool.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *staticTool) Execute(_ context.Context, _ string) (string, error) {
	return s.result, nil
}

func TestRegistry_NewRegistry(t *testing.T) {
	reg := NewRegistry()
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(reg.Specs()) != 0 {
		t.Errorf("expected empty registry, got %d tools", len(reg.Specs()))
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	tool := &staticTool{name: "my_tool", result: "ok"}
	reg.Register(tool)

	got, ok := reg.Get("my_tool")
	if !ok {
		t.Fatal("expected tool to be found")
	}
	if got.Spec().Name != "my_tool" {
		t.Errorf("expected name 'my_tool', got %q", got.Spec().Name)
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	reg := NewRegistry()
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Error("expected false for missing tool, got true")
	}
}

func TestRegistry_Specs(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&staticTool{name: "alpha", result: "a"})
	reg.Register(&staticTool{name: "beta", result: "b"})

	specs := reg.Specs()
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	names := make(map[string]bool, 2)
	for _, s := range specs {
		names[s.Name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("unexpected specs: %v", specs)
	}
}

func TestRegistry_Execute_Found(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&staticTool{name: "greeter", result: "hello"})

	result, err := reg.Execute(context.Background(), "greeter", "{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestRegistry_Execute_NotFound(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Execute(context.Background(), "missing", "{}")
	if err == nil {
		t.Fatal("expected error for missing tool, got nil")
	}
	if !strings.Contains(err.Error(), "tool not found: missing") {
		t.Errorf("unexpected error message: %v", err)
	}
}
