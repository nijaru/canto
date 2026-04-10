package approval

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/audit"
	"github.com/nijaru/canto/session"
)

func TestManager_LogsAuditEvents(t *testing.T) {
	var buf bytes.Buffer
	mgr := NewManager(nil).WithAuditLogger(audit.NewWriterLogger(&buf))
	sess := session.New("audit")

	done := make(chan Result, 1)
	go func() {
		res, err := mgr.Request(context.Background(), sess, "bash", "{}", Requirement{
			Category:  "command",
			Operation: "exec",
			Resource:  "bash",
		})
		if err != nil {
			t.Errorf("Request: %v", err)
			return
		}
		done <- res
	}()

	time.Sleep(10 * time.Millisecond)
	pending := mgr.Pending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending request, got %d", len(pending))
	}
	if err := mgr.Resolve(pending[0], DecisionAllow, "ok"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-done:
		if !res.Allowed() {
			t.Fatalf("expected allowed result, got %#v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resolution")
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d", len(lines))
	}

	var first, second audit.Event
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first audit event: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("decode second audit event: %v", err)
	}
	if first.Kind != audit.KindApprovalRequested {
		t.Fatalf("first audit kind = %q, want %q", first.Kind, audit.KindApprovalRequested)
	}
	if second.Kind != audit.KindToolAllowed {
		t.Fatalf("second audit kind = %q, want %q", second.Kind, audit.KindToolAllowed)
	}
}
