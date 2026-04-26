package governor_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

type mockGuardProvider struct {
	TokensPerMessage int
}

func (m *mockGuardProvider) ID() string { return "mock" }
func (m *mockGuardProvider) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return &llm.Response{}, nil
}

func (m *mockGuardProvider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return nil, nil
}
func (m *mockGuardProvider) Models(ctx context.Context) ([]llm.Model, error) { return nil, nil }

func (m *mockGuardProvider) CountTokens(
	ctx context.Context,
	model string,
	messages []llm.Message,
) (int, error) {
	return len(messages) * m.TokensPerMessage, nil
}

func (m *mockGuardProvider) Cost(ctx context.Context, model string, usage llm.Usage) float64 {
	return 0
}

func (m *mockGuardProvider) Capabilities(model string) llm.Capabilities {
	return llm.Capabilities{}
}
func (m *mockGuardProvider) IsTransient(err error) bool       { return false }
func (m *mockGuardProvider) IsContextOverflow(err error) bool { return false }

func TestTokenGuard(t *testing.T) {
	ctx := context.Background()
	pr := &mockGuardProvider{TokensPerMessage: 10}
	sess := session.New("test-session")

	req := &llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Hello"},
			{Role: llm.RoleAssistant, Content: "Hi"},
		},
	}

	guard := governor.NewTokenGuard(10000)
	if err := guard.ApplyRequest(ctx, pr, "", sess, req); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestTokenGuardExceeded(t *testing.T) {
	ctx := context.Background()
	pr := &mockGuardProvider{TokensPerMessage: 10}
	sess := session.New("test-session")

	req := &llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Hello"},
			{Role: llm.RoleAssistant, Content: "Hi"},
		},
	}

	guard := governor.NewTokenGuard(1) // 1 token max — trivially exceeded
	err := guard.ApplyRequest(ctx, pr, "", sess, req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "token budget exceeded") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestTokenGuardNoLimit(t *testing.T) {
	ctx := context.Background()
	pr := &mockGuardProvider{TokensPerMessage: 10}
	sess := session.New("test-session")

	req := &llm.Request{}
	guard := governor.NewTokenGuard(0)
	if err := guard.ApplyRequest(ctx, pr, "", sess, req); err != nil {
		t.Fatalf("expected no error with no limit, got: %v", err)
	}
}

func TestBudgetGuard(t *testing.T) {
	ctx := context.Background()
	pr := &mockGuardProvider{}
	sess := session.New("test-session")

	e := session.NewEvent(sess.ID(), session.MessageAdded, nil)
	e.Cost = 5.0
	_ = sess.Append(ctx, e)

	guard := governor.NewBudgetGuard(10.0)
	if err := guard.ApplyRequest(ctx, pr, "", sess, &llm.Request{}); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestBudgetGuardExceeded(t *testing.T) {
	ctx := context.Background()
	pr := &mockGuardProvider{}
	sess := session.New("test-session")

	e := session.NewEvent(sess.ID(), session.MessageAdded, nil)
	e.Cost = 5.0
	_ = sess.Append(ctx, e)

	guard := governor.NewBudgetGuard(1.0)
	err := guard.ApplyRequest(ctx, pr, "", sess, &llm.Request{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "budget exceeded") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBudgetGuardNoLimit(t *testing.T) {
	ctx := context.Background()
	pr := &mockGuardProvider{}
	sess := session.New("test-session")

	e := session.NewEvent(sess.ID(), session.MessageAdded, nil)
	e.Cost = 5.0
	_ = sess.Append(ctx, e)

	guard := governor.NewBudgetGuard(0)
	if err := guard.ApplyRequest(ctx, pr, "", sess, &llm.Request{}); err != nil {
		t.Fatalf("expected no error with no limit, got: %v", err)
	}
}

func TestBudgetGuardExactLimit(t *testing.T) {
	ctx := context.Background()
	pr := &mockGuardProvider{}
	sess := session.New("test-session")

	e := session.NewEvent(sess.ID(), session.MessageAdded, nil)
	e.Cost = 1.0
	_ = sess.Append(ctx, e)

	guard := governor.NewBudgetGuard(1.0)
	err := guard.ApplyRequest(ctx, pr, "", sess, &llm.Request{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCircuitBreakerGuard(t *testing.T) {
	ctx := context.Background()
	sess := session.New("test")
	pr := &mockGuardProvider{}

	policy := &mockDenyPolicy{}
	mgr := approval.NewGate(policy).WithThreshold(1)

	// 1. Trip the breaker
	_, _ = mgr.Request(ctx, sess, "t1", "{}", approval.Requirement{Category: "cat"})

	if !mgr.IsTripped() {
		t.Fatal("expected breaker to be tripped")
	}

	// 2. Test guard injection
	guard := governor.NewCircuitBreakerGuard(mgr)
	req := &llm.Request{}
	if err := guard.ApplyRequest(ctx, pr, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}

	if len(req.Messages) != 1 || req.Messages[0].Role != llm.RoleSystem {
		t.Fatalf("expected 1 system message, got %d", len(req.Messages))
	}
	if !strings.Contains(
		req.Messages[0].Content,
		"Automated tool approvals are currently disabled",
	) {
		t.Errorf("unexpected content: %s", req.Messages[0].Content)
	}

	// 3. Test untripped guard
	mgr.ResetBreaker()
	req = &llm.Request{}
	if err := guard.ApplyRequest(ctx, pr, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}
	if len(req.Messages) != 0 {
		t.Fatalf("expected no messages when not tripped, got %d", len(req.Messages))
	}
}

type mockDenyPolicy struct{}

func (p *mockDenyPolicy) Decide(
	ctx context.Context,
	sess *session.Session,
	req approval.Request,
) (approval.Result, bool, error) {
	return approval.Result{Decision: approval.DecisionDeny}, true, nil
}
