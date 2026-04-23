package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func TestTurnStateHandleStepResultAccumulatesUsageAndStopReason(t *testing.T) {
	s := userSession("ts-result", "hello")
	state := turnState{}
	outcome := state.handleStepResult(s, StepResult{
		Usage: llm.Usage{
			InputTokens:         10,
			OutputTokens:        4,
			TotalTokens:         14,
			CacheReadTokens:     2,
			CacheCreationTokens: 3,
			Cost:                1.5,
		},
	}, 3)

	if state.steps != 1 {
		t.Fatalf("steps = %d, want 1", state.steps)
	}
	if state.totalUsage.TotalTokens != 14 || state.totalUsage.CacheReadTokens != 2 ||
		state.totalUsage.Cost != 1.5 {
		t.Fatalf("unexpected total usage: %#v", state.totalUsage)
	}
	if outcome.result.TurnStopReason != TurnStopCompleted {
		t.Fatalf("turn stop reason = %q, want completed", outcome.result.TurnStopReason)
	}
	if !outcome.stop {
		t.Fatalf("expected completed step to stop")
	}
}

func TestTurnStateHandleStepErrorBudgetStopsWithoutError(t *testing.T) {
	state := turnState{
		totalUsage: llm.Usage{TotalTokens: 42},
	}
	outcome := state.handleStepError(
		context.Background(),
		userSession("ts-budget", "hello"),
		"agent",
		nil,
		2,
		&governor.BudgetExceededError{},
	)

	if outcome.err != nil {
		t.Fatalf("unexpected err: %v", outcome.err)
	}
	if outcome.result.TurnStopReason != TurnStopBudgetExhausted {
		t.Fatalf("turn stop reason = %q", outcome.result.TurnStopReason)
	}
	if outcome.result.Usage.TotalTokens != 42 {
		t.Fatalf("usage = %#v", outcome.result.Usage)
	}
}

func TestTurnStateHandleStepErrorRetriesRecoverableEscalation(t *testing.T) {
	s := userSession("ts-retry", "hello")
	provider := &retryClassifyingProvider{}
	state := turnState{}
	outcome := state.handleStepError(
		context.Background(),
		s,
		"agent",
		provider,
		2,
		errors.New("transient"),
	)

	if !outcome.retry {
		t.Fatalf("expected retry outcome")
	}
	if state.escalations != 1 {
		t.Fatalf("escalations = %d, want 1", state.escalations)
	}
	var retries int
	for _, e := range s.Events() {
		if e.Type == session.EscalationRetried {
			retries++
		}
	}
	if retries != 1 {
		t.Fatalf("retry events = %d, want 1", retries)
	}
}

func TestFinalizeTurnResultUsesLastAssistantMessage(t *testing.T) {
	s := userSession("ts-final", "hello")
	if err := s.Append(context.Background(), session.NewEvent(s.ID(), session.MessageAdded, llm.Message{
		Role:    llm.RoleAssistant,
		Content: "final answer",
	})); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	got := finalizeTurnResult(s, turnState{
		steps:      1,
		totalUsage: llm.Usage{TotalTokens: 9},
		stopReason: TurnStopCompleted,
	}, StepResult{})

	if got.Content != "final answer" {
		t.Fatalf("content = %q, want final answer", got.Content)
	}
	if got.Usage.TotalTokens != 9 {
		t.Fatalf("usage = %#v", got.Usage)
	}
	if got.TurnStopReason != TurnStopCompleted {
		t.Fatalf("turn stop = %q", got.TurnStopReason)
	}
}

type retryClassifyingProvider struct{}

func (p *retryClassifyingProvider) ID() string { return "retry" }

func (p *retryClassifyingProvider) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return nil, nil
}

func (p *retryClassifyingProvider) Stream(context.Context, *llm.Request) (llm.Stream, error) {
	return nil, nil
}

func (p *retryClassifyingProvider) Models(context.Context) ([]llm.Model, error) { return nil, nil }

func (p *retryClassifyingProvider) CountTokens(
	context.Context,
	string,
	[]llm.Message,
) (int, error) {
	return 0, nil
}

func (p *retryClassifyingProvider) Cost(context.Context, string, llm.Usage) float64 { return 0 }

func (p *retryClassifyingProvider) Capabilities(string) llm.Capabilities {
	return llm.DefaultCapabilities()
}

func (p *retryClassifyingProvider) IsTransient(error) bool { return true }

func (p *retryClassifyingProvider) IsContextOverflow(error) bool { return false }
