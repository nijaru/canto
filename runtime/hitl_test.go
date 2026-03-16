package runtime

import (
	"context"
	"encoding/json"
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

	// Give goroutine time to block.
	time.Sleep(10 * time.Millisecond)
	gate.Provide("yes")

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
	gate.Provide("approved")
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
	gate.Provide("ok")

	select {
	case out := <-done:
		if out != "ok" {
			t.Fatalf("out = %q, want ok", out)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}
