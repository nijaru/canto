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

func TestHarnessSessionRuntimeFacadeOwnsPhaseQueuesAndSettlement(t *testing.T) {
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

	sess := h.Session("runtime-facade-session")
	again := h.Session("runtime-facade-session")
	runtimeEvents, err := sess.RuntimeEvents(t.Context())
	if err != nil {
		t.Fatalf("RuntimeEvents: %v", err)
	}

	turn, err := sess.Submit(t.Context(), TextPrompt("go"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	turnStarted := make(chan struct{})
	turnEventsDone := make(chan struct{})
	go func() {
		defer close(turnEventsDone)
		closed := false
		for event := range turn.Events() {
			if sessionEvent, ok := event.SessionEvent(); ok &&
				sessionEvent.Type == session.TurnStarted && !closed {
				close(turnStarted)
				closed = true
			}
		}
	}()

	select {
	case <-turnStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn start")
	}
	if sess.Phase() != HarnessPhaseTurn || again.Phase() != HarnessPhaseTurn {
		t.Fatalf("phase = %q/%q, want turn", sess.Phase(), again.Phase())
	}
	if _, err := again.Submit(t.Context(), TextPrompt("overlap")); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("overlap Submit error = %v, want ErrSessionBusy", err)
	}
	if err := again.SteerText(t.Context(), "steer while active"); err != nil {
		t.Fatalf("SteerText: %v", err)
	}
	if err := again.FollowUpText(t.Context(), "follow up while active"); err != nil {
		t.Fatalf("FollowUpText: %v", err)
	}
	if err := again.Abort(t.Context()); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	select {
	case <-turnEventsDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn events to close")
	}
	if _, err := turn.Result(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Result error = %v, want context canceled", err)
	}
	if sess.Phase() != HarnessPhaseIdle || again.Phase() != HarnessPhaseIdle {
		t.Fatalf("phase = %q/%q, want idle", sess.Phase(), again.Phase())
	}
	if err := sess.WaitForIdle(t.Context()); err != nil {
		t.Fatalf("WaitForIdle: %v", err)
	}

	events := drainHarnessEvents(runtimeEvents)
	kinds := harnessEventKinds(events)
	want := []HarnessEventKind{
		HarnessEventQueueUpdated,
		HarnessEventQueueUpdated,
		HarnessEventQueueUpdated,
		HarnessEventSavePoint,
		HarnessEventSettled,
		HarnessEventAbort,
	}
	if len(kinds) != len(want) {
		t.Fatalf("runtime event kinds = %v, want %v", kinds, want)
	}
	for i, kind := range want {
		if kinds[i] != kind {
			t.Fatalf("runtime event kinds = %v, want %v", kinds, want)
		}
	}
	queue, ok := events[0].Payload.(QueueUpdatedPayload)
	if !ok || len(queue.Queue.Steer) != 1 {
		t.Fatalf("first runtime event = %#v, want one queued steer", events[0])
	}
	abort, ok := events[5].Payload.(AbortPayload)
	if !ok || len(abort.ClearedSteer) != 1 || len(abort.ClearedFollowUp) != 1 {
		t.Fatalf("abort event = %#v, want cleared steer and follow-up", events[5])
	}
	settled, ok := events[4].Payload.(SettledPayload)
	if !ok || settled.NextTurnCount != 0 {
		t.Fatalf("settled event = %#v, want no queued next turn", events[4])
	}
}

func TestHarnessSessionNextTurnPrependsQueuedPrompt(t *testing.T) {
	provider := llm.NewFauxProvider("faux", llm.FauxStep{Content: "done"})
	h, err := NewHarness("next-turn").
		Model("faux").
		Provider(provider).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	sess := h.Session("next-turn-session")
	runtimeEvents, err := sess.RuntimeEvents(t.Context())
	if err != nil {
		t.Fatalf("RuntimeEvents: %v", err)
	}
	if err := sess.NextTurnText(t.Context(), "queued first"); err != nil {
		t.Fatalf("NextTurnText: %v", err)
	}
	if _, err := sess.Prompt(t.Context(), "current"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	calls := provider.Calls()
	if len(calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(calls))
	}
	messages := calls[0].Messages
	if len(messages) < 2 ||
		messages[len(messages)-2].TextContent() != "queued first" ||
		messages[len(messages)-1].TextContent() != "current" {
		t.Fatalf("provider messages = %#v, want queued prompt before current prompt", messages)
	}

	kinds := harnessEventKinds(drainHarnessEvents(runtimeEvents))
	wantPrefix := []HarnessEventKind{
		HarnessEventQueueUpdated,
		HarnessEventQueueUpdated,
		HarnessEventSavePoint,
		HarnessEventSettled,
	}
	if len(kinds) != len(wantPrefix) {
		t.Fatalf("runtime event kinds = %v, want %v", kinds, wantPrefix)
	}
	for i, want := range wantPrefix {
		if kinds[i] != want {
			t.Fatalf("runtime event kinds = %v, want %v", kinds, wantPrefix)
		}
	}
}

func TestHarnessSessionDrainsSteeringAndFollowUpAtAgentBoundaries(t *testing.T) {
	call := llm.Call{ID: "tool-1", Type: "function"}
	call.Function.Name = "wait"
	call.Function.Arguments = `{}`
	provider := llm.NewFauxProvider(
		"faux",
		llm.FauxStep{Calls: []llm.Call{call}},
		llm.FauxStep{Content: "after first steering"},
		llm.FauxStep{Content: "after second steering"},
		llm.FauxStep{Content: "after first follow-up"},
		llm.FauxStep{Content: "after second follow-up"},
	)
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	waitTool := tool.Func(
		"wait", "waits until released", nil,
		func(ctx context.Context, _ string) (string, error) {
			close(toolStarted)
			select {
			case <-releaseTool:
				return "tool done", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	)
	h, err := NewHarness("queued-input").
		Model("faux").
		Provider(provider).
		Tools(waitTool).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	sess := h.Session("queued-input-session")
	turn, err := sess.Submit(t.Context(), TextPrompt("start"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	turnEventsDone := make(chan struct{})
	go func() {
		defer close(turnEventsDone)
		for range turn.Events() {
		}
	}()

	select {
	case <-toolStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool start")
	}
	if err := sess.SteerText(t.Context(), "steer before next provider call"); err != nil {
		t.Fatalf("SteerText: %v", err)
	}
	if err := sess.SteerText(t.Context(), "second steer before follow-up"); err != nil {
		t.Fatalf("second SteerText: %v", err)
	}
	if err := sess.FollowUpText(t.Context(), "follow up after completion"); err != nil {
		t.Fatalf("FollowUpText: %v", err)
	}
	if err := sess.FollowUpText(t.Context(), "second follow up after completion"); err != nil {
		t.Fatalf("second FollowUpText: %v", err)
	}
	close(releaseTool)

	select {
	case <-turnEventsDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn events to close")
	}
	result, err := turn.Result()
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if result.Content != "after second follow-up" {
		t.Fatalf("result content = %q, want final follow-up response", result.Content)
	}

	calls := provider.Calls()
	if len(calls) != 5 {
		t.Fatalf("provider calls = %d, want 5", len(calls))
	}
	if !requestHasText(calls[1], "steer before next provider call") {
		t.Fatalf("second provider request missing steering: %#v", calls[1].Messages)
	}
	if requestHasText(calls[1], "follow up after completion") {
		t.Fatalf("second provider request consumed follow-up too early: %#v", calls[1].Messages)
	}
	if requestHasText(calls[1], "second steer before follow-up") {
		t.Fatalf(
			"second provider request consumed multiple steering prompts: %#v",
			calls[1].Messages,
		)
	}
	if !requestHasText(calls[2], "second steer before follow-up") {
		t.Fatalf("third provider request missing second steering: %#v", calls[2].Messages)
	}
	if requestHasText(calls[2], "follow up after completion") {
		t.Fatalf(
			"third provider request consumed follow-up before steering queue drained: %#v",
			calls[2].Messages,
		)
	}
	if !requestHasText(calls[3], "follow up after completion") {
		t.Fatalf("fourth provider request missing first follow-up: %#v", calls[3].Messages)
	}
	if requestHasText(calls[3], "second follow up after completion") {
		t.Fatalf("fourth provider request consumed multiple follow-ups: %#v", calls[3].Messages)
	}
	if !requestHasText(calls[4], "second follow up after completion") {
		t.Fatalf("fifth provider request missing second follow-up: %#v", calls[4].Messages)
	}
}

func TestHarnessSessionQueueAllModeDrainsBatches(t *testing.T) {
	call := llm.Call{ID: "tool-1", Type: "function"}
	call.Function.Name = "wait"
	call.Function.Arguments = `{}`
	provider := llm.NewFauxProvider(
		"faux",
		llm.FauxStep{Calls: []llm.Call{call}},
		llm.FauxStep{Content: "after steering batch"},
		llm.FauxStep{Content: "after follow-up batch"},
	)
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	waitTool := tool.Func(
		"wait", "waits until released", nil,
		func(ctx context.Context, _ string) (string, error) {
			close(toolStarted)
			select {
			case <-releaseTool:
				return "tool done", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	)
	h, err := NewHarness("queue-all").
		Model("faux").
		Provider(provider).
		Tools(waitTool).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	sess := h.Session("queue-all-session")
	if got := sess.SteeringMode(); got != QueueOneAtATime {
		t.Fatalf("default steering mode = %q, want %q", got, QueueOneAtATime)
	}
	if err := sess.SetSteeringMode(QueueAll); err != nil {
		t.Fatalf("SetSteeringMode: %v", err)
	}
	if err := sess.SetFollowUpMode(QueueAll); err != nil {
		t.Fatalf("SetFollowUpMode: %v", err)
	}
	if got := h.Session("queue-all-session").FollowUpMode(); got != QueueAll {
		t.Fatalf("shared follow-up mode = %q, want %q", got, QueueAll)
	}

	turn, err := sess.Submit(t.Context(), TextPrompt("start"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	turnEventsDone := make(chan struct{})
	go func() {
		defer close(turnEventsDone)
		for range turn.Events() {
		}
	}()

	select {
	case <-toolStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool start")
	}
	for _, text := range []string{"steer one", "steer two"} {
		if err := sess.SteerText(t.Context(), text); err != nil {
			t.Fatalf("SteerText(%q): %v", text, err)
		}
	}
	for _, text := range []string{"follow one", "follow two"} {
		if err := sess.FollowUpText(t.Context(), text); err != nil {
			t.Fatalf("FollowUpText(%q): %v", text, err)
		}
	}
	close(releaseTool)

	select {
	case <-turnEventsDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn events to close")
	}
	result, err := turn.Result()
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if result.Content != "after follow-up batch" {
		t.Fatalf("result content = %q, want final follow-up batch response", result.Content)
	}

	calls := provider.Calls()
	if len(calls) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(calls))
	}
	if !requestHasText(calls[1], "steer one") || !requestHasText(calls[1], "steer two") {
		t.Fatalf("second provider request missing steering batch: %#v", calls[1].Messages)
	}
	if requestHasText(calls[1], "follow one") || requestHasText(calls[1], "follow two") {
		t.Fatalf(
			"second provider request consumed follow-up batch too early: %#v",
			calls[1].Messages,
		)
	}
	if !requestHasText(calls[2], "follow one") || !requestHasText(calls[2], "follow two") {
		t.Fatalf("third provider request missing follow-up batch: %#v", calls[2].Messages)
	}
}

func TestHarnessSessionClearQueuedInput(t *testing.T) {
	call := llm.Call{ID: "tool-1", Type: "function"}
	call.Function.Name = "wait"
	call.Function.Arguments = `{}`
	provider := llm.NewFauxProvider(
		"faux",
		llm.FauxStep{Calls: []llm.Call{call}},
		llm.FauxStep{Content: "after clear"},
		llm.FauxStep{Content: "next turn"},
	)
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	waitTool := tool.Func(
		"wait", "waits until released", nil,
		func(ctx context.Context, _ string) (string, error) {
			close(toolStarted)
			select {
			case <-releaseTool:
				return "tool done", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	)
	h, err := NewHarness("clear-queued-input").
		Model("faux").
		Provider(provider).
		Tools(waitTool).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	sess := h.Session("clear-queued-input-session")
	runtimeEvents, err := sess.RuntimeEvents(t.Context())
	if err != nil {
		t.Fatalf("RuntimeEvents: %v", err)
	}
	turn, err := sess.Submit(t.Context(), TextPrompt("start"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	turnEventsDone := make(chan struct{})
	go func() {
		defer close(turnEventsDone)
		for range turn.Events() {
		}
	}()

	select {
	case <-toolStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool start")
	}
	if err := sess.SteerText(t.Context(), "queued steer"); err != nil {
		t.Fatalf("SteerText: %v", err)
	}
	if err := sess.FollowUpText(t.Context(), "queued follow-up"); err != nil {
		t.Fatalf("FollowUpText: %v", err)
	}
	if err := sess.NextTurnText(t.Context(), "queued next-turn"); err != nil {
		t.Fatalf("NextTurnText: %v", err)
	}

	snapshot := sess.QueuedInput()
	if len(snapshot.Steer) != 1 || len(snapshot.FollowUp) != 1 || len(snapshot.NextTurn) != 1 {
		t.Fatalf("QueuedInput = %#v, want one item in each queue", snapshot)
	}
	cleared, err := sess.ClearQueuedInput(t.Context())
	if err != nil {
		t.Fatalf("ClearQueuedInput: %v", err)
	}
	if len(cleared.Steer) != 1 || len(cleared.FollowUp) != 1 || len(cleared.NextTurn) != 1 {
		t.Fatalf("cleared queues = %#v, want one item in each queue", cleared)
	}
	if after := sess.QueuedInput(); !queueSnapshotEmpty(after) {
		t.Fatalf("QueuedInput after clear = %#v, want empty", after)
	}
	close(releaseTool)

	select {
	case <-turnEventsDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn events to close")
	}
	if _, err := turn.Result(); err != nil {
		t.Fatalf("Result: %v", err)
	}
	if _, err := sess.Prompt(t.Context(), "plain next"); err != nil {
		t.Fatalf("Prompt after clear: %v", err)
	}

	calls := provider.Calls()
	if len(calls) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(calls))
	}
	for i, call := range calls[1:] {
		for _, text := range []string{"queued steer", "queued follow-up", "queued next-turn"} {
			if requestHasText(call, text) {
				t.Fatalf(
					"provider call %d leaked cleared queue text %q: %#v",
					i+2,
					text,
					call.Messages,
				)
			}
		}
	}

	events := drainHarnessEvents(runtimeEvents)
	var sawEmptyQueueUpdate bool
	for _, event := range events {
		queue, ok := event.Payload.(QueueUpdatedPayload)
		if ok && queueSnapshotEmpty(queue.Queue) {
			sawEmptyQueueUpdate = true
			break
		}
	}
	if !sawEmptyQueueUpdate {
		t.Fatalf("runtime events = %#v, want empty queue_update after clear", events)
	}
}

func requestHasText(req *llm.Request, text string) bool {
	if req == nil {
		return false
	}
	for _, msg := range req.Messages {
		if msg.TextContent() == text {
			return true
		}
	}
	return false
}

func drainHarnessEvents(events <-chan HarnessEvent) []HarnessEvent {
	out := make([]HarnessEvent, 0)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, event)
		default:
			return out
		}
	}
}

func harnessEventKinds(events []HarnessEvent) []HarnessEventKind {
	kinds := make([]HarnessEventKind, len(events))
	for i, event := range events {
		kinds[i] = event.Kind()
	}
	return kinds
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
