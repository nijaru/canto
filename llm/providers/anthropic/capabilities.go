package anthropic

import "github.com/nijaru/canto/llm"

// DefaultModelCaps returns capability entries for Anthropic models that
// support extended thinking. Merge with your own overrides as needed.
func DefaultModelCaps() map[string]llm.Capabilities {
	thinking := func() llm.Capabilities {
		c := llm.DefaultCapabilities()
		c.Reasoning = llm.ReasoningCapabilities{
			Kind:            llm.ReasoningKindBudget,
			BudgetMinTokens: 1024,
		}
		return c
	}
	return map[string]llm.Capabilities{
		"claude-3-7-sonnet-20250219": thinking(),
		"claude-opus-4-5":            thinking(),
		"claude-sonnet-4-5":          thinking(),
		"claude-opus-4-20250514":     thinking(),
		"claude-sonnet-4-20250514":   thinking(),
	}
}

// Capabilities returns the feature set for the given model.
// It consults the model caps map first; unknown models get DefaultCapabilities.
func (p *Provider) Capabilities(model string) llm.Capabilities {
	if p.modelCaps != nil {
		if caps, ok := p.modelCaps[model]; ok {
			return caps
		}
	}
	return llm.DefaultCapabilities()
}
