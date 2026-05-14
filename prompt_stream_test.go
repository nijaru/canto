package canto

import (
	"context"
	"testing"
	"time"

	"github.com/nijaru/canto/agent"
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
