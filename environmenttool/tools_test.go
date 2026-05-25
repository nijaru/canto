package environmenttool

import (
	"testing"
	"time"

	"github.com/nijaru/canto/executor"
	"github.com/nijaru/canto/executortool"
	"github.com/nijaru/canto/safety"
)

func TestToolsBuildsExecutorTools(t *testing.T) {
	exec := executor.NewExecutor(time.Second, 1024)
	secrets := safety.StaticSecretInjector{"TOKEN": "secret"}

	tools, err := Tools(
		Capabilities{
			Executor: exec,
			Secrets:  secrets,
		},
		Config{
			Executor:     true,
			CodeLanguage: "python",
		},
	)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}
	shell, ok := tools[0].(*executortool.ShellTool)
	if !ok {
		t.Fatalf("first tool = %T, want ShellTool", tools[0])
	}
	if shell.Executor == nil || shell.Executor == exec {
		t.Fatalf("shell executor = %#v, want environment-wired copy", shell.Executor)
	}
	if shell.Executor.SecretInjector == nil {
		t.Fatal("secret injector was not wired into executor copy")
	}
	code, ok := tools[1].(*executortool.CodeExecutionTool)
	if !ok {
		t.Fatalf("second tool = %T, want CodeExecutionTool", tools[1])
	}
	if code.Executor == nil || code.Executor.SecretInjector == nil {
		t.Fatalf("code executor = %#v, want environment-wired copy", code.Executor)
	}
}

func TestToolsRequiresExecutor(t *testing.T) {
	_, err := Tools(Capabilities{}, Config{Executor: true})
	if err == nil || err.Error() != "canto environment tools: executor is required" {
		t.Fatalf("error = %v, want executor requirement", err)
	}
}
