package skill

import (
	"context"
	"strings"
	"testing"

	agentskills "github.com/nijaru/agentskills"
	"github.com/nijaru/ion/llm"
	"github.com/nijaru/ion/session"
	"github.com/nijaru/ion/tool"
)

type runtimeConfigTool struct{ name string }

func (t *runtimeConfigTool) Spec() llm.Spec {
	return llm.Spec{Name: t.name}
}

func (t *runtimeConfigTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

func TestRuntimeConfigWithoutSkillsReturnsRegistry(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&runtimeConfigTool{name: "alpha"})

	cfg, err := RuntimeConfig(t.Context(), reg)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if cfg.Tools != reg {
		t.Fatalf("runtime tools = %#v, want original registry", cfg.Tools)
	}
	if len(cfg.RequestProcessors) != 0 {
		t.Fatalf("request processors = %d, want 0", len(cfg.RequestProcessors))
	}
}

func TestRuntimeConfigPreloadsSkillsAndScopesTools(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&runtimeConfigTool{name: "alpha"})
	reg.Register(&runtimeConfigTool{name: "beta"})

	cfg, err := RuntimeConfig(t.Context(), reg, &agentskills.Skill{
		Name:         "debug",
		Description:  "Debugging workflow",
		Instructions: "Follow the debugger checklist.",
		AllowedTools: agentskills.ToolList{"beta"},
	})
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if got := cfg.Tools.Names(); len(got) != 1 || got[0] != "beta" {
		t.Fatalf("runtime tools = %#v, want [beta]", got)
	}
	if len(cfg.RequestProcessors) != 1 {
		t.Fatalf("request processors = %d, want 1", len(cfg.RequestProcessors))
	}

	req := &llm.Request{}
	if err := cfg.RequestProcessors[0].ApplyRequest(
		t.Context(),
		nil,
		"",
		&session.Session{},
		req,
	); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}
	if len(req.Messages) == 0 {
		t.Fatal("expected injected system message")
	}
	system := req.Messages[0].Content
	if !strings.Contains(system, "# Skill: debug") ||
		!strings.Contains(system, "Follow the debugger checklist.") {
		t.Fatalf("preloaded skill prompt missing expected content: %q", system)
	}
}

func TestRuntimeConfigFailsClosedForRestrictedSkillsWithoutRegistry(t *testing.T) {
	_, err := RuntimeConfig(t.Context(), nil, &agentskills.Skill{
		Name:         "debug",
		Description:  "Debugging workflow",
		Instructions: "Follow the debugger checklist.",
		AllowedTools: agentskills.ToolList{"beta"},
	})
	if err == nil {
		t.Fatal("expected restricted skill runtime config to fail without registry")
	}
}
