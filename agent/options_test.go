package agent

import (
	"testing"

	"github.com/nijaru/canto/hook"
)

func TestWithHookRunnerNilKeepsNoopRunner(t *testing.T) {
	a := New("agent", "", "model", &mockProvider{}, nil, WithHookRunner(nil))

	if a.hooks == nil {
		t.Fatal("expected nil hook runner to normalize to no-op runner")
	}
}

func TestWithHooksAfterNilHookRunner(t *testing.T) {
	a := New(
		"agent",
		"",
		"model",
		&mockProvider{},
		nil,
		WithHookRunner(nil),
		WithHooks(hook.FromFunc("noop", []hook.Event{hook.EventSessionStart}, nil)),
	)

	if a.hooks == nil {
		t.Fatal("expected WithHooks to restore hook runner after nil replacement")
	}
}
