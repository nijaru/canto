package agent

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/llm"
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

func (e *BudgetExceededError) BudgetExceeded() bool { return true }

type budgetExceeded interface {
	BudgetExceeded() bool
}

type budgetGuard struct {
	Limit float64
}

func (g *budgetGuard) ApplyRequest(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
	req *llm.Request,
) error {
	if g.Limit <= 0 {
		return nil
	}
	totalCost := sess.TotalCost()
	if totalCost >= g.Limit {
		return &BudgetExceededError{Limit: g.Limit, TotalCost: totalCost}
	}
	return nil
}
