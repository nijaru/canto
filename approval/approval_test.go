package approval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nijaru/canto/session"
)

func TestManager_RequestResolveAllow(t *testing.T) {
	mgr := NewManager(nil)
	sess := session.New("allow")

	done := make(chan Result, 1)
	go func() {
		res, err := mgr.Request(context.Background(), sess, "bash", "{}", Requirement{
			Category:  "command",
			Operation: "exec",
			Resource:  "bash",
		})
		if err != nil {
			t.Errorf("Request: %v", err)
			return
		}
		done <- res
	}()

	time.Sleep(10 * time.Millisecond)
	pending := mgr.Pending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending request, got %d", len(pending))
	}
	if err := mgr.Resolve(pending[0], DecisionAllow, "ok"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-done:
		if !res.Allowed() {
			t.Fatalf("expected allowed result, got %#v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resolution")
	}
}

func TestManager_RequestResolveDeny(t *testing.T) {
	mgr := NewManager(nil)
	sess := session.New("deny")

	done := make(chan error, 1)
	go func() {
		res, err := mgr.Request(context.Background(), sess, "write_file", "{}", Requirement{
			Category:  "workspace",
			Operation: "write_file",
			Resource:  "a.txt",
		})
		if err != nil {
			done <- err
			return
		}
		done <- res.Error()
	}()

	time.Sleep(10 * time.Millisecond)
	pending := mgr.Pending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending request, got %d", len(pending))
	}
	if err := mgr.Resolve(pending[0], DecisionDeny, "unsafe"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case err := <-done:
		if err == nil || err.Error() != "approval denied: unsafe" {
			t.Fatalf("unexpected deny error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for denial")
	}
}

func TestManager_DuplicateAndLateDecisionRejected(t *testing.T) {
	mgr := NewManager(nil)
	sess := session.New("dup")

	go func() {
		_, _ = mgr.Request(context.Background(), sess, "bash", "{}", Requirement{
			Category: "command", Operation: "exec",
		})
	}()

	time.Sleep(10 * time.Millisecond)
	pending := mgr.Pending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending request, got %d", len(pending))
	}
	id := pending[0]
	if err := mgr.Resolve(id, DecisionAllow, "ok"); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if err := mgr.Resolve(id, DecisionAllow, "again"); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("second Resolve = %v, want ErrRequestNotFound", err)
	}
}

func TestManager_RequestCancellation(t *testing.T) {
	mgr := NewManager(nil)
	sess := session.New("cancel")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := mgr.Request(ctx, sess, "bash", "{}", Requirement{
			Category: "command", Operation: "exec",
		})
		done <- err
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancellation")
	}
	if got := len(mgr.Pending()); got != 0 {
		t.Fatalf("expected no pending requests after cancel, got %d", got)
	}
}
