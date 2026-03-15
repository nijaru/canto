package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm/providers/openai"
)

func TestWorkspace(t *testing.T) {
	tmp := t.TempDir()

	// Setup fake workspace
	agentsContent := "# Project Instructions"
	soulContent := "## Persona"
	os.WriteFile(filepath.Join(tmp, AgentsFile), []byte(agentsContent), 0o644)
	os.WriteFile(filepath.Join(tmp, SoulFile), []byte(soulContent), 0o644)

	// Test FindRoot
	subDir := filepath.Join(tmp, "sub", "dir")
	os.MkdirAll(subDir, 0o755)
	root, err := FindRoot(subDir)
	if err != nil {
		t.Fatal(err)
	}
	if root != tmp {
		t.Errorf("expected root %s, got %s", tmp, root)
	}

	// Test LoadWorkspace
	ws, err := LoadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if ws.AgentsPrompt != agentsContent {
		t.Errorf("expected agents prompt %s, got %s", agentsContent, ws.AgentsPrompt)
	}
	if ws.SoulPrompt != soulContent {
		t.Errorf("expected soul prompt %s, got %s", soulContent, ws.SoulPrompt)
	}

	// Test Configure
	p := &openai.Provider{} // Dummy
	a := agent.New("test", "base instructions", "gpt-4", p, nil)
	ws.Configure(a)

	// Should have: [AgentsPrompt, SoulPrompt, base instructions, tools, history]
	// Actually base instructions are in the middle now.
	if len(a.Builder.Processors) != 5 {
		t.Errorf("expected 5 processors, got %d", len(a.Builder.Processors))
	}
}
