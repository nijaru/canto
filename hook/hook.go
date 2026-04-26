package hook

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/go-json-experiment/json"
)

// Event defines the lifecycle events that triggers a hook.
type Event string

const (
	EventSessionStart       Event = "SessionStart"
	EventUserPromptSubmit   Event = "UserPromptSubmit"
	EventPreToolUse         Event = "PreToolUse"
	EventPostToolUse        Event = "PostToolUse"
	EventPostToolUseFailure Event = "PostToolUseFailure"
	EventSessionEnd         Event = "SessionEnd"
	EventStop               Event = "Stop"
)

// Action determines how the hook runner should proceed.
type Action int

const (
	ActionProceed Action = 0 // Exit 0: Proceed normally
	ActionLog     Action = 1 // Exit 1: Log output but proceed
	ActionBlock   Action = 2 // Exit 2: Block execution
)

// SessionMeta carries the session identifiers sent to hooks.
// It replaces *session.Session to avoid shipping the full event log
// to every hook subprocess.
type SessionMeta struct {
	ID      string `json:"id"`
	AgentID string `json:"agent_id,omitzero"`
}

// Payload is the JSON payload sent to the hook via stdin.
type Payload struct {
	Event   Event          `json:"event"`
	Session SessionMeta    `json:"session"`
	Data    map[string]any `json:"data,omitzero"`
}

// Result is the result of executing a hook.
type Result struct {
	Action Action
	Output string
	Data   map[string]any
	Error  error
}

// Handler interface defines a lifecycle hook.
type Handler interface {
	Name() string
	Events() []Event
	Execute(ctx context.Context, payload *Payload) *Result
}

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

// funcHandler runs an in-process Go function, avoiding subprocess overhead.
type funcHandler struct {
	name   string
	events []Event
	fn     func(ctx context.Context, payload *Payload) *Result
}

// FromFunc creates an in-process hook from a function.
func FromFunc(
	name string,
	events []Event,
	fn func(ctx context.Context, payload *Payload) *Result,
) *funcHandler {
	return &funcHandler{name: name, events: events, fn: fn}
}

func (h *funcHandler) Name() string { return h.name }

func (h *funcHandler) Events() []Event { return h.events }

func (h *funcHandler) Execute(ctx context.Context, payload *Payload) *Result {
	return h.fn(ctx, payload)
}

// Runner manages and executes hooks.
type Runner struct {
	mu    sync.RWMutex
	hooks []Handler
}

// NewRunner creates a new hook runner.
func NewRunner() *Runner {
	return &Runner{}
}

// Register adds a hook to the runner.
func (r *Runner) Register(h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, h)
}

// Run executes all hooks registered for the given event.
// It returns an error if any hook blocks execution (Exit Code 2).
func (r *Runner) Run(
	ctx context.Context,
	event Event,
	meta SessionMeta,
	data map[string]any,
) ([]*Result, error) {
	payload := &Payload{
		Event:   event,
		Session: meta,
		Data:    data,
	}

	var results []*Result

	r.mu.RLock()
	hooks := make([]Handler, len(r.hooks))
	copy(hooks, r.hooks)
	r.mu.RUnlock()

	for _, h := range hooks {
		matches := false
		for _, e := range h.Events() {
			if e == event {
				matches = true
				break
			}
		}

		if !matches {
			continue
		}

		res := h.Execute(ctx, payload)
		results = append(results, res)

		if res.Action == ActionBlock {
			if res.Error != nil {
				return results, fmt.Errorf(
					"hook %s blocked execution for event %s: %w",
					h.Name(), event, res.Error,
				)
			}
			return results, fmt.Errorf(
				"hook %s blocked execution for event %s",
				h.Name(), event,
			)
		}
	}

	return results, nil
}
