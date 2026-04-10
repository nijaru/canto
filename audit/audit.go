package audit

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-json-experiment/json"
)

const (
	KindApprovalRequested    = "security.approval.requested"
	KindApprovalResolved     = "security.approval.resolved"
	KindApprovalCanceled     = "security.approval.canceled"
	KindPolicyAllowed        = "security.policy.allowed"
	KindPolicyDenied         = "security.policy.denied"
	KindPolicyDeferred       = "security.policy.deferred"
	KindToolAllowed          = "security.tool.allowed"
	KindToolDenied           = "security.tool.denied"
	KindSandboxEscapeAttempt = "security.sandbox.escape_attempt"
	KindSandboxWrapFailed    = "security.sandbox.wrap_failed"
	KindEnvSanitized         = "security.env.sanitized"
	KindProtectedPathBlocked = "security.path.blocked"
)

// Event is one structured security fact in the append-only audit log.
type Event struct {
	Time      time.Time      `json:"time"`
	Kind      string         `json:"kind"`
	SessionID string         `json:"session_id,omitzero"`
	AgentID   string         `json:"agent_id,omitzero"`
	Tool      string         `json:"tool,omitzero"`
	Category  string         `json:"category,omitzero"`
	Operation string         `json:"operation,omitzero"`
	Resource  string         `json:"resource,omitzero"`
	Decision  string         `json:"decision,omitzero"`
	Reason    string         `json:"reason,omitzero"`
	Error     string         `json:"error,omitzero"`
	Metadata  map[string]any `json:"metadata,omitzero"`
}

// Logger appends security events to durable storage.
type Logger interface {
	Log(ctx context.Context, event Event) error
}

// WriterLogger writes audit events as JSONL to an arbitrary writer.
type WriterLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewWriterLogger wraps w with a Logger implementation.
func NewWriterLogger(w io.Writer) *WriterLogger {
	return &WriterLogger{w: w}
}

// Log appends event as one JSON line.
func (l *WriterLogger) Log(ctx context.Context, event Event) error {
	if l == nil || l.w == nil {
		return errors.New("audit logger is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	if len(event.Metadata) > 0 {
		event.Metadata = cloneMetadata(event.Metadata)
	}

	b, err := json.Marshal(event)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	_, err = l.w.Write(append(b, '\n'))
	return err
}

// JSONLLogger writes audit events to a JSONL file.
type JSONLLogger struct {
	*WriterLogger
	closer io.Closer
}

// NewJSONLLogger opens path for append-only audit logging.
func NewJSONLLogger(path string) (*JSONLLogger, error) {
	if path == "" {
		return nil, errors.New("audit logger path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &JSONLLogger{
		WriterLogger: NewWriterLogger(f),
		closer:       f,
	}, nil
}

// Close closes the underlying log file.
func (l *JSONLLogger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	return l.closer.Close()
}

func cloneMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
