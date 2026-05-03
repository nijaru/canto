package canto

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/coding"
	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/workspace"
)

func TestHarnessSessionPrompt(t *testing.T) {
	h, err := NewHarness("hello").
		Instructions("You are concise.").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "hello"})).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	result, err := h.Session("sess").Prompt(t.Context(), "hi")
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if result.Content != "hello" {
		t.Fatalf("content = %q, want hello", result.Content)
	}
}

func TestHarnessSessionPromptStream(t *testing.T) {
	h, err := NewHarness("stream").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{
			Chunks: []llm.Chunk{{Content: "he"}, {Content: "llo"}},
		})).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	events, err := h.Session("stream-session").PromptStream(t.Context(), "hi")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var chunks, sessionEvents int
	var result agent.StepResult
	for event := range events {
		switch event.Type {
		case RunEventChunk:
			chunks++
		case RunEventSession:
			sessionEvents++
		case RunEventResult:
			result = event.Result
		case RunEventError:
			t.Fatalf("stream error: %v", event.Err)
		}
	}

	if chunks != 2 {
		t.Fatalf("chunks = %d, want 2", chunks)
	}
	if sessionEvents == 0 {
		t.Fatal("expected durable session events")
	}
	if result.Content != "hello" {
		t.Fatalf("result content = %q, want hello", result.Content)
	}
}

func TestHarnessBuilderRegistersTools(t *testing.T) {
	testTool := tool.Func(
		"echo",
		"Echo input.",
		map[string]any{"type": "object"},
		func(_ context.Context, args string) (string, error) {
			return args, nil
		},
	)
	h, err := NewHarness("tools").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Tools(testTool).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	if _, ok := h.Tools.Get("echo"); !ok {
		t.Fatal("expected echo tool to be registered")
	}
}

func TestHarnessBuilderStoresEnvironment(t *testing.T) {
	root, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open workspace: %v", err)
	}
	defer root.Close()

	executor := coding.NewExecutor(time.Second, 1024)
	secrets := safety.StaticSecretInjector{"TOKEN": "secret"}
	h, err := NewHarness("env").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Environment(Environment{
			Workspace: root,
			Executor:  executor,
			Secrets:   secrets,
			Bootstrap: []session.ContextEntry{{
				Kind:    session.ContextKindHarness,
				Content: "workspace ready",
			}},
		}).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	if h.Environment.Workspace != root {
		t.Fatal("workspace capability was not retained")
	}
	if h.Environment.Executor != executor {
		t.Fatal("executor capability was not retained")
	}
	if h.Environment.Secrets == nil {
		t.Fatal("secret injector was not retained")
	}
	if got := h.Environment.Bootstrap[0].Content; got != "workspace ready" {
		t.Fatalf("bootstrap content = %q", got)
	}
}

func TestHarnessBuilderRequiresModel(t *testing.T) {
	_, err := NewHarness("missing-model").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Build()
	if err == nil {
		t.Fatal("expected missing model error")
	}
	if err.Error() != "canto harness: model is required" {
		t.Fatalf("error = %q, want model required", err)
	}
}

func TestHarnessBuilderRequiresSessionStore(t *testing.T) {
	_, err := NewHarness("missing-store").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Build()
	if err == nil {
		t.Fatal("expected missing session store error")
	}
	if err.Error() != "canto harness: session store is required; call SessionStore or Ephemeral" {
		t.Fatalf("error = %q, want session store required", err)
	}
}

func TestHarnessCloseLeavesExternalStoreOpen(t *testing.T) {
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	h, err := NewHarness("external-store").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		SessionStore(store).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := h.Session("external-session").Prompt(t.Context(), "hi"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := store.Load(t.Context(), "external-session"); err != nil {
		t.Fatalf("external store was closed: %v", err)
	}
}

func TestHarnessBuilderCompactionRecoversOverflow(t *testing.T) {
	overflow := errors.New("context_length_exceeded")
	provider := llm.NewFauxProvider(
		"faux",
		llm.FauxStep{Err: overflow},
		llm.FauxStep{Content: "recovered"},
	)
	provider.IsContextOverflowFn = func(err error) bool {
		return errors.Is(err, overflow)
	}

	h, err := NewHarness("recover").
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
	defer h.Close()

	result, err := h.Session("overflow").Prompt(t.Context(), "hi")
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if result.Content != "recovered" {
		t.Fatalf("content = %q, want recovered", result.Content)
	}
	if got := len(provider.Calls()); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
}

func TestHarnessBuilderCompactionRunsBeforePrompt(t *testing.T) {
	provider := llm.NewFauxProvider(
		"faux",
		llm.FauxStep{Content: "summary"},
		llm.FauxStep{Content: "answer"},
	)
	h, err := NewHarness("compact").
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
	defer h.Close()

	sess := session.New("compact-session").WithWriter(h.Store)
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

	result, err := h.Session(sess.ID()).Prompt(t.Context(), "new request")
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if result.Content != "answer" {
		t.Fatalf("content = %q, want answer", result.Content)
	}

	loaded, err := h.Store.Load(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if countCompactions(loaded) == 0 {
		t.Fatal("expected proactive compaction event")
	}
}

func TestHarnessBuilderCompactionValidatesOptions(t *testing.T) {
	_, err := NewHarness("bad-compact").
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
