package coding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/nijaru/canto/audit"
	"github.com/nijaru/canto/safety"
)

// OutputStream identifies which stream produced a chunk.
type OutputStream string

const (
	StdoutStream OutputStream = "stdout"
	StderrStream OutputStream = "stderr"
)

// OutputChunk is emitted while a command runs.
type OutputChunk struct {
	Stream OutputStream
	Text   string
}

// Command describes one subprocess execution.
type Command struct {
	Name        string
	Args        []string
	Dir         string
	Env         []string
	SecretNames []string
	OnOutput    func(OutputChunk)
	Sandbox     *safety.SandboxOptions
}

// Result captures the structured outcome of a subprocess run.
type Result struct {
	Stdout    string
	Stderr    string
	Combined  string
	ExitCode  int
	TimedOut  bool
	Truncated bool
}

// Executor provides a standardized way to execute commands with timeouts,
// bounded output capture, and streaming callbacks.
type Executor struct {
	Timeout          time.Duration
	MaxOutputBytes   int
	Sandbox          safety.Sandbox
	EnvSanitizer     *safety.EnvSanitizer
	SecretInjector   safety.SecretInjector
	OutputCompressor OutputCompressor
	AuditLogger      audit.Logger
}

// NewExecutor creates a new executor with the given timeout and max output size.
func NewExecutor(timeout time.Duration, maxOutputBytes int) *Executor {
	return &Executor{
		Timeout:          timeout,
		MaxOutputBytes:   maxOutputBytes,
		EnvSanitizer:     safety.NewEnvSanitizer(),
		OutputCompressor: NewLineOutputCompressor(),
	}
}

// Run executes cmd using the executor's timeout and output-capture limits.
func (e *Executor) Run(ctx context.Context, cmd Command) (Result, error) {
	if cmd.Name == "" {
		return Result{}, errors.New("executor: command name is required")
	}

	runCtx := ctx
	cancel := func() {}
	if e.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, e.Timeout)
	}
	defer cancel()

	execCmd := exec.CommandContext(runCtx, cmd.Name, cmd.Args...)
	execCmd.Dir = cmd.Dir
	env := execCmd.Environ()
	if len(cmd.Env) > 0 {
		env = append(env, cmd.Env...)
	}
	if e.EnvSanitizer != nil {
		env = e.EnvSanitizer.Sanitize(env)
	}
	if len(cmd.SecretNames) > 0 {
		if e.SecretInjector == nil {
			if e.AuditLogger != nil {
				_ = e.AuditLogger.Log(context.Background(), audit.Event{
					Kind:      audit.KindSecretInjectionFailed,
					Tool:      cmd.Name,
					Category:  "execute",
					Operation: "secret.inject",
					Resource:  cmd.Name,
					Decision:  "deny",
					Reason:    "secret injector is not configured",
					Metadata: map[string]any{
						"secret_count": len(cmd.SecretNames),
					},
				})
			}
			return Result{}, errors.New("executor secret injector is not configured")
		}
		injected, err := e.SecretInjector.Inject(runCtx, cmd.SecretNames)
		if err != nil {
			if e.AuditLogger != nil {
				_ = e.AuditLogger.Log(context.Background(), audit.Event{
					Kind:      audit.KindSecretInjectionFailed,
					Tool:      cmd.Name,
					Category:  "execute",
					Operation: "secret.inject",
					Resource:  cmd.Name,
					Decision:  "deny",
					Reason:    err.Error(),
					Metadata: map[string]any{
						"secret_count": len(cmd.SecretNames),
					},
				})
			}
			return Result{}, fmt.Errorf("executor secret injection: %w", err)
		}
		env = append(env, injected...)
		if e.AuditLogger != nil {
			_ = e.AuditLogger.Log(context.Background(), audit.Event{
				Kind:      audit.KindSecretInjected,
				Tool:      cmd.Name,
				Category:  "execute",
				Operation: "secret.inject",
				Resource:  cmd.Name,
				Decision:  "allow",
				Reason:    "secrets injected into subprocess environment",
				Metadata: map[string]any{
					"secret_count": len(cmd.SecretNames),
					"injected":     len(injected),
				},
			})
		}
	}
	if len(env) > 0 {
		execCmd.Env = env
	}
	if e.Sandbox != nil {
		opts := safety.SandboxOptions{WorkDir: cmd.Dir}
		if cmd.Sandbox != nil {
			opts = *cmd.Sandbox
			if opts.WorkDir == "" {
				opts.WorkDir = cmd.Dir
			}
		}
		if err := e.Sandbox.Wrap(execCmd, opts); err != nil {
			if e.AuditLogger != nil {
				kind := audit.KindSandboxEscapeAttempt
				if errors.Is(err, safety.ErrSandboxUnavailable) {
					kind = audit.KindSandboxWrapFailed
				}
				_ = e.AuditLogger.Log(context.Background(), audit.Event{
					Kind:      kind,
					Tool:      cmd.Name,
					Category:  string(safety.CategoryExecute),
					Operation: "sandbox.wrap",
					Resource:  cmd.Name,
					Decision:  "deny",
					Reason:    err.Error(),
					Metadata: map[string]any{
						"workdir": cmd.Dir,
					},
				})
			}
			return Result{}, fmt.Errorf("executor sandbox: %w", err)
		}
	}
	configureExecutorProcess(execCmd)

	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("executor stdout pipe: %w", err)
	}
	stderr, err := execCmd.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("executor stderr pipe: %w", err)
	}

	collector := newOutputCollector(e.MaxOutputBytes, cmd.OnOutput)

	if err := execCmd.Start(); err != nil {
		return Result{}, fmt.Errorf("executor start %q: %w", cmd.Name, err)
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-runCtx.Done():
			killExecutorProcess(execCmd)
		case <-done:
		}
	}()

	var wg sync.WaitGroup
	var readErrMu sync.Mutex
	var readErr error
	recordReadErr := func(err error) {
		if err == nil || errors.Is(err, io.EOF) {
			return
		}
		readErrMu.Lock()
		if readErr == nil {
			readErr = err
		}
		readErrMu.Unlock()
	}

	wg.Go(func() {
		recordReadErr(collector.readStream(StdoutStream, stdout))
	})
	wg.Go(func() {
		recordReadErr(collector.readStream(StderrStream, stderr))
	})

	// Drain both pipes before Wait. The exec package closes the parent's pipe
	// descriptors during Wait, so waiting first can race with the readers and
	// surface "file already closed" on fast-exiting commands.
	wg.Wait()
	waitErr := execCmd.Wait()

	if readErr != nil {
		return Result{}, fmt.Errorf("executor read output: %w", readErr)
	}

	result := collector.result()
	if execCmd.ProcessState != nil {
		result.ExitCode = execCmd.ProcessState.ExitCode()
	}
	if e.OutputCompressor != nil {
		result = e.OutputCompressor.Compress(result)
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		return result, fmt.Errorf("command timed out after %v", e.Timeout)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		return result, context.Canceled
	}
	if waitErr != nil {
		return result, fmt.Errorf("command failed: %w", waitErr)
	}

	return result, nil
}

// DefaultExecutor provides a safe default for tool execution.
var DefaultExecutor = NewExecutor(30*time.Second, 10000)
