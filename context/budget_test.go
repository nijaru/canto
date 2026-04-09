package context_test

import (
	"context"
	"errors"
	"testing"

	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

type mockBudgetProvider struct {
	tokensPerMessage int
}

func (m *mockBudgetProvider) ID() string { return "mock" }
func (m *mockBudgetProvider) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return &llm.Response{}, nil
}

func (m *mockBudgetProvider) Stream(context.Context, *llm.Request) (llm.Stream, error) {
	return nil, nil
}
func (m *mockBudgetProvider) Models(context.Context) ([]llm.Model, error) { return nil, nil }
func (m *mockBudgetProvider) CountTokens(
	ctx context.Context,
	model string,
	messages []llm.Message,
) (int, error) {
	return len(messages) * m.tokensPerMessage, nil
}
func (m *mockBudgetProvider) Cost(context.Context, string, llm.Usage) float64 { return 0 }
func (m *mockBudgetProvider) Capabilities(string) llm.Capabilities {
	return llm.Capabilities{}
}
func (m *mockBudgetProvider) IsTransient(error) bool       { return false }
func (m *mockBudgetProvider) IsContextOverflow(error) bool { return false }

func TestBudgetGuardCheckLevels(t *testing.T) {
	guard := ccontext.NewBudgetGuard(100)

	cases := []struct {
		name     string
		current  int
		pending  int
		expected ccontext.BudgetLevel
	}{
		{name: "ok", current: 50, pending: 0, expected: ccontext.BudgetOK},
		{name: "warning", current: 60, pending: 10, expected: ccontext.BudgetWarning},
		{name: "critical", current: 75, pending: 15, expected: ccontext.BudgetCritical},
		{name: "exceeded", current: 90, pending: 10, expected: ccontext.BudgetExceeded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := guard.Check(tc.current, tc.pending)
			if status.Level != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, status.Level)
			}
		})
	}
}

func TestBudgetGuardCheckNormalizesThresholds(t *testing.T) {
	guard := &ccontext.BudgetGuard{
		MaxTokens:            100,
		WarningThresholdPct:  -1,
		CriticalThresholdPct: 0.2,
	}

	status := guard.Check(75, 0)
	if status.WarningThresholdPct != 0.70 {
		t.Fatalf("expected default warning threshold, got %f", status.WarningThresholdPct)
	}
	if status.CriticalThresholdPct != 0.70 {
		t.Fatalf(
			"expected critical threshold to clamp to warning threshold, got %f",
			status.CriticalThresholdPct,
		)
	}
	if status.Level != ccontext.BudgetCritical {
		t.Fatalf("expected critical after normalization, got %s", status.Level)
	}
}

func TestBudgetGuardApplyRequestReportsWarningWithoutError(t *testing.T) {
	provider := &mockBudgetProvider{tokensPerMessage: 35}
	sess := session.New("warning")
	req := &llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "a"},
			{Role: llm.RoleAssistant, Content: "b"},
		},
	}

	var seen ccontext.BudgetStatus
	guard := ccontext.NewBudgetGuard(100)
	guard.OnStatus = func(status ccontext.BudgetStatus) {
		seen = status
	}

	if err := guard.ApplyRequest(t.Context(), provider, "", sess, req); err != nil {
		t.Fatalf("expected no error on warning threshold, got %v", err)
	}
	if seen.Level != ccontext.BudgetWarning {
		t.Fatalf("expected warning callback, got %s", seen.Level)
	}
	if !seen.NeedsCompaction() {
		t.Fatal("expected warning status to request compaction")
	}
}

func TestBudgetGuardApplyRequestReturnsTypedThresholdError(t *testing.T) {
	provider := &mockBudgetProvider{tokensPerMessage: 45}
	sess := session.New("critical")
	req := &llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "a"},
			{Role: llm.RoleAssistant, Content: "b"},
		},
	}

	guard := ccontext.NewBudgetGuard(100)
	err := guard.ApplyRequest(t.Context(), provider, "", sess, req)
	if err == nil {
		t.Fatal("expected threshold error, got nil")
	}

	var thresholdErr *ccontext.BudgetThresholdError
	if !errors.As(err, &thresholdErr) {
		t.Fatalf("expected BudgetThresholdError, got %T", err)
	}
	if thresholdErr.Status.Level != ccontext.BudgetCritical {
		t.Fatalf("expected critical level, got %s", thresholdErr.Status.Level)
	}
	if !thresholdErr.Status.IsTerminal() {
		t.Fatal("expected critical status to be terminal")
	}
}

func TestBudgetGuardApplyRequestNoLimit(t *testing.T) {
	provider := &mockBudgetProvider{tokensPerMessage: 1000}
	sess := session.New("nolimit")
	req := &llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "huge"}},
	}

	guard := ccontext.NewBudgetGuard(0)
	if err := guard.ApplyRequest(t.Context(), provider, "", sess, req); err != nil {
		t.Fatalf("expected no error with no limit, got %v", err)
	}
}
