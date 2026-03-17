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

func TestLazyToolProcessor_UnlocksFromHistory(t *testing.T) {
	reg := makeRegistry(3) // tool_0, tool_1, tool_2
	reg.Register(&mockTool{name: "search_tools"})
	p := NewLazyTools(reg)
	p.Threshold = 2 // 4 total > 2 → lazy mode

	// Seed session with a prior search_tools result that unlocked tool_1.
	sess := session.New("s3")
	specs := []llm.Spec{{Name: "tool_1", Description: "desc of tool_1"}}
	data, _ := json.Marshal(specs)
	_ = sess.Append(
		context.Background(),
		session.NewEvent("s3", session.MessageAdded, llm.Message{
			Role:    llm.RoleTool,
			Name:    "search_tools",
			Content: string(data),
		}),
	)

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
