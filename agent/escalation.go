package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

type escalationError struct {
	scope       string
	target      string
	message     string
	recoverable bool
	cause       error
	toolMessage *llm.Message
}

func (e *escalationError) Error() string {
	if e == nil {
		return ""
	}
	if e.message != "" {
		return e.message
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	return "escalation"
}

func (e *escalationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func classifyStepError(err error, provider llm.Provider) *escalationError {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}

	var stepErr *escalationError
	if errors.As(err, &stepErr) {
		return stepErr
	}

	if provider != nil && provider.IsTransient(err) {
		return &escalationError{
			scope:       "model",
			message:     err.Error(),
			recoverable: true,
			cause:       err,
		}
	}

	return nil
}

func recordEscalationRetry(
	ctx context.Context,
	s *session.Session,
	agentID string,
	attempt int,
	escalation *escalationError,
) error {
	if escalation == nil {
		return nil
	}
	return s.Append(ctx, session.NewEscalationRetriedEvent(s.ID(), session.EscalationRetriedData{
		AgentID: agentID,
		Scope:   escalation.scope,
		Target:  escalation.target,
		Attempt: attempt,
		Error:   escalation.Error(),
	}))
}

func appendWithheldToolMessage(ctx context.Context, s *session.Session, escalation *escalationError) error {
	if escalation == nil || escalation.toolMessage == nil {
		return nil
	}
	return s.Append(ctx, session.NewEvent(s.ID(), session.MessageAdded, *escalation.toolMessage))
}

func hardEscalationError(escalation *escalationError, attempts int) error {
	if escalation == nil {
		return nil
	}
	return fmt.Errorf("escalation exhausted after %d attempts: %w", attempts, escalation)
}
