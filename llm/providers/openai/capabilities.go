package openai

import (
	"strings"

	"github.com/nijaru/canto/llm"
)

// Capabilities returns the feature set for the given model.
// It consults ModelCaps first; unknown models get DefaultCapabilities.
func (b *Base) Capabilities(model string) llm.Capabilities {
	if b.ModelCaps != nil {
		if caps, ok := b.ModelCaps[model]; ok {
			return caps
		}
	}
	if isReasoningModel(model) {
		return reasoningCapabilitiesForModel(model)
	}
	return llm.DefaultCapabilities()
}

func isReasoningModel(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "mimo") ||
		strings.Contains(m, "o1-") ||
		strings.Contains(m, "o3-") ||
		strings.Contains(m, "o4-") ||
		strings.Contains(m, "deepseek-reasoner") ||
		strings.Contains(m, "deepseek-r1") ||
		strings.Contains(m, "r1-") ||
		strings.Contains(m, "reasoner") ||
		strings.Contains(m, "reasoning") ||
		m == "o1" || m == "o3" || m == "o4" ||
		strings.HasPrefix(m, "o1/") || strings.HasPrefix(m, "o3/") || strings.HasPrefix(m, "o4/") ||
		strings.HasSuffix(m, "/o1") || strings.HasSuffix(m, "/o3") || strings.HasSuffix(m, "/o4") ||
		strings.Contains(m, "/o1-") || strings.Contains(m, "/o3-") || strings.Contains(m, "/o4-")
}

func reasoningCapabilitiesForModel(model string) llm.Capabilities {
	m := strings.ToLower(model)
	role := llm.RoleSystem
	if strings.Contains(m, "o1") {
		role = llm.RoleUser
	} else if strings.Contains(m, "o3") || strings.Contains(m, "o4") {
		role = llm.RoleDeveloper
	}
	return llm.Capabilities{
		Streaming:  true,
		Tools:      true,
		SystemRole: role,
		Reasoning: llm.ReasoningCapabilities{
			Kind:       llm.ReasoningKindEffort,
			Efforts:    []string{"minimal", "low", "medium", "high"},
			CanDisable: true,
		},
	}
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
