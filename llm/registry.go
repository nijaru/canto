package llm

import (
	"path/filepath"
	"strings"
	"sync"
)

// ModelPreset defines standard capability profiles.
type ModelPreset string

const (
	PresetChat            ModelPreset = "chat"
	PresetReasoning       ModelPreset = "reasoning"
	PresetOpenAIReasoning ModelPreset = "openai-reasoning"
)

// ModelDef represents a model capability mapping definition.
type ModelDef struct {
	Pattern      string        `json:"pattern"                toml:"pattern"` // glob pattern (e.g. "deepseek-*") or exact name
	Preset       ModelPreset   `json:"preset,omitempty"       toml:"preset,omitempty"`
	Capabilities *Capabilities `json:"capabilities,omitempty" toml:"capabilities,omitempty"`
}

// Registry manages thread-safe resolution of model capabilities.
type Registry struct {
	mu   sync.RWMutex
	defs []ModelDef
}

// NewRegistry creates a new Model Capability Registry.
func NewRegistry() *Registry {
	return &Registry{
		defs: make([]ModelDef, 0),
	}
}

// Clear clears all registered model definitions.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs = r.defs[:0]
}

// Register registers a new model capability definition.
func (r *Registry) Register(def ModelDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs = append(r.defs, def)
}

// Resolve resolves capabilities for a given model ID.
func (r *Registry) Resolve(modelID string) Capabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()

	modelLower := strings.ToLower(strings.TrimSpace(modelID))
	for _, def := range r.defs {
		patternLower := strings.ToLower(strings.TrimSpace(def.Pattern))

		// Try glob matching first
		if matched, err := filepath.Match(patternLower, modelLower); err == nil && matched {
			return r.capabilitiesFromDef(def, modelLower)
		}

		// Fallback to substring matching if the pattern is in the model ID
		if strings.Contains(modelLower, patternLower) {
			return r.capabilitiesFromDef(def, modelLower)
		}
	}

	// Fallback to dynamic naming heuristics
	if isReasoningModelHeuristic(modelLower) {
		return reasoningCapabilitiesHeuristic(modelLower)
	}

	return DefaultCapabilities()
}

func (r *Registry) capabilitiesFromDef(def ModelDef, modelID string) Capabilities {
	if def.Capabilities != nil {
		return *def.Capabilities
	}

	switch def.Preset {
	case PresetChat:
		return DefaultCapabilities()
	case PresetReasoning:
		return Capabilities{
			Streaming:   true,
			Tools:       true,
			Temperature: false,
			SystemRole:  RoleSystem,
			Reasoning: ReasoningCapabilities{
				Kind:       ReasoningKindEffort,
				Efforts:    []string{"minimal", "low", "medium", "high"},
				CanDisable: true,
			},
		}
	case PresetOpenAIReasoning:
		role := RoleSystem
		if strings.Contains(modelID, "o1") {
			role = RoleUser
		} else if strings.Contains(modelID, "o3") || strings.Contains(modelID, "o4") {
			role = RoleDeveloper
		}
		return Capabilities{
			Streaming:   true,
			Tools:       true,
			Temperature: false,
			SystemRole:  role,
			Reasoning: ReasoningCapabilities{
				Kind:       ReasoningKindEffort,
				Efforts:    []string{"minimal", "low", "medium", "high"},
				CanDisable: true,
			},
		}
	default:
		return DefaultCapabilities()
	}
}

// DefaultRegistry is the framework-wide capability registry.
var DefaultRegistry = NewRegistry()

// RegisterModel registers a model capability definition globally.
func RegisterModel(def ModelDef) {
	DefaultRegistry.Register(def)
}

// ResolveCapabilities resolves model capabilities globally.
func ResolveCapabilities(model string) Capabilities {
	return DefaultRegistry.Resolve(model)
}

// ClearRegistry clears all definitions from the global registry.
func ClearRegistry() {
	DefaultRegistry.Clear()
}

// isReasoningModelHeuristic is the fallback dynamic reasoning model naming check.
func isReasoningModelHeuristic(model string) bool {
	if strings.Contains(model, "reasoner") || strings.Contains(model, "reasoning") ||
		strings.Contains(model, "thinking") ||
		strings.Contains(model, "mimo") {
		return true
	}
	segments := strings.FieldsFunc(model, func(r rune) bool {
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

// reasoningCapabilitiesHeuristic builds capability defaults for heuristically-detected reasoning models.
func reasoningCapabilitiesHeuristic(model string) Capabilities {
	role := RoleSystem
	segments := strings.FieldsFunc(model, func(r rune) bool {
		return r == '/' || r == ':' || r == '-' || r == '_' || r == '.'
	})
	for _, seg := range segments {
		if seg == "o1" {
			role = RoleUser
			break
		} else if seg == "o3" || seg == "o4" {
			role = RoleDeveloper
			break
		}
	}
	return Capabilities{
		Streaming:  true,
		Tools:      true,
		SystemRole: role,
		Reasoning: ReasoningCapabilities{
			Kind:       ReasoningKindEffort,
			Efforts:    []string{"minimal", "low", "medium", "high"},
			CanDisable: true,
		},
	}
}
