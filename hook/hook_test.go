package hook

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nijaru/canto/session"
)

func TestHookRunner(t *testing.T) {
	runner := NewRunner()
	sess := session.New("test-session")

	// 1. Success Hook
	hookSuccess := NewCommandHook(
		"success-hook",
		[]HookEvent{EventSessionStart},
		"sh",
		[]string{"-c", "echo 'success'"},
		2*time.Second,
	)

	// 2. Log Hook (Exit 1)
	hookLog := NewCommandHook(
		"log-hook",
		[]HookEvent{EventSessionStart},
		"sh",
		[]string{"-c", "echo 'log this'; exit 1"},
		2*time.Second,
	)

	// 3. Block Hook (Exit 2)
	hookBlock := NewCommandHook(
		"block-hook",
		[]HookEvent{EventPreToolUse},
		"sh",
		[]string{"-c", "echo 'stop right there' >&2; exit 2"},
		2*time.Second,
	)

	runner.Register(hookSuccess)
	runner.Register(hookLog)
	runner.Register(hookBlock)

	// Test SessionStart: should run 2 hooks, none block
	results, err := runner.Run(context.Background(), EventSessionStart, sess, nil)
	if err != nil {
		t.Fatalf("did not expect error for SessionStart, got: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Action != HookActionProceed {
		t.Errorf("expected proceed action for first hook, got %d", results[0].Action)
	}
	if !strings.Contains(results[0].Output, "success") {
		t.Errorf("expected 'success' in output, got '%s'", results[0].Output)
	}

	if results[1].Action != HookActionLog {
		t.Errorf("expected log action for second hook, got %d", results[1].Action)
	}
	if !strings.Contains(results[1].Output, "log this") {
		t.Errorf("expected 'log this' in output, got '%s'", results[1].Output)
	}

	// Test PreToolUse: should run 1 hook, block
	results, err = runner.Run(context.Background(), EventPreToolUse, sess, nil)
	if err == nil {
		t.Fatalf("expected error for PreToolUse (block hook)")
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].Action != HookActionBlock {
		t.Errorf("expected block action, got %d", results[0].Action)
	}
}
