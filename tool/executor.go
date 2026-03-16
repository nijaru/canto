package tool

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Executor provides a standardized way to execute commands with timeouts,
// output truncation, and a basic sandbox (via subprocess constraints).
type Executor struct {
	Timeout       time.Duration
	MaxOutputSize int
}

// NewExecutor creates a new executor with the given timeout and max output size.
func NewExecutor(timeout time.Duration, maxOutputSize int) *Executor {
	return &Executor{
		Timeout:       timeout,
		MaxOutputSize: maxOutputSize,
	}
}

// Execute runs the command with the executor's constraints.
func (e *Executor) Execute(ctx context.Context, name string, args ...string) (string, error) {
	// 1. Create context with timeout
	tCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()

	// 2. Setup command
	cmd := exec.CommandContext(tCtx, name, args...)

	// 3. Setup output buffers and truncation
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 4. Run command
	err := cmd.Run()

	// 5. Combine outputs
	out := stdout.String() + stderr.String()

	// 6. Handle truncation
	if len(out) > e.MaxOutputSize {
		out = out[:e.MaxOutputSize] + "\n\n[Output truncated due to size limits]"
	}

	// 7. Handle timeout error
	if tCtx.Err() == context.DeadlineExceeded {
		return out + "\n\n[Execution timed out]", fmt.Errorf(
			"command timed out after %v",
			e.Timeout,
		)
	}

	// 8. Handle execution error — return raw output; caller appends error context.
	if err != nil {
		return out, fmt.Errorf("command failed: %w", err)
	}

	return out, nil
}

// DefaultExecutor provides a safe default for tool execution.
var DefaultExecutor = NewExecutor(30*time.Second, 10000)
