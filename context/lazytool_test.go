package context

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// mockTool is a minimal Tool implementation for tests.
type mockTool struct{ name string }

func (m *mockTool) Spec() llm.Spec {
	return llm.Spec{Name: m.name, Description: "desc of " + m.name}
}
func (m *mockTool) Execute(_ context.Context, _ string) (string, error) { return "", nil }

func makeRegistry(n int) *tool.Registry {
	reg := tool.NewRegistry()
	for i := range n {
		reg.Register(&mockTool{name: fmt.Sprintf("tool_%d", i)})
	}
	return reg
}

func TestLazyToolProcessor_BelowThreshold(t *testing.T) {
	reg := makeRegistry(5)
	p := NewLazyTools(reg)
	p.Threshold = 10

	sess := session.New("s1")
	req := &llm.Request{}
	if err := p.Process(context.Background(), nil, "", sess, req); err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 5 {
		t.Errorf("expected 5 tools, got %d", len(req.Tools))
	}
}

func TestLazyToolProcessor_AboveThreshold_OnlySearchTool(t *testing.T) {
	reg := makeRegistry(25)
	// Register a fake search_tools tool.
	reg.Register(&mockTool{name: "search_tools"})
	p := NewLazyTools(reg)
	p.Threshold = 10

	sess := session.New("s2")
	req := &llm.Request{}
	if err := p.Process(context.Background(), nil, "", sess, req); err != nil {
		t.Fatal(err)
	}
	// Only search_tools should be in req.Tools (no prior history).
	if len(req.Tools) != 1 || req.Tools[0].Name != "search_tools" {
		t.Errorf("expected only search_tools, got %v", req.Tools)
	}
}

func TestSearchUnlockedTools(t *testing.T) {
	sess := session.New("s-tools")
	specs := []llm.Spec{{Name: "tool_1", Description: "desc of tool_1"}}
	data, err := json.Marshal(specs)
	if err != nil {
		t.Fatalf("marshal specs: %v", err)
	}
	if err := sess.Append(context.Background(), session.NewToolCompletedEvent(sess.ID(), session.ToolCompletedData{
		Tool:   "search_tools",
		ID:     "call_1",
		Output: string(data),
	})); err != nil {
		t.Fatalf("append tool completion: %v", err)
	}

	unlocked, err := SearchUnlockedTools(sess)
	if err != nil {
		t.Fatalf("search unlocked tools: %v", err)
	}
	if _, ok := unlocked["tool_1"]; !ok {
		t.Fatal("expected tool_1 to be unlocked")
	}
}

func TestLazyToolProcessor_UnlocksFromSessionState(t *testing.T) {
	reg := makeRegistry(3) // tool_0, tool_1, tool_2
	reg.Register(&mockTool{name: "search_tools"})
	p := NewLazyTools(reg)
	p.Threshold = 2 // 4 total > 2 → lazy mode

	// Seed session with a prior search_tools result that unlocked tool_1.
	sess := session.New("s3")
	specs := []llm.Spec{{Name: "tool_1", Description: "desc of tool_1"}}
	data, err := json.Marshal(specs)
	if err != nil {
		t.Fatalf("marshal specs: %v", err)
	}
	if err := sess.Append(context.Background(), session.NewToolCompletedEvent(sess.ID(), session.ToolCompletedData{
		Tool:   "search_tools",
		ID:     "call_1",
		Output: string(data),
	})); err != nil {
		t.Fatalf("append tool completion: %v", err)
	}

	req := &llm.Request{}
	if err := p.Process(context.Background(), nil, "", sess, req); err != nil {
		t.Fatal(err)
	}

	names := make(map[string]bool)
	for _, spec := range req.Tools {
		names[spec.Name] = true
	}
	if !names["search_tools"] {
		t.Error("expected search_tools in req.Tools")
	}
	if !names["tool_1"] {
		t.Error("expected tool_1 (unlocked) in req.Tools")
	}
	if names["tool_0"] || names["tool_2"] {
		t.Error("expected tool_0 and tool_2 NOT in req.Tools (not unlocked)")
	}
}

func TestLazyToolProcessor_UnlockedToolsAreSorted(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&mockTool{name: "tool_b"})
	reg.Register(&mockTool{name: "tool_a"})
	reg.Register(&mockTool{name: "search_tools"})

	p := NewLazyTools(reg)
	p.Threshold = 1

	sess := session.New("s4")
	specs := []llm.Spec{
		{Name: "tool_b", Description: "desc of tool_b"},
		{Name: "tool_a", Description: "desc of tool_a"},
	}
	data, err := json.Marshal(specs)
	if err != nil {
		t.Fatalf("marshal specs: %v", err)
	}
	if err := sess.Append(context.Background(), session.NewToolCompletedEvent(sess.ID(), session.ToolCompletedData{
		Tool:   "search_tools",
		ID:     "call_1",
		Output: string(data),
	})); err != nil {
		t.Fatalf("append tool completion: %v", err)
	}

	req := &llm.Request{}
	if err := p.Process(context.Background(), nil, "", sess, req); err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(req.Tools))
	}
	if req.Tools[0].Name != "search_tools" || req.Tools[1].Name != "tool_a" || req.Tools[2].Name != "tool_b" {
		t.Fatalf("unexpected tool order: %v", []string{req.Tools[0].Name, req.Tools[1].Name, req.Tools[2].Name})
	}
}
