package runtime

import (
	"testing"
	"time"

	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/session"
)

func TestNewRunnerWithConfigAppliesSettings(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	coord := NewLocalCoordinator()
	hooks := hook.NewRunner()
	runner := NewRunnerWithConfig(store, &echoAgent{}, ExecutionConfig{
		WaitTimeout:      5 * time.Second,
		ExecutionTimeout: 45 * time.Second,
		Coordinator:      coord,
		Hooks:            hooks,
	})

	if runner.WaitTimeout != 5*time.Second {
		t.Fatalf("wait timeout = %v, want 5s", runner.WaitTimeout)
	}
	if runner.ExecutionTimeout != 45*time.Second {
		t.Fatalf("execution timeout = %v, want 45s", runner.ExecutionTimeout)
	}
	if runner.Coordinator != coord {
		t.Fatal("expected coordinator to be applied")
	}
	if runner.Hooks != hooks {
		t.Fatal("expected hooks to be applied")
	}
}

func TestRunnerChildRunnerInheritsConfig(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	coord := NewLocalCoordinator()
	hooks := hook.NewRunner()
	runner := NewRunnerWithConfig(store, &echoAgent{}, ExecutionConfig{
		WaitTimeout:      9 * time.Second,
		ExecutionTimeout: 90 * time.Second,
		Coordinator:      coord,
		Hooks:            hooks,
	})

	childRunner := runner.ChildRunner()
	defer childRunner.Close()

	if childRunner.Store != store {
		t.Fatal("expected child runner to reuse store")
	}
	if childRunner.WaitTimeout != runner.WaitTimeout {
		t.Fatalf("child wait timeout = %v, want %v", childRunner.WaitTimeout, runner.WaitTimeout)
	}
	if childRunner.ExecutionTimeout != runner.ExecutionTimeout {
		t.Fatalf(
			"child execution timeout = %v, want %v",
			childRunner.ExecutionTimeout,
			runner.ExecutionTimeout,
		)
	}
	if childRunner.Coordinator != coord {
		t.Fatal("expected child runner to inherit coordinator")
	}
	if childRunner.Hooks != hooks {
		t.Fatal("expected child runner to inherit hooks")
	}
}
