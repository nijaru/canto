package canto

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

type burstEventAgent struct {
	count int
}

func (a burstEventAgent) ID() string {
	return "burst"
}

func (a burstEventAgent) Step(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a burstEventAgent) Turn(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	if err := sess.Append(ctx, session.NewTurnStartedEvent(sess.ID(), session.TurnStartedData{
		AgentID: a.ID(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	for i := range a.count {
		if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.Handoff, map[string]int{
			"index": i,
		})); err != nil {
			return agent.StepResult{}, err
		}
	}
	if err := sess.Append(ctx, session.NewTurnCompletedEvent(sess.ID(), session.TurnCompletedData{
		AgentID: a.ID(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: "done"}, nil
}

func TestPromptStreamDeliversBurstSessionEventsWithoutDrops(t *testing.T) {
	const burstEvents = 96

	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	harness := &Harness{
		Agent:     burstEventAgent{count: burstEvents},
		Runner:    runtime.NewRunner(store, burstEventAgent{count: burstEvents}),
		Store:     store,
		ownsStore: true,
	}
	defer harness.Close()

	events, err := harness.Session("burst-session").PromptStream(t.Context(), "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	seen := make(map[int]struct{}, burstEvents)
	var gotResult bool
	for event := range events {
		switch event.Type {
		case RunEventSession:
			if event.Event.Type != session.Handoff {
				continue
			}
			var data struct {
				Index int `json:"index"`
			}
			if err := event.Event.UnmarshalData(&data); err != nil {
				t.Fatalf("decode burst event: %v", err)
			}
			seen[data.Index] = struct{}{}
		case RunEventResult:
			gotResult = true
		case RunEventError:
			t.Fatalf("stream error: %v", event.Err)
		}
	}

	if len(seen) != burstEvents {
		t.Fatalf("burst session events = %d, want %d", len(seen), burstEvents)
	}
	if !gotResult {
		t.Fatal("missing terminal result")
	}
}

type gatedBurstEventAgent struct {
	firstBurst int
	tail       int
	burstDone  chan struct{}
	continueCh chan struct{}
}

func (a gatedBurstEventAgent) ID() string {
	return "gated-burst"
}

func (a gatedBurstEventAgent) Step(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a gatedBurstEventAgent) Turn(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	if err := sess.Append(ctx, session.NewTurnStartedEvent(sess.ID(), session.TurnStartedData{
		AgentID: a.ID(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	for i := range a.firstBurst {
		if err := appendIndexedHandoff(ctx, sess, i); err != nil {
			return agent.StepResult{}, err
		}
	}
	close(a.burstDone)
	select {
	case <-a.continueCh:
	case <-ctx.Done():
		return agent.StepResult{}, ctx.Err()
	}
	for i := a.firstBurst; i < a.firstBurst+a.tail; i++ {
		if err := appendIndexedHandoff(ctx, sess, i); err != nil {
			return agent.StepResult{}, err
		}
	}
	if err := sess.Append(ctx, session.NewTurnCompletedEvent(sess.ID(), session.TurnCompletedData{
		AgentID: a.ID(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: "done"}, nil
}

func TestPromptStreamBackpressuresSlowConsumersAndPreservesEventOrder(t *testing.T) {
	const (
		firstBurst           = 96
		tail                 = 10
		expectedHandoffCount = firstBurst + tail
	)

	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	a := gatedBurstEventAgent{
		firstBurst: firstBurst,
		tail:       tail,
		burstDone:  make(chan struct{}),
		continueCh: make(chan struct{}),
	}
	harness := &Harness{
		Agent:     a,
		Runner:    runtime.NewRunner(store, a),
		Store:     store,
		ownsStore: true,
	}
	defer harness.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	events, err := harness.Session("ordered-session").PromptStream(ctx, "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	select {
	case <-a.burstDone:
		t.Fatal("agent completed first burst before stream was consumed")
	case <-time.After(25 * time.Millisecond):
	}

	var indexes []int
	readHandoff := func() {
		t.Helper()
		for {
			select {
			case event, ok := <-events:
				if !ok {
					t.Fatal("stream closed before expected handoff event")
				}
				switch event.Type {
				case RunEventSession:
					index, ok := handoffIndex(t, event.Event)
					if ok {
						indexes = append(indexes, index)
						return
					}
				case RunEventError:
					t.Fatalf("stream error: %v", event.Err)
				}
			case <-ctx.Done():
				t.Fatalf("timed out waiting for handoff event: %v", ctx.Err())
			}
		}
	}

	readHandoff()
	if indexes[0] != 0 {
		t.Fatalf("first handoff index = %d, want 0", indexes[0])
	}
	for len(indexes) < firstBurst {
		readHandoff()
	}
	select {
	case <-a.burstDone:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for burst: %v", ctx.Err())
	}
	close(a.continueCh)

	var gotResult bool
	for event := range events {
		switch event.Type {
		case RunEventSession:
			index, ok := handoffIndex(t, event.Event)
			if ok {
				indexes = append(indexes, index)
			}
		case RunEventResult:
			gotResult = true
		case RunEventError:
			t.Fatalf("stream error: %v", event.Err)
		}
	}

	if !gotResult {
		t.Fatal("missing terminal result")
	}
	if len(indexes) != expectedHandoffCount {
		t.Fatalf("handoff events = %d, want %d: %v", len(indexes), expectedHandoffCount, indexes)
	}
	for i, got := range indexes {
		if got != i {
			t.Fatalf(
				"handoff index at stream position %d = %d, want %d; order=%v",
				i,
				got,
				i,
				indexes,
			)
		}
	}
}

type immediateChunkAgent struct{}

func (a immediateChunkAgent) ID() string {
	return "immediate-chunk"
}

func (a immediateChunkAgent) Step(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a immediateChunkAgent) Turn(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.StreamTurn(ctx, sess, nil)
}

func (a immediateChunkAgent) StreamTurn(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if err := sess.Append(ctx, session.NewTurnStartedEvent(sess.ID(), session.TurnStartedData{
		AgentID: a.ID(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	if chunkFn != nil {
		chunkFn(&llm.Chunk{Content: "hello"})
	}
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleAssistant,
		Content: "hello",
	})); err != nil {
		return agent.StepResult{}, err
	}
	if err := sess.Append(ctx, session.NewTurnCompletedEvent(sess.ID(), session.TurnCompletedData{
		AgentID: a.ID(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: "hello"}, nil
}

func TestPromptStreamEmitsTurnStartedBeforeImmediateChunk(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	a := immediateChunkAgent{}
	harness := &Harness{
		Agent:     a,
		Runner:    runtime.NewRunner(store, a),
		Store:     store,
		ownsStore: true,
	}
	defer harness.Close()

	events, err := harness.Session("immediate-chunk-session").PromptStream(t.Context(), "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var sawTurnStarted bool
	for event := range events {
		switch event.Type {
		case RunEventSession:
			if event.Event.Type == session.TurnStarted {
				sawTurnStarted = true
			}
		case RunEventChunk:
			if !sawTurnStarted {
				t.Fatal("received model chunk before turn_started session event")
			}
		case RunEventError:
			t.Fatalf("stream error: %v", event.Err)
		}
	}
	if !sawTurnStarted {
		t.Fatal("missing turn_started session event")
	}
}

func TestPromptStreamEmitsUserMessageBeforeTurnAndChunks(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	a := immediateChunkAgent{}
	harness := &Harness{
		Agent:     a,
		Runner:    runtime.NewRunner(store, a),
		Store:     store,
		ownsStore: true,
	}
	defer harness.Close()

	events, err := harness.Session("committed-user-session").PromptStream(t.Context(), "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var (
		sawUser        bool
		sawTurnStarted bool
		sawChunk       bool
	)
	for event := range events {
		switch event.Type {
		case RunEventSession:
			switch event.Event.Type {
			case session.MessageAdded:
				var msg llm.Message
				if err := event.Event.UnmarshalData(&msg); err != nil {
					t.Fatalf("decode message event: %v", err)
				}
				if msg.Role == llm.RoleUser && msg.Content == "go" {
					sawUser = true
				}
			case session.TurnStarted:
				if !sawUser {
					t.Fatal("received turn_started before committed user message")
				}
				sawTurnStarted = true
			}
		case RunEventChunk:
			if !sawUser {
				t.Fatal("received model chunk before committed user message")
			}
			if !sawTurnStarted {
				t.Fatal("received model chunk before turn_started session event")
			}
			sawChunk = true
		case RunEventError:
			t.Fatalf("stream error: %v", event.Err)
		}
	}
	if !sawUser {
		t.Fatal("missing committed user message session event")
	}
	if !sawTurnStarted {
		t.Fatal("missing turn_started session event")
	}
	if !sawChunk {
		t.Fatal("missing model chunk")
	}
}

type toolLifecycleAgent struct{}

func (a toolLifecycleAgent) ID() string {
	return "tool-lifecycle"
}

func (a toolLifecycleAgent) Step(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a toolLifecycleAgent) Turn(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.StreamTurn(ctx, sess, nil)
}

func (a toolLifecycleAgent) StreamTurn(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if err := sess.Append(ctx, session.NewTurnStartedEvent(sess.ID(), session.TurnStartedData{
		AgentID: a.ID(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	call := llm.Call{ID: "tool-1", Type: "function"}
	call.Function.Name = "bash"
	call.Function.Arguments = `{"command":"printf ok"}`
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), llm.Message{
		Role:  llm.RoleAssistant,
		Calls: []llm.Call{call},
	})); err != nil {
		return agent.StepResult{}, err
	}
	if err := sess.Append(ctx, session.NewToolStartedEvent(sess.ID(), session.ToolStartedData{
		Tool:      "bash",
		Arguments: `{"command":"printf ok"}`,
		ID:        "tool-1",
	})); err != nil {
		return agent.StepResult{}, err
	}
	if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.ToolOutputDelta, map[string]any{
		"tool":  "bash",
		"id":    "tool-1",
		"delta": "ok",
	})); err != nil {
		return agent.StepResult{}, err
	}
	if err := sess.Append(ctx, session.NewToolCompletedEvent(sess.ID(), session.ToolCompletedData{
		Tool:   "bash",
		ID:     "tool-1",
		Output: "ok",
	})); err != nil {
		return agent.StepResult{}, err
	}
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleTool,
		Name:    "bash",
		ToolID:  "tool-1",
		Content: "ok",
	})); err != nil {
		return agent.StepResult{}, err
	}
	if err := sess.Append(ctx, session.NewTurnCompletedEvent(sess.ID(), session.TurnCompletedData{
		AgentID: a.ID(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: "ok"}, nil
}

func TestPromptStreamFlushesToolLifecycleBeforeResult(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	a := toolLifecycleAgent{}
	harness := &Harness{
		Agent:     a,
		Runner:    runtime.NewRunner(store, a),
		Store:     store,
		ownsStore: true,
	}
	defer harness.Close()

	events, err := harness.Session("tool-lifecycle-session").PromptStream(t.Context(), "run tool")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var order []string
	for event := range events {
		switch event.Type {
		case RunEventSession:
			switch event.Event.Type {
			case session.ToolStarted:
				order = append(order, "tool_started")
			case session.ToolOutputDelta:
				order = append(order, "tool_output_delta")
			case session.ToolCompleted:
				order = append(order, "tool_completed")
			case session.MessageAdded:
				var msg llm.Message
				if err := event.Event.UnmarshalData(&msg); err != nil {
					t.Fatalf("decode message event: %v", err)
				}
				if msg.Role == llm.RoleTool && msg.ToolID == "tool-1" {
					order = append(order, "tool_message")
				}
			}
		case RunEventResult:
			order = append(order, "result")
		case RunEventError:
			t.Fatalf("stream error: %v", event.Err)
		}
	}

	want := []string{
		"tool_started",
		"tool_output_delta",
		"tool_completed",
		"tool_message",
		"result",
	}
	if len(order) != len(want) {
		t.Fatalf("tool lifecycle order = %v, want %v", order, want)
	}
	for i, got := range order {
		if got != want[i] {
			t.Fatalf("tool lifecycle order = %v, want %v", order, want)
		}
	}
}

type terminalErrorAgent struct {
	err error
}

func (a terminalErrorAgent) ID() string {
	return "terminal-error"
}

func (a terminalErrorAgent) Step(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a terminalErrorAgent) Turn(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.StreamTurn(ctx, sess, nil)
}

func (a terminalErrorAgent) StreamTurn(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if err := sess.Append(ctx, session.NewTurnStartedEvent(sess.ID(), session.TurnStartedData{
		AgentID: a.ID(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	if err := sess.Append(context.WithoutCancel(ctx), session.NewTurnCompletedEvent(sess.ID(), session.TurnCompletedData{
		AgentID: a.ID(),
		Error:   a.err.Error(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{}, a.err
}

func TestPromptStreamFlushesTerminalSessionErrorBeforeRunError(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	a := terminalErrorAgent{err: errors.New("provider exploded")}
	harness := &Harness{
		Agent:     a,
		Runner:    runtime.NewRunner(store, a),
		Store:     store,
		ownsStore: true,
	}
	defer harness.Close()

	events, err := harness.Session("terminal-error-session").PromptStream(t.Context(), "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var order []string
	for event := range events {
		switch event.Type {
		case RunEventSession:
			data, ok, err := event.Event.TurnCompletedData()
			if err != nil {
				t.Fatalf("decode turn completed: %v", err)
			}
			if ok && data.Error == "provider exploded" {
				order = append(order, "turn_error")
			}
		case RunEventError:
			if event.Err == nil || event.Err.Error() != "provider exploded" {
				t.Fatalf("run error = %v, want provider exploded", event.Err)
			}
			order = append(order, "run_error")
		case RunEventResult:
			t.Fatal("unexpected result for terminal error turn")
		}
	}

	want := []string{"turn_error", "run_error"}
	if len(order) != len(want) {
		t.Fatalf("terminal error order = %v, want %v", order, want)
	}
	for i, got := range order {
		if got != want[i] {
			t.Fatalf("terminal error order = %v, want %v", order, want)
		}
	}
}

type cancelAfterTurnStartedAgent struct{}

func (a cancelAfterTurnStartedAgent) ID() string {
	return "cancel-after-turn-started"
}

func (a cancelAfterTurnStartedAgent) Step(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a cancelAfterTurnStartedAgent) Turn(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.StreamTurn(ctx, sess, nil)
}

func (a cancelAfterTurnStartedAgent) StreamTurn(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if err := sess.Append(ctx, session.NewTurnStartedEvent(sess.ID(), session.TurnStartedData{
		AgentID: a.ID(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	<-ctx.Done()
	if err := sess.Append(context.WithoutCancel(ctx), session.NewTurnCompletedEvent(sess.ID(), session.TurnCompletedData{
		AgentID: a.ID(),
		Error:   context.Canceled.Error(),
	})); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{}, ctx.Err()
}

func TestPromptStreamFlushesCanceledTurnBeforeRunError(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	a := cancelAfterTurnStartedAgent{}
	harness := &Harness{
		Agent:     a,
		Runner:    runtime.NewRunner(store, a),
		Store:     store,
		ownsStore: true,
	}
	defer harness.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, err := harness.Session("canceled-turn-session").PromptStream(ctx, "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var order []string
	for event := range events {
		switch event.Type {
		case RunEventSession:
			if event.Event.Type == session.TurnStarted {
				cancel()
				order = append(order, "turn_started")
				continue
			}
			data, ok, err := event.Event.TurnCompletedData()
			if err != nil {
				t.Fatalf("decode turn completed: %v", err)
			}
			if ok && data.Error == context.Canceled.Error() {
				order = append(order, "turn_canceled")
			}
		case RunEventError:
			if !errors.Is(event.Err, context.Canceled) {
				t.Fatalf("run error = %v, want context canceled", event.Err)
			}
			order = append(order, "run_error")
		case RunEventResult:
			t.Fatal("unexpected result for canceled turn")
		}
	}

	want := []string{"turn_started", "turn_canceled", "run_error"}
	if len(order) != len(want) {
		t.Fatalf("canceled turn order = %v, want %v", order, want)
	}
	for i, got := range order {
		if got != want[i] {
			t.Fatalf("canceled turn order = %v, want %v", order, want)
		}
	}
}

func TestPromptStreamAnnotatesOrderedRunEvents(t *testing.T) {
	h, err := NewHarness("metadata").
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

	const sessionID = "metadata-session"
	events, err := h.Session(sessionID).PromptStream(t.Context(), "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var (
		seq           int64
		turnID        string
		sawChunk      bool
		sawSession    bool
		sawTerminal   bool
		sawDurability bool
	)
	for event := range events {
		seq++
		if event.Seq != seq {
			t.Fatalf("event seq = %d, want %d for %#v", event.Seq, seq, event.Type)
		}
		if event.SessionID != sessionID {
			t.Fatalf("event session id = %q, want %q", event.SessionID, sessionID)
		}
		if event.TurnID == "" {
			t.Fatalf("event %d has empty turn id", event.Seq)
		}
		if turnID == "" {
			turnID = event.TurnID
		}
		if event.TurnID != turnID {
			t.Fatalf("event turn id = %q, want stable %q", event.TurnID, turnID)
		}

		switch event.Type {
		case RunEventChunk:
			sawChunk = true
			if event.Durability != RunEventLiveOnly {
				t.Fatalf("chunk durability = %q, want %q", event.Durability, RunEventLiveOnly)
			}
		case RunEventSession:
			sawSession = true
			if event.Durability != RunEventDurable {
				t.Fatalf("session durability = %q, want %q", event.Durability, RunEventDurable)
			}
		case RunEventResult:
			sawTerminal = true
			if event.Durability != RunEventTerminal {
				t.Fatalf("result durability = %q, want %q", event.Durability, RunEventTerminal)
			}
		case RunEventError:
			t.Fatalf("stream error: %v", event.Err)
		}
		if event.Durability != "" {
			sawDurability = true
		}
	}
	if !sawSession {
		t.Fatal("missing durable session event")
	}
	if !sawChunk {
		t.Fatal("missing live chunk event")
	}
	if !sawTerminal {
		t.Fatal("missing terminal result event")
	}
	if !sawDurability {
		t.Fatal("missing durability annotations")
	}
}

func TestPromptStreamFlushesTurnUsageBeforeResult(t *testing.T) {
	usage := llm.Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7, Cost: 0.25}
	h, err := NewHarness("usage").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{
			Content: "done",
			Usage:   usage,
		})).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	events, err := h.Session("usage-session").PromptStream(t.Context(), "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var order []string
	for event := range events {
		switch event.Type {
		case RunEventSession:
			data, ok, err := event.Event.TurnCompletedData()
			if err != nil {
				t.Fatalf("decode turn completed: %v", err)
			}
			if ok && data.Usage.TotalTokens == usage.TotalTokens {
				order = append(order, "turn_usage")
			}
		case RunEventResult:
			if event.Result.Usage.TotalTokens != usage.TotalTokens {
				t.Fatalf("result usage = %#v, want %#v", event.Result.Usage, usage)
			}
			order = append(order, "result")
		case RunEventError:
			t.Fatalf("stream error: %v", event.Err)
		}
	}

	want := []string{"turn_usage", "result"}
	if len(order) != len(want) {
		t.Fatalf("usage order = %v, want %v", order, want)
	}
	for i, got := range order {
		if got != want[i] {
			t.Fatalf("usage order = %v, want %v", order, want)
		}
	}
}

func TestPromptStreamWaitsForYieldingPostToolHookBeforeResult(t *testing.T) {
	call := llm.Call{ID: "tool-1", Type: "function"}
	call.Function.Name = "echo"
	call.Function.Arguments = `{"text":"ok"}`

	hookEntered := make(chan struct{})
	releaseHook := make(chan struct{})
	hooks := hook.NewRunner()
	hooks.Register(hook.FromFunc(
		"yield-post-tool",
		[]hook.Event{hook.EventPostToolUse},
		func(ctx context.Context, _ *hook.Payload) *hook.Result {
			close(hookEntered)
			select {
			case <-releaseHook:
				return &hook.Result{
					Action: hook.ActionProceed,
					Data:   map[string]any{"output": "hooked"},
				}
			case <-ctx.Done():
				return &hook.Result{Action: hook.ActionBlock, Error: ctx.Err()}
			}
		},
	))

	h, err := NewHarness("yield-hook").
		Model("faux").
		Provider(llm.NewFauxProvider(
			"faux",
			llm.FauxStep{Calls: []llm.Call{call}},
			llm.FauxStep{Content: "done"},
		)).
		Tools(tool.Func(
			"echo",
			"Echo input.",
			map[string]any{"type": "object"},
			func(_ context.Context, _ string) (string, error) {
				return "ok", nil
			},
		)).
		Hooks(hooks).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	events, err := h.Session("yield-hook-session").PromptStream(t.Context(), "run tool")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var (
		order        []string
		hookReleased bool
	)
	record := func(event RunEvent) {
		t.Helper()
		switch event.Type {
		case RunEventSession:
			switch event.Event.Type {
			case session.ToolStarted:
				order = append(order, "tool_started")
			case session.ToolCompleted:
				data, ok, err := event.Event.ToolCompletedData()
				if err != nil {
					t.Fatalf("decode tool completed: %v", err)
				}
				if ok {
					if !hookReleased {
						t.Fatalf(
							"tool completed before yielding post-tool hook released: %#v",
							data,
						)
					}
					if data.Output != "hooked" {
						t.Fatalf("tool completed output = %q, want hooked", data.Output)
					}
					order = append(order, "tool_completed")
				}
			}
		case RunEventResult:
			if !hookReleased {
				t.Fatal("terminal result arrived before yielding post-tool hook released")
			}
			order = append(order, "result")
		case RunEventError:
			t.Fatalf("stream error: %v", event.Err)
		}
	}

	for event := range events {
		record(event)
		if hookReleased || !isClosed(hookEntered) {
			continue
		}
		assertNoToolCompletionOrResultBeforeHookRelease(t, events, record)
		close(releaseHook)
		hookReleased = true
	}

	if !hookReleased {
		t.Fatal("post-tool hook never ran")
	}
	want := []string{"tool_started", "tool_completed", "result"}
	if len(order) != len(want) {
		t.Fatalf("yielding hook order = %v, want %v", order, want)
	}
	for i, got := range order {
		if got != want[i] {
			t.Fatalf("yielding hook order = %v, want %v", order, want)
		}
	}
}

func TestPromptStreamKeepsStableTurnIDAcrossOverflowRecovery(t *testing.T) {
	overflow := errors.New("context_length_exceeded")
	provider := llm.NewFauxProvider(
		"faux",
		llm.FauxStep{Err: overflow},
		llm.FauxStep{Content: "recovered"},
	)
	provider.IsContextOverflowFn = func(err error) bool {
		return errors.Is(err, overflow)
	}
	h, err := NewHarness("overflow-stream").
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

	events, err := h.Session("overflow-stream-session").PromptStream(t.Context(), "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var (
		turnID      string
		resultCount int
	)
	for event := range events {
		if event.TurnID == "" {
			t.Fatalf("event %d has empty turn id", event.Seq)
		}
		if turnID == "" {
			turnID = event.TurnID
		}
		if event.TurnID != turnID {
			t.Fatalf("event turn id = %q, want stable %q", event.TurnID, turnID)
		}
		switch event.Type {
		case RunEventResult:
			resultCount++
			if event.Result.Content != "recovered" {
				t.Fatalf("result content = %q, want recovered", event.Result.Content)
			}
		case RunEventError:
			t.Fatalf("stream error after overflow recovery: %v", event.Err)
		}
	}
	if resultCount != 1 {
		t.Fatalf("result events = %d, want 1", resultCount)
	}
}

func assertNoToolCompletionOrResultBeforeHookRelease(
	t *testing.T,
	events <-chan RunEvent,
	record func(RunEvent),
) {
	t.Helper()
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("stream closed before yielding post-tool hook released")
			}
			record(event)
		case <-timer.C:
			return
		}
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func appendIndexedHandoff(ctx context.Context, sess *session.Session, index int) error {
	return sess.Append(ctx, session.NewEvent(sess.ID(), session.Handoff, map[string]int{
		"index": index,
	}))
}

func handoffIndex(t *testing.T, event session.Event) (int, bool) {
	t.Helper()
	if event.Type != session.Handoff {
		return 0, false
	}
	var data struct {
		Index int `json:"index"`
	}
	if err := event.UnmarshalData(&data); err != nil {
		t.Fatalf("decode handoff event: %v", err)
	}
	return data.Index, true
}
