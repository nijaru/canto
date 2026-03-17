package hook

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHookRunner(t *testing.T) {
	runner := NewRunner()
	meta := SessionMeta{ID: "test-session"}

	// 1. Success Hook
	hookSuccess := NewCommand(
		"success-hook",
		[]Event{EventSessionStart},
		"sh",
		[]string{"-c", "echo 'success'"},
		2*time.Second,
	)

	// 2. Log Hook (Exit 1)
	hookLog := NewCommand(
		"log-hook",
		[]Event{EventSessionStart},
		"sh",
		[]string{"-c", "echo 'log this'; exit 1"},
		2*time.Second,
	)

	// 3. Block Hook (Exit 2)
	hookBlock := NewCommand(
		"block-hook",
		[]Event{EventPreToolUse},
		"sh",
		[]string{"-c", "echo 'stop right there' >&2; exit 2"},
		2*time.Second,
	)

	runner.Register(hookSuccess)
	runner.Register(hookLog)
	runner.Register(hookBlock)

	// Test SessionStart: should run 2 hooks, none block
	results, err := runner.Run(context.Background(), EventSessionStart, meta, nil)
	if err != nil {
		t.Fatalf("did not expect error for SessionStart, got: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Action != ActionProceed {
		t.Errorf("expected proceed action for first hook, got %d", results[0].Action)
	}
	if !strings.Contains(results[0].Output, "success") {
		t.Errorf("expected 'success' in output, got '%s'", results[0].Output)
	}

	if results[1].Action != ActionLog {
		t.Errorf("expected log action for second hook, got %d", results[1].Action)
	}
	if !strings.Contains(results[1].Output, "log this") {
		t.Errorf("expected 'log this' in output, got '%s'", results[1].Output)
	}

	// Test PreToolUse: should run 1 hook, block
	results, err = runner.Run(context.Background(), EventPreToolUse, meta, nil)
	if err == nil {
		t.Fatalf("expected error for PreToolUse (block hook)")
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].Action != ActionBlock {
		t.Errorf("expected block action, got %d", results[0].Action)
	}
}

func TestFuncHook(t *testing.T) {
	called := false
	h := NewFunc(
		"test-func",
		[]Event{EventSessionStart},
		func(_ context.Context, p *Payload) *Result {
			called = true
			if p.Session.ID != "sess-1" {
				t.Errorf("session ID = %q, want sess-1", p.Session.ID)
			}
			return &Result{Action: ActionProceed, Output: "ok"}
		},
	)

	runner := NewRunner()
	runner.Register(h)

	results, err := runner.Run(
		context.Background(),
		EventSessionStart,
		SessionMeta{ID: "sess-1"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Func fn not called")
	}
	if len(results) != 1 || results[0].Output != "ok" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestFuncHook_Block(t *testing.T) {
	h := NewFunc(
		"blocker",
		[]Event{EventPreToolUse},
		func(_ context.Context, _ *Payload) *Result {
			return &Result{Action: ActionBlock, Error: context.Canceled}
		},
	)

	runner := NewRunner()
	runner.Register(h)

	_, err := runner.Run(context.Background(), EventPreToolUse, SessionMeta{ID: "s"}, nil)
	if err == nil {
		t.Fatal("expected block error")
	}
}
