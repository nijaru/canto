package canto

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/executor"
	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/runtime"
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
		switch event.Kind() {
		case RunEventChunk:
			chunks++
		case RunEventSession:
			sessionEvents++
		case RunEventResult:
			var ok bool
			result, ok = event.Result()
			if !ok {
				t.Fatal("result event missing result payload")
			}
		case RunEventError:
			err, _ := event.Err()
			t.Fatalf("stream error: %v", err)
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

func TestHarnessSessionSubmitTurn(t *testing.T) {
	h, err := NewHarness("submit").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{
			Chunks: []llm.Chunk{{Content: "ok"}},
		})).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	turn, err := h.Session("submit-session").Submit(t.Context(), TextPrompt("hi"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if turn.ID() == "" {
		t.Fatal("turn ID is empty")
	}

	var sawResult bool
	for event := range turn.Events() {
		if event.TurnID != turn.ID() {
			t.Fatalf("event turn id = %q, want %q", event.TurnID, turn.ID())
		}
		if sessionEvent, ok := event.SessionEvent(); ok && sessionEvent.TurnID != turn.ID() {
			t.Fatalf("durable event turn id = %q, want %q", sessionEvent.TurnID, turn.ID())
		}
		if event.Kind() == RunEventResult {
			sawResult = true
		}
		if event.Kind() == RunEventError {
			err, _ := event.Err()
			t.Fatalf("stream error: %v", err)
		}
	}
	if !sawResult {
		t.Fatal("missing terminal result event")
	}

	result, err := turn.Result()
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("result content = %q, want ok", result.Content)
	}
}

func TestHarnessSessionSubmitTurnCancel(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	a := cancelAfterTurnStartedAgent{}
	h := &Harness{
		Agent:     a,
		Runner:    runtime.NewRunner(store, a),
		Store:     store,
		ownsStore: true,
	}
	defer h.Close()

	turn, err := h.Session("submit-cancel-session").Submit(t.Context(), TextPrompt("go"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	var sawCancel bool
	for event := range turn.Events() {
		switch event.Kind() {
		case RunEventSession:
			sessionEvent, ok := event.SessionEvent()
			if !ok {
				t.Fatal("session event missing session payload")
			}
			if sessionEvent.Type == session.TurnStarted {
				if err := turn.Cancel(t.Context()); err != nil {
					t.Fatalf("Cancel: %v", err)
				}
			}
		case RunEventError:
			err, _ := event.Err()
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("stream error = %v, want context canceled", err)
			}
			sawCancel = true
		case RunEventResult:
			t.Fatal("unexpected result for canceled turn")
		}
	}
	if !sawCancel {
		t.Fatal("missing canceled terminal event")
	}
	if _, err := turn.Result(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Result error = %v, want context canceled", err)
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

	exec := executor.NewExecutor(time.Second, 1024)
	secrets := safety.StaticSecretInjector{"TOKEN": "secret"}
	h, err := NewHarness("env").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Environment(Environment{
			Workspace: root,
			Executor:  exec,
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
	if h.Environment.Executor != exec {
		t.Fatal("executor capability was not retained")
	}
	if h.Environment.Secrets == nil {
		t.Fatal("secret injector was not retained")
	}
	if got := h.Environment.Bootstrap[0].Content; got != "workspace ready" {
		t.Fatalf("bootstrap content = %q", got)
	}
}

func TestHarnessBuilderRegistersWorkspaceToolsFromEnvironment(t *testing.T) {
	root, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open workspace: %v", err)
	}
	defer root.Close()
	if err := root.WriteFile("hello.txt", []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	h, err := NewHarness("env-tools").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Environment(Environment{Workspace: root}).
		ToolsFromEnvironment(EnvironmentToolConfig{Workspace: true}).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	for _, name := range []string{"read_file", "write_file", "list_dir", "edit"} {
		if _, ok := h.Tools.Get(name); !ok {
			t.Fatalf("missing environment workspace tool %q", name)
		}
	}
	out, err := h.Tools.Execute(t.Context(), "read_file", `{"path":"hello.txt"}`)
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if out != "hello" {
		t.Fatalf("read_file output = %q, want hello", out)
	}
}

func TestHarnessSessionMaintenanceFacade(t *testing.T) {
	h, err := NewHarness("maintenance").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	parent := session.New("maintenance-parent").WithWriter(h.Store)
	appendCompactionHistory(t, parent)

	facade := h.Session(parent.ID())
	replayed, err := facade.Replay(t.Context())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got := len(replayed.Events()); got != 4 {
		t.Fatalf("Replay events = %d, want 4", got)
	}

	events, err := facade.EventsAfter(t.Context(), 2)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 3 || events[1].Seq != 4 {
		t.Fatalf("EventsAfter = %#v, want seq 3 and 4", events)
	}

	ok, err := facade.SnapshotIfNeeded(t.Context(), SnapshotOptions{MaxEvents: 1})
	if err != nil {
		t.Fatalf("SnapshotIfNeeded: %v", err)
	}
	if !ok {
		t.Fatal("SnapshotIfNeeded did not append a projection snapshot")
	}
	ok, err = facade.Snapshot(t.Context(), SnapshotOptions{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !ok {
		t.Fatal("Snapshot did not append a projection snapshot")
	}

	replayed, err = facade.Replay(t.Context())
	if err != nil {
		t.Fatalf("Replay after snapshot: %v", err)
	}
	last, ok := replayed.LastEvent()
	if !ok || last.Type != session.ProjectionSnapshotted {
		t.Fatalf("last event = %#v, want projection snapshot", last)
	}

	child, err := facade.Fork(t.Context(), "maintenance-child", session.ForkOptions{
		BranchLabel: "review",
		ForkReason:  "compare",
	})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if child.ID() != "maintenance-child" {
		t.Fatalf("child id = %q, want maintenance-child", child.ID())
	}
	childReplay, err := child.Replay(t.Context())
	if err != nil {
		t.Fatalf("child Replay: %v", err)
	}
	childEvents := childReplay.Events()
	if len(childEvents) != len(replayed.Events()) {
		t.Fatalf("child events = %d, want %d", len(childEvents), len(replayed.Events()))
	}
	if _, ok, err := childEvents[0].ForkOrigin(); err != nil || !ok {
		t.Fatalf("child first event fork origin ok=%v err=%v", ok, err)
	}
}

func TestHarnessBuilderEnvironmentToolsRequireCapabilities(t *testing.T) {
	_, err := NewHarness("missing-env").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})).
		ToolsFromEnvironment(EnvironmentToolConfig{Workspace: true}).
		Ephemeral().
		Build()
	if err == nil || !strings.Contains(err.Error(), "workspace is required") {
		t.Fatalf("Build error = %v, want workspace requirement", err)
	}
}

func TestHarnessSessionCompactFacade(t *testing.T) {
	h, err := NewHarness("compact-facade").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "summary"})).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	sess := session.New("compact-facade-session").WithWriter(h.Store)
	appendCompactionHistory(t, sess)

	result, err := h.Session(sess.ID()).Compact(t.Context(), governor.CompactOptions{
		MaxTokens:    20,
		ThresholdPct: 0.10,
		MinKeepTurns: 2,
		OffloadDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !result.Compacted {
		t.Fatal("Compact result = false, want compaction")
	}

	loaded, err := h.Store.Load(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if countCompactions(loaded) == 0 {
		t.Fatal("expected durable compaction event")
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
			MinKeepTurns: 2,
			OffloadDir:   t.TempDir(),
		}).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	sess := session.New("compact-session").WithWriter(h.Store)
	appendCompactionHistory(t, sess)

	result, err := h.Session(sess.ID()).Prompt(t.Context(), "new request")
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if result.Content != "answer" {
		t.Fatalf("content = %q, want answer", result.Content)
	}

	calls := provider.Calls()
	if len(calls) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(calls))
	}
	if requestMessagesContain(calls[0], "new request") {
		t.Fatal("proactive compaction request unexpectedly included the new prompt")
	}
	if !requestMessagesContain(calls[1], "new request") {
		t.Fatal("agent request did not include the new prompt")
	}

	loaded, err := h.Store.Load(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if countCompactions(loaded) == 0 {
		t.Fatal("expected proactive compaction event")
	}
}

func TestHarnessBuilderCompactionFailureDoesNotAppendPrompt(t *testing.T) {
	compactErr := errors.New("compact failed")
	provider := llm.NewFauxProvider("faux", llm.FauxStep{Err: compactErr})
	h, err := NewHarness("compact-failure").
		Model("faux").
		Provider(provider).
		Ephemeral().
		Compaction(governor.CompactOptions{
			MaxTokens:    20,
			ThresholdPct: 0.10,
			MinKeepTurns: 2,
			OffloadDir:   t.TempDir(),
		}).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	sess := session.New("compact-failure-session").WithWriter(h.Store)
	appendCompactionHistory(t, sess)

	_, err = h.Session(sess.ID()).Prompt(t.Context(), "new request")
	if !errors.Is(err, compactErr) {
		t.Fatalf("Prompt error = %v, want %v", err, compactErr)
	}

	loaded, err := h.Store.Load(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, msg := range loaded.Messages() {
		if msg.Role == llm.RoleUser && msg.Content == "new request" {
			t.Fatal("failed proactive compaction appended the new prompt")
		}
	}
}

func appendCompactionHistory(t *testing.T, sess *session.Session) {
	t.Helper()
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "old user message one with enough text to compact"},
		session.AssistantMessage("old assistant message one with enough text to compact"),
		{Role: llm.RoleUser, Content: "old user message two with enough text to compact"},
		session.AssistantMessage("old assistant message two with enough text to compact"),
	} {
		if err := sess.Append(t.Context(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}
}

func requestMessagesContain(req *llm.Request, content string) bool {
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, content) {
			return true
		}
	}
	return false
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
