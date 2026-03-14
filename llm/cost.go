package llm

import (
	"charm.land/catwalk/pkg/catwalk"
)

// CalculateCost computes the cost of an LLM call based on token counts and model pricing.
func CalculateCost(model catwalk.Model, usage Usage) float64 {
	inputCost := float64(usage.InputTokens) * (model.CostPer1MIn / 1_000_000)
	outputCost := float64(usage.OutputTokens) * (model.CostPer1MOut / 1_000_000)
	return inputCost + outputCost
}

// Budget tracks cost accumulation and enforces limits.
type Budget struct {
	TotalCost float64
	Limit     float64
}

// IsExceeded returns true if the budget limit is reached.
func (b *Budget) IsExceeded() bool {
	return b.Limit > 0 && b.TotalCost >= b.Limit
}

// Add adds a usage record to the budget using model pricing.
func (b *Budget) Add(model catwalk.Model, usage Usage) {
	b.TotalCost += CalculateCost(model, usage)
}
