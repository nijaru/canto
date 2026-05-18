package canto

import (
	"context"
	"testing"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
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

func TestPromptStreamReplaysSessionEventsDroppedByLiveWatch(t *testing.T) {
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
	waitForDurableBurstEvents(t, store, "burst-session", burstEvents)

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

func TestPromptStreamPreservesEventOrderWhenLiveWatchDropsMiddle(t *testing.T) {
	const (
		firstBurst           = 96
		tail                 = 10
		bufferedLivePrefix   = 66
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
	select {
	case <-a.burstDone:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for burst: %v", ctx.Err())
	}
	for len(indexes) < bufferedLivePrefix {
		readHandoff()
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

func waitForDurableBurstEvents(
	t *testing.T,
	store *session.SQLiteStore,
	sessionID string,
	want int,
) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()

	for {
		sess, err := store.Load(t.Context(), sessionID)
		if err != nil {
			t.Fatalf("load session: %v", err)
		}
		var got int
		for _, event := range sess.Events() {
			if event.Type == session.Handoff {
				got++
			}
		}
		if got == want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("durable burst events = %d, want %d", got, want)
		case <-tick.C:
		}
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
