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
