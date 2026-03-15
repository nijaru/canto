package context

import (
	"context"

	"github.com/nijaru/canto/llm"
)

// EstimateTokens provides a simple token estimation based on character count.
// It uses the heuristic of 1 token ≈ 4 characters.
func EstimateTokens(text string) int {
	return len(text) / 4
}

// EstimateMessagesTokens calculates the total tokens for a slice of messages.
// If provider is not nil, it uses the provider's CountTokens method for more accuracy.
func EstimateMessagesTokens(ctx context.Context, p llm.Provider, model string, messages []llm.Message) int {
	if p != nil {
		count, err := p.CountTokens(ctx, model, messages)
		if err == nil {
			return count
		}
	}

	total := 0
	for _, m := range messages {
		total += EstimateTokens(m.Content)
		for _, call := range m.Calls {
			total += EstimateTokens(call.Function.Name) + EstimateTokens(call.Function.Arguments)
		}
	}
	return total
}

// exceedsThreshold checks if the current token count exceeds the threshold.
func exceedsThreshold(current, max int, thresholdPct float64) bool {
	if max <= 0 {
		return false
	}
	return float64(current) > float64(max)*thresholdPct
}
