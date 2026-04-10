package eval_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/x/eval"
)

func TestHarborConnectorBootstrapsHarnessContext(t *testing.T) {
	connector := eval.HarborConnector{}
	task, err := connector.Connect(eval.HarnessTaskSpec{
		TaskID:          "harbor-1",
		InstructionText: "Fix the bug in the workspace.",
		WorkspacePath:   "/workspace/project",
		ContainerImage:  "ghcr.io/example/harbor:latest",
		SetupCommands:   []string{"go test ./...", "go build ./..."},
		TestCommands:    []string{"make test"},
		Notes:           []string{"Do not leave the workspace broken."},
		Metadata:        map[string]any{"domain": "web"},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	env, ok := task.Env.(eval.HarnessEnvironment)
	if !ok {
		t.Fatalf("expected HarnessEnvironment, got %T", task.Env)
	}

	sess := session.New("sess")
	if err := env.Bootstrap(context.Background(), sess); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	msgs := sess.Messages()
	if got, want := len(msgs), 1; got != want {
		t.Fatalf("message count: got %d want %d", got, want)
	}
	if msgs[0].Role != llm.RoleSystem {
		t.Fatalf("bootstrap role: got %s want %s", msgs[0].Role, llm.RoleSystem)
	}
	for _, want := range []string{
		"Harness: Harbor",
		"Workspace: /workspace/project",
		"Container image: ghcr.io/example/harbor:latest",
		"Setup commands:",
		"- go test ./...",
		"Test commands:",
		"- make test",
		"Metadata:",
		"- domain: web",
	} {
		if !strings.Contains(msgs[0].Content, want) {
			t.Fatalf("bootstrap content missing %q:\n%s", want, msgs[0].Content)
		}
	}
}

func TestSWEBenchConnectorValidatesRepository(t *testing.T) {
	connector := eval.SWEBenchConnector{}
	if _, err := connector.Connect(eval.HarnessTaskSpec{
		TaskID: "swe-1",
	}); err == nil {
		t.Fatal("expected missing repository error")
	}
}

func TestSWEBenchConnectorBootstrapsHarnessContext(t *testing.T) {
	connector := eval.SWEBenchConnector{}
	task, err := connector.Connect(eval.HarnessTaskSpec{
		TaskID:          "swe-1",
		InstructionText: "Resolve the issue.",
		Repository:      "github.com/example/repo",
		BaseCommit:      "abc123",
		Notes:           []string{"Use the repo under test."},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	env, ok := task.Env.(eval.HarnessEnvironment)
	if !ok {
		t.Fatalf("expected HarnessEnvironment, got %T", task.Env)
	}

	sess := session.New("sess")
	if err := env.Bootstrap(context.Background(), sess); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	msgs := sess.Messages()
	if got, want := len(msgs), 1; got != want {
		t.Fatalf("message count: got %d want %d", got, want)
	}
	if msgs[0].Role != llm.RoleSystem {
		t.Fatalf("bootstrap role: got %s want %s", msgs[0].Role, llm.RoleSystem)
	}
	for _, want := range []string{
		"Harness: SWE-bench",
		"Repository: github.com/example/repo",
		"Base commit: abc123",
		"Notes:",
		"- Use the repo under test.",
	} {
		if !strings.Contains(msgs[0].Content, want) {
			t.Fatalf("bootstrap content missing %q:\n%s", want, msgs[0].Content)
		}
	}
}
