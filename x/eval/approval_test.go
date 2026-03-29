package eval

import (
	"context"
	"testing"
	"time"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/session"
)

func TestEvaluateApprovalCases(t *testing.T) {
	manager := approval.NewManager(nil)
	sess := session.New("approval-eval")

	results := EvaluateApprovalCases(t.Context(), manager, sess, []ApprovalCase{
		{
			Name: "allow request",
			Run: func(ctx context.Context, manager *approval.Manager, sess *session.Session) error {
				done := make(chan error, 1)
				go func() {
					_, err := manager.Request(ctx, sess, "bash", "{}", approval.Requirement{
						Category:  "command",
						Operation: "exec",
						Resource:  "echo hi",
					})
					done <- err
				}()
				time.Sleep(10 * time.Millisecond)
				pending := manager.Pending()
				if len(pending) != 1 {
					return context.DeadlineExceeded
				}
				if err := manager.Resolve(pending[0], approval.DecisionAllow, "ok"); err != nil {
					return err
				}
				return <-done
			},
		},
		{
			Name: "deny request",
			Run: func(ctx context.Context, manager *approval.Manager, sess *session.Session) error {
				done := make(chan error, 1)
				go func() {
					res, err := manager.Request(ctx, sess, "write_file", "{}", approval.Requirement{
						Category:  "workspace",
						Operation: "write_file",
						Resource:  "a.txt",
					})
					if err != nil {
						done <- err
						return
					}
					done <- res.Error()
				}()
				time.Sleep(10 * time.Millisecond)
				pending := manager.Pending()
				if len(pending) != 1 {
					return context.DeadlineExceeded
				}
				if err := manager.Resolve(pending[0], approval.DecisionDeny, "unsafe"); err != nil {
					return err
				}
				return <-done
			},
			ExpectError: "approval denied: unsafe",
		},
	})
	if len(results) != 2 || !results[0].Passed || !results[1].Passed {
		t.Fatalf("unexpected approval eval results: %#v", results)
	}
}
