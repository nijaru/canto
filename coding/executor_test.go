package coding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/audit"
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

type failingSandbox struct {
	err error
}

func (f *failingSandbox) Wrap(cmd *exec.Cmd, opts safety.SandboxOptions) error {
	return f.err
}

type fakeSecretInjector struct {
	env []string
	err error
}

func (f *fakeSecretInjector) Inject(ctx context.Context, names []string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.env...), nil
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

func TestExecutor_CompressesRepeatedCombinedOutput(t *testing.T) {
	e := NewExecutor(time.Second, 1024)

	result, err := e.Run(t.Context(), Command{
		Name: "bash",
		Args: []string{"-lc", "printf 'line\\nline\\nline\\n\\nline2\\n'"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(result.Combined, "line\nline\nline") {
		t.Fatalf("expected repeated lines to be compressed, got %q", result.Combined)
	}
	if !strings.Contains(result.Combined, "3x line") {
		t.Fatalf("expected repeated line count, got %q", result.Combined)
	}
	if !strings.Contains(result.Combined, "line2") {
		t.Fatalf("expected trailing content to remain, got %q", result.Combined)
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

func TestExecutor_InjectsSecretsAfterSanitization(t *testing.T) {
	injector := &fakeSecretInjector{env: []string{"OPENAI_API_KEY=secret"}}
	e := &Executor{
		Timeout:        time.Second,
		MaxOutputBytes: 2048,
		SecretInjector: injector,
		EnvSanitizer: &safety.EnvSanitizer{
			Allow: []string{"PATH"},
			Deny:  []string{"KEY", "TOKEN"},
		},
	}

	result, err := e.Run(t.Context(), Command{
		Name:        "bash",
		Args:        []string{"-lc", `printf "%s" "$OPENAI_API_KEY"`},
		SecretNames: []string{"OPENAI_API_KEY"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "secret" {
		t.Fatalf("expected injected secret output %q, got %q", "secret", result.Stdout)
	}
}

func TestExecutor_RejectsSecretRequestsWithoutInjector(t *testing.T) {
	e := &Executor{
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
	}

	_, err := e.Run(t.Context(), Command{
		Name:        "bash",
		Args:        []string{"-lc", `echo hi`},
		SecretNames: []string{"OPENAI_API_KEY"},
	})
	if err == nil {
		t.Fatal("expected secret injector error")
	}
}

func TestExecutor_LogsSandboxFailure(t *testing.T) {
	var buf bytes.Buffer
	failing := &failingSandbox{err: errors.New("sandbox failed")}
	e := &Executor{
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
		Sandbox:        failing,
		AuditLogger:    audit.NewStreamLogger(&buf),
	}

	_, err := e.Run(t.Context(), Command{
		Name: "bash",
		Args: []string{"-c", "echo hi"},
	})
	if err == nil {
		t.Fatal("expected sandbox error")
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 audit line, got %d", len(lines))
	}

	var event audit.Event
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("decode audit event: %v", err)
	}
	if event.Kind != audit.KindSandboxEscapeAttempt {
		t.Fatalf("event kind = %q, want %q", event.Kind, audit.KindSandboxEscapeAttempt)
	}
	if !strings.Contains(event.Reason, "sandbox failed") {
		t.Fatalf("expected sandbox failure reason, got %q", event.Reason)
	}
}

func TestExecutor_LogsSandboxUnavailable(t *testing.T) {
	var buf bytes.Buffer
	failing := &failingSandbox{err: fmt.Errorf("backend missing: %w", safety.ErrSandboxUnavailable)}
	e := &Executor{
		Timeout:        time.Second,
		MaxOutputBytes: 1024,
		Sandbox:        failing,
		AuditLogger:    audit.NewStreamLogger(&buf),
	}

	_, err := e.Run(t.Context(), Command{
		Name: "bash",
		Args: []string{"-c", "echo hi"},
	})
	if err == nil {
		t.Fatal("expected sandbox error")
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 audit line, got %d", len(lines))
	}

	var event audit.Event
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("decode audit event: %v", err)
	}
	if event.Kind != audit.KindSandboxWrapFailed {
		t.Fatalf("event kind = %q, want %q", event.Kind, audit.KindSandboxWrapFailed)
	}
}
