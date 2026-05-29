package approvalaudit

import (
	"bytes"
	"testing"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/ion/approval"
	"github.com/nijaru/ion/audit"
)

func TestLoggerAdaptsApprovalEvents(t *testing.T) {
	var buf bytes.Buffer
	logger := New(audit.NewStreamLogger(&buf))

	err := logger.Log(t.Context(), approval.AuditEvent{
		Kind:      approval.AuditKindToolAllowed,
		SessionID: "session",
		Tool:      "bash",
		Category:  "command",
		Operation: "exec",
		Resource:  "bash",
		Decision:  string(approval.DecisionAllow),
		Reason:    "ok",
		Metadata:  map[string]any{"k": "v"},
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	var event audit.Event
	if err := json.Unmarshal(buf.Bytes(), &event); err != nil {
		t.Fatalf("decode audit event: %v", err)
	}
	if event.Kind != audit.KindToolAllowed {
		t.Fatalf("kind = %q, want %q", event.Kind, audit.KindToolAllowed)
	}
	if event.Tool != "bash" || event.Decision != string(approval.DecisionAllow) {
		t.Fatalf("event = %#v, want adapted approval fields", event)
	}
}
