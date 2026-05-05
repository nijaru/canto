package hook

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/go-json-experiment/json"
)

// Command executes a shell command.
type Command struct {
	name    string
	events  []Event
	command string
	args    []string
	timeout time.Duration
}

// NewCommand creates a new command hook.
func NewCommand(
	name string,
	events []Event,
	command string,
	args []string,
	timeout time.Duration,
) *Command {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Command{
		name:    name,
		events:  events,
		command: command,
		args:    args,
		timeout: timeout,
	}
}

func (h *Command) Name() string { return h.name }

func (h *Command) Events() []Event { return h.events }

func (h *Command) Execute(ctx context.Context, payload *Payload) *Result {
	hookCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	b, err := json.Marshal(payload)
	if err != nil {
		return &Result{
			Action: ActionBlock,
			Error:  fmt.Errorf("failed to marshal payload: %w", err),
		}
	}

	cmd := exec.CommandContext(hookCtx, h.command, h.args...)
	cmd.Stdin = bytes.NewReader(b)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	output := stdout.String()

	if err != nil {
		if hookCtx.Err() == context.DeadlineExceeded {
			return &Result{
				Action: ActionBlock,
				Output: output,
				Error:  fmt.Errorf("hook %s timed out after %v", h.name, h.timeout),
			}
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			switch exitCode {
			case 1:
				return &Result{Action: ActionLog, Output: output, Error: nil}
			case 2:
				return &Result{
					Action: ActionBlock,
					Output: output,
					Error:  fmt.Errorf("hook blocked execution: %s", stderr.String()),
				}
			default:
				return &Result{
					Action: ActionBlock,
					Output: output,
					Error: fmt.Errorf(
						"hook failed with exit code %d: %s",
						exitCode,
						stderr.String(),
					),
				}
			}
		}

		return &Result{
			Action: ActionBlock,
			Output: output,
			Error:  fmt.Errorf("failed to run hook: %w", err),
		}
	}

	return &Result{Action: ActionProceed, Output: output, Error: nil}
}
