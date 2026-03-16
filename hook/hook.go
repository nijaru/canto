package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// HookEvent defines the lifecycle events that triggers a hook.
type HookEvent string

const (
	EventSessionStart       HookEvent = "SessionStart"
	EventUserPromptSubmit   HookEvent = "UserPromptSubmit"
	EventPreToolUse         HookEvent = "PreToolUse"
	EventPostToolUse        HookEvent = "PostToolUse"
	EventPostToolUseFailure HookEvent = "PostToolUseFailure"
	EventPreCompact         HookEvent = "PreCompact"
	EventSessionEnd         HookEvent = "SessionEnd"
	EventStop               HookEvent = "Stop"
)

// HookAction determines how the hook runner should proceed.
type HookAction int

const (
	HookActionProceed HookAction = 0 // Exit 0: Proceed normally
	HookActionLog     HookAction = 1 // Exit 1: Log output but proceed
	HookActionBlock   HookAction = 2 // Exit 2: Block execution
)

// SessionMeta carries the session identifiers sent to hooks.
// It replaces *session.Session to avoid shipping the full event log
// to every hook subprocess.
type SessionMeta struct {
	ID      string `json:"id"`
	AgentID string `json:"agent_id,omitempty"`
}

// HookPayload is the JSON payload sent to the hook via stdin.
type HookPayload struct {
	Event   HookEvent      `json:"event"`
	Session SessionMeta    `json:"session"`
	Data    map[string]any `json:"data,omitempty"`
}

// HookResult is the result of executing a hook.
type HookResult struct {
	Action HookAction
	Output string
	Error  error
}

// Hook interface defines a lifecycle hook.
type Hook interface {
	Name() string
	Events() []HookEvent
	Execute(ctx context.Context, payload *HookPayload) *HookResult
}

// CommandHook executes a shell command.
type CommandHook struct {
	name    string
	events  []HookEvent
	command string
	args    []string
	timeout time.Duration
}

// NewCommandHook creates a new command hook.
func NewCommandHook(
	name string,
	events []HookEvent,
	command string,
	args []string,
	timeout time.Duration,
) *CommandHook {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &CommandHook{
		name:    name,
		events:  events,
		command: command,
		args:    args,
		timeout: timeout,
	}
}

func (h *CommandHook) Name() string { return h.name }

func (h *CommandHook) Events() []HookEvent { return h.events }

func (h *CommandHook) Execute(ctx context.Context, payload *HookPayload) *HookResult {
	hookCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	b, err := json.Marshal(payload)
	if err != nil {
		return &HookResult{
			Action: HookActionBlock,
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
			return &HookResult{
				Action: HookActionBlock,
				Output: output,
				Error:  fmt.Errorf("hook %s timed out after %v", h.name, h.timeout),
			}
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			switch exitCode {
			case 1:
				return &HookResult{Action: HookActionLog, Output: output, Error: nil}
			case 2:
				return &HookResult{
					Action: HookActionBlock,
					Output: output,
					Error:  fmt.Errorf("hook blocked execution: %s", stderr.String()),
				}
			default:
				return &HookResult{
					Action: HookActionBlock,
					Output: output,
					Error: fmt.Errorf(
						"hook failed with exit code %d: %s",
						exitCode,
						stderr.String(),
					),
				}
			}
		}

		return &HookResult{
			Action: HookActionBlock,
			Output: output,
			Error:  fmt.Errorf("failed to run hook: %w", err),
		}
	}

	return &HookResult{Action: HookActionProceed, Output: output, Error: nil}
}

// FuncHook runs an in-process Go function, avoiding subprocess overhead.
type FuncHook struct {
	name   string
	events []HookEvent
	fn     func(ctx context.Context, payload *HookPayload) *HookResult
}

// NewFuncHook creates an in-process hook from a function.
func NewFuncHook(
	name string,
	events []HookEvent,
	fn func(ctx context.Context, payload *HookPayload) *HookResult,
) *FuncHook {
	return &FuncHook{name: name, events: events, fn: fn}
}

func (h *FuncHook) Name() string { return h.name }

func (h *FuncHook) Events() []HookEvent { return h.events }

func (h *FuncHook) Execute(ctx context.Context, payload *HookPayload) *HookResult {
	return h.fn(ctx, payload)
}

// Runner manages and executes hooks.
type Runner struct {
	mu    sync.RWMutex
	hooks []Hook
}

// NewRunner creates a new hook runner.
func NewRunner() *Runner {
	return &Runner{}
}

// Register adds a hook to the runner.
func (r *Runner) Register(h Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, h)
}

// Run executes all hooks registered for the given event.
// It returns an error if any hook blocks execution (Exit Code 2).
func (r *Runner) Run(
	ctx context.Context,
	event HookEvent,
	meta SessionMeta,
	data map[string]any,
) ([]*HookResult, error) {
	payload := &HookPayload{
		Event:   event,
		Session: meta,
		Data:    data,
	}

	var results []*HookResult

	r.mu.RLock()
	hooks := make([]Hook, len(r.hooks))
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

		if res.Action == HookActionBlock {
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
