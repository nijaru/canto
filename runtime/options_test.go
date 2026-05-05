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
	if runner.scheduler == nil {
		t.Fatal("expected default scheduler to be applied")
	}
}

func TestNewRunnerKeepsNoopHooksWhenOptionIsNil(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	runner := NewRunner(store, &echoAgent{}, WithHooks(nil))
	defer runner.Close()

	if runner.hooks == nil {
		t.Fatal("expected nil hooks option to keep a no-op hook runner")
	}
	if _, err := runner.Send(t.Context(), "nil-hooks", "hello"); err != nil {
		t.Fatalf("send with nil hooks option: %v", err)
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

func TestNewRunnerBuildsSharedChildRunner(t *testing.T) {
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
		WithWaitTimeout(7*time.Second),
		WithExecutionTimeout(70*time.Second),
		WithCoordinator(coord),
		WithHooks(hooks),
		WithMaxConcurrent(3),
	)
	defer runner.Close()

	if runner.childRunner == nil {
		t.Fatal("expected shared child runner")
	}
	if runner.childRunner.waitTimeout != 7*time.Second {
		t.Fatalf("shared child wait timeout = %v, want 7s", runner.childRunner.waitTimeout)
	}
	if runner.childRunner.executionTimeout != 70*time.Second {
		t.Fatalf(
			"shared child execution timeout = %v, want 70s",
			runner.childRunner.executionTimeout,
		)
	}
	if runner.childRunner.coordinator != coord {
		t.Fatal("expected shared child runner to inherit coordinator")
	}
	if runner.childRunner.hooks != hooks {
		t.Fatal("expected shared child runner to inherit hooks")
	}
	if runner.childRunner.maxConcurrent != 3 {
		t.Fatalf("shared child max concurrent = %d, want 3", runner.childRunner.maxConcurrent)
	}
}

func TestNewRunnerAppliesSchedulerOption(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	scheduler := NewLocalScheduler()
	runner := NewRunner(store, &echoAgent{}, WithScheduler(scheduler))
	defer runner.Close()

	if runner.scheduler != scheduler {
		t.Fatal("expected scheduler option to be applied")
	}
}
