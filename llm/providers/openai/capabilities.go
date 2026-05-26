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
	if strings.Contains(m, "reasoner") || strings.Contains(m, "reasoning") ||
		strings.Contains(m, "thinking") ||
		strings.Contains(m, "mimo") {
		return true
	}
	segments := strings.FieldsFunc(m, func(r rune) bool {
		return r == '/' || r == ':' || r == '-' || r == '_' || r == '.'
	})
	for _, seg := range segments {
		if seg == "o1" || seg == "o3" || seg == "o4" {
			return true
		}
		if len(seg) >= 2 && seg[0] == 'r' && isDigits(seg[1:]) {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func reasoningCapabilitiesForModel(model string) llm.Capabilities {
	m := strings.ToLower(model)
	role := llm.RoleSystem
	segments := strings.FieldsFunc(m, func(r rune) bool {
		return r == '/' || r == ':' || r == '-' || r == '_' || r == '.'
	})
	for _, seg := range segments {
		if seg == "o1" {
			role = llm.RoleUser
			break
		} else if seg == "o3" || seg == "o4" {
			role = llm.RoleDeveloper
			break
		}
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
