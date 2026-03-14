package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nijaru/canto/agent"
	ccontext "github.com/nijaru/canto/context"
)

// Workspace markers
const (
	AgentsFile = "AGENTS.md"
	SoulFile   = "SOUL.md"
	GitDir     = ".git"
)

// Workspace represents the project context loaded from the filesystem.
type Workspace struct {
	Root         string
	AgentsPrompt string
	SoulPrompt   string
}

// FindRoot traverses up from the startPath looking for workspace markers.
func FindRoot(startPath string) (string, error) {
	curr, err := filepath.Abs(startPath)
	if err != nil {
		return "", err
	}

	for {
		// Check for markers
		if _, err := os.Stat(filepath.Join(curr, AgentsFile)); err == nil {
			return curr, nil
		}
		if _, err := os.Stat(filepath.Join(curr, GitDir)); err == nil {
			return curr, nil
		}

		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}

	return "", fmt.Errorf("workspace root not found starting from %s", startPath)
}

// LoadWorkspace loads agent and soul prompts from the given root directory.
func LoadWorkspace(root string) (*Workspace, error) {
	ws := &Workspace{Root: root}

	// Load AGENTS.md
	agentsPath := filepath.Join(root, AgentsFile)
	if b, err := os.ReadFile(agentsPath); err == nil {
		ws.AgentsPrompt = string(b)
	}

	// Load SOUL.md
	soulPath := filepath.Join(root, SoulFile)
	if b, err := os.ReadFile(soulPath); err == nil {
		ws.SoulPrompt = string(b)
	}

	return ws, nil
}

// Configure adds workspace prompts to the agent's context builder.
func (ws *Workspace) Configure(a *agent.Agent) {
	// Prepend SoulPrompt if available (more specific)
	if ws.SoulPrompt != "" {
		a.Builder.Processors = append([]ccontext.ContextProcessor{
			ccontext.InstructionProcessor(ws.SoulPrompt),
		}, a.Builder.Processors...)
	}

	// Prepend AgentsPrompt if available (more general)
	if ws.AgentsPrompt != "" {
		a.Builder.Processors = append([]ccontext.ContextProcessor{
			ccontext.InstructionProcessor(ws.AgentsPrompt),
		}, a.Builder.Processors...)
	}
}
