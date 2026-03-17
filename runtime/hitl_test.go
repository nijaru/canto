package runtime

import (
	"context"
	"github.com/go-json-experiment/json"
	"testing"
	"time"

	"github.com/nijaru/canto/session"
)

func TestInputGate_RequestProvide(t *testing.T) {
	gate := NewInputGate()
	sess := session.New("s1")

	done := make(chan string, 1)
	go func() {
		resp, err := gate.Request(context.Background(), sess, "proceed?")
		if err != nil {
			t.Errorf("Request: %v", err)
			return
		}
		done <- resp
	}()

	time.Sleep(10 * time.Millisecond)
	gate.Provide(context.Background(), "yes")

	select {
	case resp := <-done:
		if resp != "yes" {
			t.Fatalf("resp = %q, want yes", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Request to unblock")
	}
}

func TestInputGate_EventsRecorded(t *testing.T) {
	gate := NewInputGate()
	sess := session.New("s2")

	go func() {
		gate.Request(context.Background(), sess, "approve?") //nolint
	}()
	time.Sleep(10 * time.Millisecond)
	gate.Provide(context.Background(), "approved")
	time.Sleep(10 * time.Millisecond)

	events := sess.Events()
	var externalEvents []session.Event
	for _, e := range events {
		if e.Type == session.EventTypeExternalInput {
			externalEvents = append(externalEvents, e)
		}
	}
	if len(externalEvents) != 2 {
		t.Fatalf("expected 2 external_input events, got %d", len(externalEvents))
	}

	var pending map[string]any
	json.Unmarshal(externalEvents[0].Data, &pending)
	if pending["status"] != "pending" {
		t.Errorf("first event status = %q, want pending", pending["status"])
	}

	var received map[string]any
	json.Unmarshal(externalEvents[1].Data, &received)
	if received["status"] != "received" {
		t.Errorf("second event status = %q, want received", received["status"])
	}
	if received["input"] != "approved" {
		t.Errorf("input = %q, want approved", received["input"])
	}
}

func TestInputGate_ContextCancel(t *testing.T) {
	gate := NewInputGate()
	sess := session.New("s3")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := gate.Request(ctx, sess, "question")
		done <- err
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected context error")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancelled Request")
	}
}

func TestInputGate_CancelDrainsStaleValue(t *testing.T) {
	// H1: verify that cancelling a Request drains g.ch so the next
	// Provide/Request exchange is not contaminated by the stale value.
	gate := NewInputGate()
	sess := session.New("s3b")

	ctx, cancel := context.WithCancel(context.Background())

	// Start a Request, cancel it.
	reqDone := make(chan error, 1)
	go func() {
		_, err := gate.Request(ctx, sess, "stale question")
		reqDone <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-reqDone // wait for cancellation

	// A concurrent Provide races with cancel — its value might land in g.ch.
	// After Request returns, g.ch must be empty (drained in ctx.Done arm).
	// Verify the next Provide+Request sees the correct answer.
	sess2 := session.New("s3c")
	fresh := make(chan string, 1)
	go func() {
		resp, err := gate.Request(context.Background(), sess2, "fresh question")
		if err != nil {
			t.Errorf("fresh Request: %v", err)
			return
		}
		fresh <- resp
	}()

	time.Sleep(10 * time.Millisecond)
	gate.Provide(context.Background(), "correct answer")

	select {
	case resp := <-fresh:
		if resp != "correct answer" {
			t.Fatalf("resp = %q, want 'correct answer'", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out on fresh Request")
	}
}

func TestInputGate_Tool(t *testing.T) {
	gate := NewInputGate()
	sess := session.New("s4")

	tool := gate.Tool(sess)
	if tool.Spec().Name != "request_human_input" {
		t.Fatalf("tool name = %q", tool.Spec().Name)
	}

	done := make(chan string, 1)
	go func() {
		out, err := tool.Execute(context.Background(), `{"prompt":"ok?"}`)
		if err != nil {
			t.Errorf("Execute: %v", err)
			return
		}
		done <- out
	}()

	time.Sleep(10 * time.Millisecond)
	gate.Provide(context.Background(), "ok")

	select {
	case out := <-done:
		if out != "ok" {
			t.Fatalf("out = %q, want ok", out)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestInputGate_ProvideContextCancel(t *testing.T) {
	gate := NewInputGate()
	ctx, cancel := context.WithCancel(context.Background())

	// Fill the buffer so the second Provide blocks.
	gate.Provide(context.Background(), "buffer filler")

	delivered := make(chan bool, 1)
	go func() {
		// Buffer is full — Provide should block until ctx is cancelled.
		ok := gate.Provide(ctx, "never received")
		delivered <- ok
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case ok := <-delivered:
		if ok {
			t.Fatal("Provide returned true after context cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("Provide did not unblock on context cancel")
	}
}
