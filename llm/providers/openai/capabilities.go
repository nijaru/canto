package openai

import "github.com/nijaru/canto/llm"

// Capabilities returns the feature set for the given model.
// It consults ModelCaps first; unknown models get DefaultCapabilities.
func (b *Base) Capabilities(model string) llm.Capabilities {
	if b.ModelCaps != nil {
		if caps, ok := b.ModelCaps[model]; ok {
			return caps
		}
	}
	return llm.DefaultCapabilities()
}

// DefaultModelCaps returns capability entries for well-known OpenAI reasoning
// models. Pass to Base.ModelCaps (or merge with your own overrides) when
// constructing a provider that will use these models.
func DefaultModelCaps() map[string]llm.Capabilities {
	reasoning := func(systemRole llm.Role) llm.Capabilities {
		return llm.Capabilities{
			Streaming:  true,
			Tools:      true,
			SystemRole: systemRole,
			Reasoning: llm.ReasoningCapabilities{
				Kind:       llm.ReasoningKindEffort,
				Efforts:    []string{"minimal", "low", "medium", "high"},
				CanDisable: true,
			},
			// Temperature is false (zero value) — reasoning models ignore it.
		}
	}
	return map[string]llm.Capabilities{
		// o1 family: no system role — instructions become user messages.
		"o1":         reasoning(llm.RoleUser),
		"o1-mini":    reasoning(llm.RoleUser),
		"o1-preview": reasoning(llm.RoleUser),
		// o3/o4 families: privileged instruction role.
		"o3":      reasoning(llm.RoleDeveloper),
		"o3-mini": reasoning(llm.RoleDeveloper),
		"o3-pro":  reasoning(llm.RoleDeveloper),
		"o4-mini": reasoning(llm.RoleDeveloper),
	}
}
