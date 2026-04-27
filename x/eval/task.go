package eval

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Task defines a repeatable evaluation scenario.
type Task interface {
	ID() string
	Instruction() string
	Environment() Environment
}

// Environment prepares a task-specific session before the agent runs.
type Environment interface {
	ID() string
	Bootstrap(ctx context.Context, sess *session.Session) error
}

// TaskSpec is the default concrete Task implementation.
type TaskSpec struct {
	TaskID          string
	InstructionText string
	Env             Environment
}

// ID returns the task identifier.
func (t TaskSpec) ID() string { return t.TaskID }

// Instruction returns the task instruction.
func (t TaskSpec) Instruction() string { return t.InstructionText }

// Environment returns the task environment.
func (t TaskSpec) Environment() Environment { return t.Env }

// StaticEnvironment is a simple Environment that appends fixed context and
// optional transcript seed messages.
type StaticEnvironment struct {
	EnvironmentID string
	Context       []session.ContextEntry
	Transcript    []llm.Message
}

// ID returns the environment identifier.
func (e StaticEnvironment) ID() string { return e.EnvironmentID }

// Bootstrap appends the environment's context and transcript seeds to the session.
func (e StaticEnvironment) Bootstrap(ctx context.Context, sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("eval: nil session")
	}

	for _, entry := range e.Context {
		if entry.Kind == "" {
			entry.Kind = session.ContextKindHarness
		}
		if err := sess.AppendContext(ctx, entry); err != nil {
			return err
		}
	}
	for _, msg := range e.Transcript {
		if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.MessageAdded, msg)); err != nil {
			return err
		}
	}
	return nil
}
