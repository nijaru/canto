package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nijaru/canto/safety"
)

type fakeSandbox struct {
	called bool
	opts   safety.SandboxOptions
}

func (f *fakeSandbox) Wrap(cmd *exec.Cmd, opts safety.SandboxOptions) error {
	f.called = true
	f.opts = opts
	return nil
}

func TestExecutor_RunStructuredResult(t *testing.T) {
	e := NewExecutor(time.Second, 1024)

	result, err := e.Run(t.Context(), Command{
		Name: "bash",
		Args: []string{"-c", "printf stdout; printf stderr >&2"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "stdout" {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, "stdout")
	}
	if result.Stderr != "stderr" {
		t.Fatalf("Stderr = %q, want %q", result.Stderr, "stderr")
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
}

func TestExecutor_Timeout(t *testing.T) {
	e := NewExecutor(100*time.Millisecond, 1000)

	result, err := e.Run(context.Background(), Command{
		Name: "sleep",
		Args: []string{"1"},
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !result.TimedOut {
		t.Fatal("expected TimedOut to be true")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestExecutor_Truncation(t *testing.T) {
	e := NewExecutor(time.Second, 10)

	result, err := e.Run(t.Context(), Command{
		Name: "echo",
		Args: []string{"hello world"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Truncated {
		t.Fatal("expected output to be truncated")
	}
	if !strings.Contains(result.Combined, "[Output truncated due to size limits]") {
		t.Errorf("expected output to be truncated, got: %q", result.Combined)
	}
}

func TestExecutor_StreamsOutput(t *testing.T) {
	e := NewExecutor(time.Second, 1024)
	var chunks []OutputChunk

	_, err := e.Run(t.Context(), Command{
		Name: "bash",
		Args: []string{"-c", "printf one; printf two >&2"},
		OnOutput: func(chunk OutputChunk) {
			chunks = append(chunks, chunk)
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawStdout bool
	var sawStderr bool
	for _, chunk := range chunks {
		if chunk.Stream == StdoutStream && strings.Contains(chunk.Text, "one") {
			sawStdout = true
		}
		if chunk.Stream == StderrStream && strings.Contains(chunk.Text, "two") {
			sawStderr = true
		}
	}
	if !sawStdout || !sawStderr {
		t.Fatalf("expected both stdout and stderr chunks, got %#v", chunks)
	}
}

func TestExecutor_AppliesSandboxAndSanitizesEnvironment(t *testing.T) {
	sandbox := &fakeSandbox{}
	e := &Executor{
		Timeout:        time.Second,
		MaxOutputBytes: 2048,
		Sandbox:        sandbox,
		EnvSanitizer: &safety.EnvSanitizer{
			Allow: []string{"PATH", "SAFE_VAR"},
			Deny:  []string{"KEY", "TOKEN"},
		},
	}

	result, err := e.Run(t.Context(), Command{
		Name: "bash",
		Args: []string{"-lc", `printf "%s|%s" "$SAFE_VAR" "$OPENAI_API_KEY"`},
		Env:  []string{"SAFE_VAR=ok", "OPENAI_API_KEY=secret"},
		Sandbox: &safety.SandboxOptions{
			WorkDir:       t.TempDir(),
			WritablePaths: []string{t.TempDir()},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sandbox.called {
		t.Fatal("expected sandbox to be invoked")
	}
	if result.Stdout != "ok|" {
		t.Fatalf("expected sanitized command output %q, got %q", "ok|", result.Stdout)
	}
}
