package llm

import (
	"testing"
)

func TestRegistryPresets(t *testing.T) {
	reg := NewRegistry()

	// 1. Test Chat Preset
	reg.Register(ModelDef{
		Pattern: "standard-chat-*",
		Preset:  PresetChat,
	})

	caps := reg.Resolve("standard-chat-gpt-4o")
	if !caps.Temperature {
		t.Errorf("expected standard-chat to support temperature")
	}
	if caps.SystemRole != RoleSystem {
		t.Errorf("expected standard-chat system role to be RoleSystem, got %s", caps.SystemRole)
	}
	if caps.Reasoning.Kind != ReasoningKindNone {
		t.Errorf("expected standard-chat to have no reasoning, got %s", caps.Reasoning.Kind)
	}

	// 2. Test Reasoning Preset
	reg.Register(ModelDef{
		Pattern: "deepseek-*",
		Preset:  PresetReasoning,
	})

	caps = reg.Resolve("deepseek-r1-model")
	if caps.Temperature {
		t.Errorf("expected reasoning model to disable temperature")
	}
	if caps.SystemRole != RoleSystem {
		t.Errorf("expected reasoning model system role to be RoleSystem, got %s", caps.SystemRole)
	}
	if caps.Reasoning.Kind != ReasoningKindEffort {
		t.Errorf(
			"expected reasoning model to have reasoning effort kind, got %s",
			caps.Reasoning.Kind,
		)
	}

	// 3. Test OpenAI Reasoning Preset (o1/o3/o4)
	reg.Register(ModelDef{
		Pattern: "o3-*",
		Preset:  PresetOpenAIReasoning,
	})

	caps = reg.Resolve("o3-mini-2025")
	if caps.Temperature {
		t.Errorf("expected o3 reasoning model to disable temperature")
	}
	if caps.SystemRole != RoleDeveloper {
		t.Errorf(
			"expected o3 reasoning model system role to be RoleDeveloper, got %s",
			caps.SystemRole,
		)
	}
}

func TestRegistryCustomCapabilities(t *testing.T) {
	reg := NewRegistry()

	customCaps := Capabilities{
		Streaming:   true,
		Tools:       false,
		Temperature: false,
		SystemRole:  RoleUser,
	}

	reg.Register(ModelDef{
		Pattern:      "custom-override-*",
		Capabilities: &customCaps,
	})

	caps := reg.Resolve("custom-override-model")
	if !caps.Streaming {
		t.Errorf("expected Streaming to be true")
	}
	if caps.Tools {
		t.Errorf("expected Tools to be false")
	}
	if caps.Temperature {
		t.Errorf("expected Temperature to be false")
	}
	if caps.SystemRole != RoleUser {
		t.Errorf("expected SystemRole to be RoleUser, got %s", caps.SystemRole)
	}
}

func TestRegistryFallbackHeuristic(t *testing.T) {
	reg := NewRegistry() // Empty registry, should fall back to heuristic

	// Model that should match standard chat (no reasoning keywords)
	caps := reg.Resolve("gpt-4o")
	if !caps.Temperature {
		t.Errorf("expected fallback to default standard chat with temperature")
	}

	// Model that contains reasoning indicators
	caps = reg.Resolve("xiaomi/mimo-v2.5-pro")
	if caps.Temperature {
		t.Errorf("expected mimo model to fall back to reasoning caps")
	}
	if caps.Reasoning.Kind != ReasoningKindEffort {
		t.Errorf("expected mimo model to support reasoning effort")
	}
}

func TestSubstringMatching(t *testing.T) {
	reg := NewRegistry()

	reg.Register(ModelDef{
		Pattern: "mimo",
		Preset:  PresetReasoning,
	})

	caps := reg.Resolve("some-provider/mimo-model-v2")
	if caps.Temperature {
		t.Errorf("expected substring pattern match to disable temperature")
	}
}
