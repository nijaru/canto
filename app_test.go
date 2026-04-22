package canto

import (
	"context"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

func TestAgentBuilderSend(t *testing.T) {
	app, err := NewAgent("hello").
		Instructions("You are concise.").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "hello"})).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer app.Close()

	result, err := app.Send(t.Context(), "sess", "hi")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Content != "hello" {
		t.Fatalf("content = %q, want hello", result.Content)
	}
}

func TestAgentBuilderRegistersTools(t *testing.T) {
	testTool := tool.Func(
		"echo",
		"Echo input.",
		map[string]any{"type": "object"},
		func(_ context.Context, args string) (string, error) {
			return args, nil
		},
	)
	app, err := NewAgent("tools").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Tools(testTool).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer app.Close()

	if _, ok := app.Tools.Get("echo"); !ok {
		t.Fatal("expected echo tool to be registered")
	}
}
