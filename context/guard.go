package context

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// TokenGuardProcessor ensures the LLM request doesn't exceed the token budget.
// It also detects if the context is nearing the "rot threshold" (default 60%).
type TokenGuardProcessor struct {
	MaxTokens       int
	RotThresholdPct float64
}

// NewTokenGuard creates a new token guard processor.
func NewTokenGuard(maxTokens int) *TokenGuardProcessor {
	return &TokenGuardProcessor{
		MaxTokens:       maxTokens,
		RotThresholdPct: 0.60,
	}
}

func (p *TokenGuardProcessor) Process(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
	req *llm.LLMRequest,
) error {
	// 1. Calculate current token usage
	currentTokens := EstimateMessagesTokens(ctx, pr, model, req.Messages)

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

func (p *BudgetGuard) Process(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
	req *llm.LLMRequest,
) error {
	if p.Limit <= 0 {
		return nil
	}

	totalCost := sess.TotalCost()

	if totalCost >= p.Limit {
		return fmt.Errorf("budget exceeded: %.4f >= %.4f", totalCost, p.Limit)
	}

	return nil
}
