package approvalaudit

import (
	"context"

	"github.com/nijaru/ion/approval"
	"github.com/nijaru/ion/audit"
)

// Logger adapts approval audit events to the generic audit logger.
type Logger struct {
	logger audit.Logger
}

// New wraps logger for use with approval.Gate.WithAuditLogger.
func New(logger audit.Logger) *Logger {
	return &Logger{logger: logger}
}

func (l *Logger) Log(ctx context.Context, event approval.AuditEvent) error {
	if l == nil || l.logger == nil {
		return nil
	}
	return l.logger.Log(ctx, audit.Event{
		Kind:      event.Kind,
		SessionID: event.SessionID,
		Tool:      event.Tool,
		Category:  event.Category,
		Operation: event.Operation,
		Resource:  event.Resource,
		Decision:  event.Decision,
		Reason:    event.Reason,
		Metadata:  event.Metadata,
	})
}
