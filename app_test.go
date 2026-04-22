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
		Model("faux").
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
		Model("faux").
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

func TestAgentBuilderRequiresModel(t *testing.T) {
	_, err := NewAgent("missing-model").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Build()
	if err == nil {
		t.Fatal("expected missing model error")
	}
	if err.Error() != "canto app: model is required" {
		t.Fatalf("error = %q, want model required", err)
	}
}
