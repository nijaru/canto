package skill

import (
	"context"
	"testing"

	agentskills "github.com/nijaru/agentskills"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

type securityTool struct{ name string }

func (t *securityTool) Spec() llm.Spec {
	return llm.Spec{Name: t.name}
}

func (t *securityTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

func TestSecurityHooks_ScopeRegistryFromAllowedTools(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&securityTool{name: "read_file"})
	reg.Register(&securityTool{name: "grep"})
	reg.Register(&securityTool{name: "edit_file"})

	scoped, err := DefaultSecurityHooks().ScopeRegistry(reg,
		&agentskills.Skill{
			Name:         "debug",
			Description:  "debug",
			AllowedTools: agentskills.ToolList{"read_file", "grep"},
		},
		&agentskills.Skill{
			Name:         "edit",
			Description:  "edit",
			AllowedTools: agentskills.ToolList{"edit_file"},
		},
	)
	if err != nil {
		t.Fatalf("ScopeRegistry: %v", err)
	}
	if got := scoped.Names(); len(got) != 3 || got[0] != "edit_file" || got[1] != "grep" ||
		got[2] != "read_file" {
		t.Fatalf("scoped names = %#v", got)
	}
}

func TestSecurityHooks_ScopeRegistryFailsClosedWithoutRegistry(t *testing.T) {
	_, err := DefaultSecurityHooks().ScopeRegistry(nil, &agentskills.Skill{
		Name:         "debug",
		Description:  "debug",
		AllowedTools: agentskills.ToolList{"read_file"},
	})
	if err == nil {
		t.Fatal("expected ScopeRegistry to fail without registry")
	}
}
