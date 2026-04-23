package canto

import (
	"context"
	"errors"
	"testing"

	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

func TestAgentBuilderSend(t *testing.T) {
	app, err := NewAgent("hello").
		Instructions("You are concise.").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "hello"})).
		Ephemeral().
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
		Ephemeral().
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

func TestAgentBuilderRequiresSessionStore(t *testing.T) {
	_, err := NewAgent("missing-store").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Build()
	if err == nil {
		t.Fatal("expected missing session store error")
	}
	if err.Error() != "canto app: session store is required; call SessionStore or Ephemeral" {
		t.Fatalf("error = %q, want session store required", err)
	}
}

func TestAgentBuilderCompactionRecoversOverflow(t *testing.T) {
	overflow := errors.New("context_length_exceeded")
	provider := llm.NewFauxProvider(
		"faux",
		llm.FauxStep{Err: overflow},
		llm.FauxStep{Content: "recovered"},
	)
	provider.IsContextOverflowFn = func(err error) bool {
		return errors.Is(err, overflow)
	}

	app, err := NewAgent("recover").
		Model("faux").
		Provider(provider).
		Ephemeral().
		Compaction(governor.CompactOptions{
			MaxTokens:  1000,
			OffloadDir: t.TempDir(),
		}).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer app.Close()

	result, err := app.Send(t.Context(), "overflow", "hi")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Content != "recovered" {
		t.Fatalf("content = %q, want recovered", result.Content)
	}
	if got := len(provider.Calls()); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
}

func TestAgentBuilderCompactionRunsBeforeSend(t *testing.T) {
	provider := llm.NewFauxProvider(
		"faux",
		llm.FauxStep{Content: "summary"},
		llm.FauxStep{Content: "answer"},
	)
	app, err := NewAgent("compact").
		Model("faux").
		Provider(provider).
		Ephemeral().
		Compaction(governor.CompactOptions{
			MaxTokens:    20,
			ThresholdPct: 0.10,
			MinKeepTurns: 1,
			OffloadDir:   t.TempDir(),
		}).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer app.Close()

	sess := session.New("compact-session").WithWriter(app.Store)
	if err := sess.AppendUser(t.Context(), "old user message with enough text to compact"); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if err := sess.Append(
		t.Context(),
		session.NewMessage(
			sess.ID(),
			session.AssistantMessage("old assistant message with enough text to compact"),
		),
	); err != nil {
		t.Fatalf("append assistant: %v", err)
	}

	result, err := app.Send(t.Context(), sess.ID(), "new request")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Content != "answer" {
		t.Fatalf("content = %q, want answer", result.Content)
	}

	loaded, err := app.Store.Load(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if countCompactions(loaded) == 0 {
		t.Fatal("expected proactive compaction event")
	}
}

func TestAgentBuilderCompactionValidatesOptions(t *testing.T) {
	_, err := NewAgent("bad-compact").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Ephemeral().
		Compaction(governor.CompactOptions{MaxTokens: 100}).
		Build()
	if err == nil {
		t.Fatal("expected compaction validation error")
	}
}

func countCompactions(sess *session.Session) int {
	count := 0
	for event := range sess.All() {
		if event.Type == session.CompactionTriggered {
			count++
		}
	}
	return count
}
