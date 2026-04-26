package governor

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
	"github.com/nijaru/canto/session"
)

// BudgetExceededError reports that a request should not proceed because the
// session has already consumed the configured budget.
type BudgetExceededError struct {
	Limit     float64
	TotalCost float64
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("budget exceeded: %.4f >= %.4f", e.TotalCost, e.Limit)
}

// TokenGuard ensures the LLM request doesn't exceed the token budget.
// It also detects if the context is nearing the "rot threshold" (default 60%).
type TokenGuard struct {
	MaxTokens       int
	RotThresholdPct float64
}

// NewTokenGuard creates a new token guard processor.
func NewTokenGuard(maxTokens int) *TokenGuard {
	return &TokenGuard{
		MaxTokens:       maxTokens,
		RotThresholdPct: 0.60,
	}
}

func (p *TokenGuard) ApplyRequest(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	// 1. Calculate current token usage
	currentTokens := prompt.EstimateMessagesTokens(ctx, pr, model, req.Messages)

	// 2. Check against budget
	if p.MaxTokens > 0 && currentTokens > p.MaxTokens {
		return fmt.Errorf("token budget exceeded: %d > %d", currentTokens, p.MaxTokens)
	}

	return nil
}

// BudgetGuard checks if the session's total cost has exceeded the budget.
type BudgetGuard struct {
	Limit float64
}

// NewBudgetGuard creates a new budget guard.
func NewBudgetGuard(limit float64) *BudgetGuard {
	return &BudgetGuard{Limit: limit}
}

func (p *BudgetGuard) ApplyRequest(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	if p.Limit <= 0 {
		return nil
	}

	totalCost := sess.TotalCost()

	if totalCost >= p.Limit {
		return &BudgetExceededError{Limit: p.Limit, TotalCost: totalCost}
	}

	return nil
}

// CircuitBreakerGuard injects a warning into the prompt if the approval gate
// is in a tripped state (automated approvals disabled).
type CircuitBreakerGuard struct {
	Manager *approval.Gate
}

// NewCircuitBreakerGuard creates a new circuit breaker guard.
func NewCircuitBreakerGuard(mgr *approval.Gate) *CircuitBreakerGuard {
	return &CircuitBreakerGuard{Manager: mgr}
}

func (p *CircuitBreakerGuard) ApplyRequest(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	if p.Manager == nil || !p.Manager.IsTripped() {
		return nil
	}

	hint := "Notice: Automated tool approvals are currently disabled due to repeated safety denials. " +
		"Every subsequent tool call will require manual human approval until the agent demonstrates safe behavior."

	return prompt.Instructions(hint).ApplyRequest(ctx, pr, model, sess, req)
}
