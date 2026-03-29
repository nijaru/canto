package runtime

import (
	"testing"
	"time"

	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/session"
)

func TestNewRunnerAppliesOptions(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	coord := NewLocalCoordinator()
	hooks := hook.NewRunner()
	runner := NewRunner(
		store,
		&echoAgent{},
		WithWaitTimeout(5*time.Second),
		WithExecutionTimeout(45*time.Second),
		WithCoordinator(coord),
		WithHooks(hooks),
	)

	if runner.waitTimeout != 5*time.Second {
		t.Fatalf("wait timeout = %v, want 5s", runner.waitTimeout)
	}
	if runner.executionTimeout != 45*time.Second {
		t.Fatalf("execution timeout = %v, want 45s", runner.executionTimeout)
	}
	if runner.coordinator != coord {
		t.Fatal("expected coordinator to be applied")
	}
	if runner.hooks != hooks {
		t.Fatal("expected hooks to be applied")
	}
}

func TestRunnerChildRunnerInheritsOptions(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	coord := NewLocalCoordinator()
	hooks := hook.NewRunner()
	runner := NewRunner(
		store,
		&echoAgent{},
		WithWaitTimeout(9*time.Second),
		WithExecutionTimeout(90*time.Second),
		WithCoordinator(coord),
		WithHooks(hooks),
	)

	childRunner := runner.ChildRunner(WithMaxConcurrent(2))
	defer childRunner.Close()

	if childRunner.store != store {
		t.Fatal("expected child runner to reuse store")
	}
	if childRunner.waitTimeout != runner.waitTimeout {
		t.Fatalf("child wait timeout = %v, want %v", childRunner.waitTimeout, runner.waitTimeout)
	}
	if childRunner.executionTimeout != runner.executionTimeout {
		t.Fatalf(
			"child execution timeout = %v, want %v",
			childRunner.executionTimeout,
			runner.executionTimeout,
		)
	}
	if childRunner.coordinator != coord {
		t.Fatal("expected child runner to inherit coordinator")
	}
	if childRunner.hooks != hooks {
		t.Fatal("expected child runner to inherit hooks")
	}
	if childRunner.maxConcurrent != 2 {
		t.Fatalf("max concurrent = %d, want 2", childRunner.maxConcurrent)
	}
}
