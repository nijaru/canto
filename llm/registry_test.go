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
	if caps.Temperature {
		t.Errorf("expected standard-chat to not support temperature by default")
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

func TestRegistryFallbackDefault(t *testing.T) {
	reg := NewRegistry() // Empty registry

	// Model that should match standard chat (default fallback)
	caps := reg.Resolve("gpt-4o")
	if caps.Temperature {
		t.Errorf("expected fallback to default standard chat without temperature")
	}

	// Unknown model should also default to standard chat deterministically with no heuristic
	caps = reg.Resolve("xiaomi/mimo-v2.5-pro")
	if caps.Temperature {
		t.Errorf("expected unrecognized model to resolve to standard chat without temperature")
	}

	// 3. Test global registry pre-registered matches
	RegisterModel(ModelDef{Pattern: "deepseek-r1", Preset: PresetReasoning})
	defer ClearRegistry()

	caps = ResolveCapabilities("deepseek-r1")
	if caps.Temperature {
		t.Errorf("expected pre-registered deepseek-r1 to disable temperature")
	}
	if caps.Reasoning.Kind != ReasoningKindEffort {
		t.Errorf("expected pre-registered deepseek-r1 to have reasoning effort")
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
