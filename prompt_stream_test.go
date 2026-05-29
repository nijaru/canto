package canto

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/ion/llm"
	"github.com/nijaru/ion/session"
	"github.com/nijaru/ion/tool"
)

type burstEventAgent struct {
	count int
}

var errPromptStreamTransient = errors.New("transient provider failure")

func runEventSession(t *testing.T, event RunEvent) session.Event {
	t.Helper()
	sessionEvent, ok := event.SessionEvent()
	if !ok {
		t.Fatalf("event %s missing session payload", event.Kind())
	}
	return sessionEvent
}

func runEventResult(t *testing.T, event RunEvent) agent.StepResult {
	t.Helper()
	result, ok := event.Result()
	if !ok {
		t.Fatalf("event %s missing result payload", event.Kind())
	}
	return result
}

func runEventErr(t *testing.T, event RunEvent) error {
	t.Helper()
	err, ok := event.Err()
	if !ok {
		t.Fatalf("event %s missing error payload", event.Kind())
	}
	return err
}

type promptStreamRetryProvider struct {
	attempts int
}

func (p *promptStreamRetryProvider) ID() string {
	return "retry-faux"
}

func (p *promptStreamRetryProvider) Capabilities(string) llm.Capabilities {
	return llm.DefaultCapabilities()
}

func (p *promptStreamRetryProvider) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: "done"}, nil
}

func (p *promptStreamRetryProvider) Stream(context.Context, *llm.Request) (llm.Stream, error) {
	p.attempts++
	if p.attempts == 1 {
		return nil, errPromptStreamTransient
	}
	return llm.NewFauxStream(llm.Chunk{Content: "done"}), nil
}

func (p *promptStreamRetryProvider) Models(context.Context) ([]llm.Model, error) {
	return nil, nil
}

func (p *promptStreamRetryProvider) CountTokens(
	context.Context,
	string,
	[]llm.Message,
) (int, error) {
	return 0, nil
}

func (p *promptStreamRetryProvider) Cost(context.Context, string, llm.Usage) float64 {
	return 0
}

func (p *promptStreamRetryProvider) IsTransient(err error) bool {
	return errors.Is(err, errPromptStreamTransient)
}

func (p *promptStreamRetryProvider) IsContextOverflow(error) bool {
	return false
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
		switch event.Kind() {
		case RunEventSession:
			if runEventSession(t, event).Type != session.Handoff {
				continue
			}
			var data struct {
				Index int `json:"index"`
			}
			if err := runEventSession(t, event).UnmarshalData(&data); err != nil {
				t.Fatalf("decode burst event: %v", err)
			}
			seen[data.Index] = struct{}{}
		case RunEventResult:
			gotResult = true
		case RunEventError:
			t.Fatalf("stream error: %v", runEventErr(t, event))
		}
	}

	if len(seen) != burstEvents {
		t.Fatalf("burst session events = %d, want %d", len(seen), burstEvents)
	}
	if !gotResult {
		t.Fatal("missing terminal result")
	}
}

func TestPromptStreamEmitsProviderRetryLifecycle(t *testing.T) {
	provider := llm.NewRetryProvider(&promptStreamRetryProvider{})
	provider.Config.MinInterval = time.Nanosecond
	provider.Config.MaxInterval = time.Nanosecond

	h, err := NewHarness("provider-retry").
		Model("retry-faux").
		Provider(provider).
		Ephemeral().
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer h.Close()

	events, err := h.Session("provider-retry-session").PromptStream(t.Context(), "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var sawRetry bool
	var sawChunk bool
	for event := range events {
		switch event.Kind() {
		case RunEventRetry:
			sawRetry = true
			if event.Durability != RunEventLiveOnly {
				t.Fatalf("retry durability = %q, want %q", event.Durability, RunEventLiveOnly)
			}
			if event.Lifecycle == nil || event.Lifecycle.Type != RunLifecycleRetry {
				t.Fatalf("retry lifecycle = %#v", event.Lifecycle)
			}
			if event.Lifecycle.Retry == nil {
				t.Fatal("retry lifecycle missing retry metadata")
			}
			retry := event.Lifecycle.Retry
			if retry.Scope != "provider" || retry.Target != "provider" {
				t.Fatalf(
					"retry scope/target = %q/%q, want provider/provider",
					retry.Scope,
					retry.Target,
				)
			}
			if retry.Attempt != 1 {
				t.Fatalf("retry attempt = %d, want 1", retry.Attempt)
			}
			if retry.Error != errPromptStreamTransient.Error() {
				t.Fatalf("retry error = %q, want %q", retry.Error, errPromptStreamTransient.Error())
			}
		case RunEventChunk:
			if !sawRetry {
				t.Fatal("content chunk arrived before provider retry lifecycle")
			}
			sawChunk = true
		case RunEventError:
			t.Fatalf("stream error after retry: %v", runEventErr(t, event))
		}
	}
	if !sawRetry {
		t.Fatal("missing provider retry lifecycle event")
	}
	if !sawChunk {
		t.Fatal("missing chunk after retry")
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
				switch event.Kind() {
				case RunEventSession:
					index, ok := handoffIndex(t, runEventSession(t, event))
					if ok {
						indexes = append(indexes, index)
						return
					}
				case RunEventError:
					t.Fatalf("stream error: %v", runEventErr(t, event))
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
		switch event.Kind() {
		case RunEventSession:
			index, ok := handoffIndex(t, runEventSession(t, event))
			if ok {
				indexes = append(indexes, index)
			}
		case RunEventResult:
			gotResult = true
		case RunEventError:
			t.Fatalf("stream error: %v", runEventErr(t, event))
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
		switch event.Kind() {
		case RunEventSession:
			if runEventSession(t, event).Type == session.TurnStarted {
				sawTurnStarted = true
			}
		case RunEventChunk:
			if !sawTurnStarted {
				t.Fatal("received model chunk before turn_started session event")
			}
		case RunEventError:
			t.Fatalf("stream error: %v", runEventErr(t, event))
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
		switch event.Kind() {
		case RunEventSession:
			switch runEventSession(t, event).Type {
			case session.MessageAdded:
				var msg llm.Message
				if err := runEventSession(t, event).UnmarshalData(&msg); err != nil {
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
			t.Fatalf("stream error: %v", runEventErr(t, event))
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
		switch event.Kind() {
		case RunEventSession:
			switch runEventSession(t, event).Type {
			case session.ToolStarted:
				order = append(order, "tool_started")
			case session.ToolOutputDelta:
				order = append(order, "tool_output_delta")
			case session.ToolCompleted:
				order = append(order, "tool_completed")
			case session.MessageAdded:
				var msg llm.Message
				if err := runEventSession(t, event).UnmarshalData(&msg); err != nil {
					t.Fatalf("decode message event: %v", err)
				}
				if msg.Role == llm.RoleTool && msg.ToolID == "tool-1" {
					order = append(order, "tool_message")
				}
			}
		case RunEventResult:
			order = append(order, "result")
		case RunEventError:
			t.Fatalf("stream error: %v", runEventErr(t, event))
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
		switch event.Kind() {
		case RunEventSession:
			data, ok, err := runEventSession(t, event).TurnCompletedData()
			if err != nil {
				t.Fatalf("decode turn completed: %v", err)
			}
			if ok && data.Error == "provider exploded" {
				order = append(order, "turn_error")
			}
		case RunEventError:
			if runEventErr(t, event) == nil ||
				runEventErr(t, event).Error() != "provider exploded" {
				t.Fatalf("run error = %v, want provider exploded", runEventErr(t, event))
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
		switch event.Kind() {
		case RunEventSession:
			if runEventSession(t, event).Type == session.TurnStarted {
				cancel()
				order = append(order, "turn_started")
				continue
			}
			data, ok, err := runEventSession(t, event).TurnCompletedData()
			if err != nil {
				t.Fatalf("decode turn completed: %v", err)
			}
			if ok && data.Error == context.Canceled.Error() {
				order = append(order, "turn_canceled")
			}
		case RunEventError:
			if !errors.Is(runEventErr(t, event), context.Canceled) {
				t.Fatalf("run error = %v, want context canceled", runEventErr(t, event))
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
		durableSeq    int64
		turnID        string
		sawChunk      bool
		sawSession    bool
		sawTerminal   bool
		sawDurability bool
	)
	for event := range events {
		seq++
		if event.Seq != seq {
			t.Fatalf("event seq = %d, want %d for %#v", event.Seq, seq, event.Kind())
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

		switch event.Kind() {
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
			durableSeq++
			if runEventSession(t, event).Seq != durableSeq {
				t.Fatalf(
					"durable event seq = %d, want %d",
					runEventSession(t, event).Seq,
					durableSeq,
				)
			}
			if runEventSession(t, event).TurnID != event.TurnID {
				t.Fatalf(
					"durable event turn id = %q, want stream turn id %q",
					runEventSession(t, event).TurnID,
					event.TurnID,
				)
			}
		case RunEventResult:
			sawTerminal = true
			if event.Durability != RunEventTerminal {
				t.Fatalf("result durability = %q, want %q", event.Durability, RunEventTerminal)
			}
		case RunEventError:
			t.Fatalf("stream error: %v", runEventErr(t, event))
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

type lifecycleMetadataAgent struct{}

func (a lifecycleMetadataAgent) ID() string {
	return "lifecycle-metadata"
}

func (a lifecycleMetadataAgent) Step(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a lifecycleMetadataAgent) Turn(
	ctx context.Context,
	sess *session.Session,
) (agent.StepResult, error) {
	return a.StreamTurn(ctx, sess, nil)
}

func (a lifecycleMetadataAgent) StreamTurn(
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
		chunkFn(&llm.Chunk{Usage: &llm.Usage{
			InputTokens: 10,
			TotalTokens: 10,
			Cost:        0.01,
		}})
		chunkFn(&llm.Chunk{Usage: &llm.Usage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
			Cost:         0.015,
		}})
	}
	if err := sess.Append(ctx, session.NewToolStartedEvent(sess.ID(), session.ToolStartedData{
		Tool:           "read",
		Arguments:      `{"file_path":"AGENTS.md"}`,
		ID:             "tool-1",
		IdempotencyKey: "turn-1:tool-1",
	})); err != nil {
		return agent.StepResult{}, err
	}
	if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.ToolOutputDelta, map[string]any{
		"tool":  "read",
		"id":    "tool-1",
		"delta": "ok",
	})); err != nil {
		return agent.StepResult{}, err
	}
	if err := sess.Append(ctx, session.NewToolCompletedEvent(sess.ID(), session.ToolCompletedData{
		Tool:           "read",
		ID:             "tool-1",
		IdempotencyKey: "turn-1:tool-1",
		Output:         "ok",
	})); err != nil {
		return agent.StepResult{}, err
	}
	if chunkFn != nil {
		chunkFn(&llm.Chunk{Usage: &llm.Usage{
			InputTokens:  2,
			OutputTokens: 1,
			TotalTokens:  3,
			Cost:         0.003,
		}})
	}
	usage := llm.Usage{
		InputTokens:  12,
		OutputTokens: 6,
		TotalTokens:  18,
		Cost:         0.018,
	}
	if err := sess.Append(ctx, session.NewTurnCompletedEvent(sess.ID(), session.TurnCompletedData{
		AgentID:        a.ID(),
		Steps:          2,
		Usage:          usage,
		TurnStopReason: string(agent.TurnStopCompleted),
	})); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{
		Content:        "done",
		Usage:          usage,
		TurnStopReason: agent.TurnStopCompleted,
	}, nil
}

func TestPromptStreamAnnotatesLifecycleMetadata(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	a := lifecycleMetadataAgent{}
	harness := &Harness{
		Agent:     a,
		Runner:    runtime.NewRunner(store, a),
		Store:     store,
		ownsStore: true,
	}
	defer harness.Close()

	events, err := harness.Session("lifecycle-session").PromptStream(t.Context(), "go")
	if err != nil {
		t.Fatalf("PromptStream: %v", err)
	}

	var (
		usageDeltas       []llm.Usage
		toolStarted       bool
		toolOutputDelta   bool
		toolCompleted     bool
		turnTerminalUsage *RunUsage
		runTerminal       bool
	)
	for event := range events {
		if event.Usage != nil && event.Usage.Kind == RunUsageProviderDelta {
			usageDeltas = append(usageDeltas, event.Usage.Delta)
		}
		if event.Lifecycle == nil {
			continue
		}
		switch event.Lifecycle.Type {
		case RunLifecycleTool:
			if event.Lifecycle.Tool == nil {
				t.Fatalf("tool lifecycle missing tool payload: %#v", event.Lifecycle)
			}
			switch event.Lifecycle.Status {
			case RunLifecycleStarted:
				toolStarted = true
				if len(event.Lifecycle.ActiveTools) != 1 ||
					event.Lifecycle.ActiveTools[0].ID != "tool-1" {
					t.Fatalf("active tools after start = %#v", event.Lifecycle.ActiveTools)
				}
			case RunLifecycleUpdated:
				toolOutputDelta = true
				if event.Lifecycle.Tool.Delta != "ok" {
					t.Fatalf("tool delta = %q, want ok", event.Lifecycle.Tool.Delta)
				}
			case RunLifecycleCompleted:
				toolCompleted = true
				if len(event.Lifecycle.ActiveTools) != 0 {
					t.Fatalf("active tools after completion = %#v", event.Lifecycle.ActiveTools)
				}
				if event.Lifecycle.Tool.Output != "ok" {
					t.Fatalf("tool output = %q, want ok", event.Lifecycle.Tool.Output)
				}
			}
		case RunLifecycleTurn:
			if event.Lifecycle.Terminal {
				turnTerminalUsage = event.Lifecycle.Usage
			}
		case RunLifecycleRun:
			if event.Lifecycle.Terminal && event.Lifecycle.Status == RunLifecycleCompleted {
				runTerminal = true
			}
		}
	}

	if len(usageDeltas) != 3 {
		t.Fatalf("usage deltas = %#v, want 3 deltas", usageDeltas)
	}
	if usageDeltas[0].InputTokens != 10 ||
		usageDeltas[0].OutputTokens != 0 ||
		usageDeltas[0].TotalTokens != 10 {
		t.Fatalf("first usage delta = %#v", usageDeltas[0])
	}
	if usageDeltas[1].InputTokens != 0 ||
		usageDeltas[1].OutputTokens != 5 ||
		usageDeltas[1].TotalTokens != 5 {
		t.Fatalf("second usage delta = %#v", usageDeltas[1])
	}
	if usageDeltas[2].InputTokens != 2 ||
		usageDeltas[2].OutputTokens != 1 ||
		usageDeltas[2].TotalTokens != 3 {
		t.Fatalf("usage delta after tool completion = %#v", usageDeltas[2])
	}
	if !toolStarted || !toolOutputDelta || !toolCompleted {
		t.Fatalf(
			"tool lifecycle started=%t delta=%t completed=%t",
			toolStarted,
			toolOutputDelta,
			toolCompleted,
		)
	}
	if turnTerminalUsage == nil ||
		turnTerminalUsage.Kind != RunUsageTurn ||
		turnTerminalUsage.Cumulative.TotalTokens != 18 {
		t.Fatalf("turn terminal usage = %#v", turnTerminalUsage)
	}
	if usageHasValue(turnTerminalUsage.Delta) {
		t.Fatalf("turn terminal usage delta = %#v, want already emitted", turnTerminalUsage.Delta)
	}
	if !runTerminal {
		t.Fatal("missing run terminal lifecycle")
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
		switch event.Kind() {
		case RunEventSession:
			data, ok, err := runEventSession(t, event).TurnCompletedData()
			if err != nil {
				t.Fatalf("decode turn completed: %v", err)
			}
			if ok && data.Usage.TotalTokens == usage.TotalTokens {
				if event.Usage == nil ||
					event.Usage.Cumulative.TotalTokens != usage.TotalTokens ||
					event.Usage.Cumulative.Cost != usage.Cost {
					t.Fatalf("turn event usage = %#v, want cumulative usage", event.Usage)
				}
				order = append(order, "turn_usage")
			}
		case RunEventResult:
			if runEventResult(t, event).Usage.TotalTokens != usage.TotalTokens {
				t.Fatalf("result usage = %#v, want %#v", runEventResult(t, event).Usage, usage)
			}
			order = append(order, "result")
		case RunEventError:
			t.Fatalf("stream error: %v", runEventErr(t, event))
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
		sawToolStart bool
	)
	record := func(event RunEvent) {
		t.Helper()
		switch event.Kind() {
		case RunEventSession:
			switch runEventSession(t, event).Type {
			case session.ToolStarted:
				sawToolStart = true
				order = append(order, "tool_started")
			case session.ToolCompleted:
				data, ok, err := runEventSession(t, event).ToolCompletedData()
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
			t.Fatalf("stream error: %v", runEventErr(t, event))
		}
	}

	for event := range events {
		record(event)
		if hookReleased || !sawToolStart {
			continue
		}
		select {
		case <-hookEntered:
		case <-time.After(time.Second):
			t.Fatal("post-tool hook did not run after tool started")
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
		RuntimeOptions(runtime.WithOverflowRecovery(
			provider.IsContextOverflow,
			harnessCompactor(t, provider, "faux", governor.CompactOptions{
				MaxTokens:  1000,
				OffloadDir: t.TempDir(),
			}),
			1,
		)).
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
		sawRecovery bool
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
		if event.Lifecycle != nil &&
			event.Lifecycle.Type == RunLifecycleRetry &&
			event.Lifecycle.Retry != nil &&
			event.Lifecycle.Retry.Scope == "overflow_recovery" {
			sawRecovery = true
		}
		switch event.Kind() {
		case RunEventResult:
			resultCount++
			if runEventResult(t, event).Content != "recovered" {
				t.Fatalf("result content = %q, want recovered", runEventResult(t, event).Content)
			}
		case RunEventError:
			t.Fatalf("stream error after overflow recovery: %v", runEventErr(t, event))
		}
	}
	if resultCount != 1 {
		t.Fatalf("result events = %d, want 1", resultCount)
	}
	if !sawRecovery {
		t.Fatal("missing overflow recovery retry lifecycle event")
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
