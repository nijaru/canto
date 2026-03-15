package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/nijaru/canto/llm"
)

// BashTool executes shell commands.
type BashTool struct{}

// Spec returns the tool specification.
func (b *BashTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "bash",
		Description: "Execute a bash command and return its output.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The command to execute.",
				},
			},
			"required": []string{"command"},
		},
	}
}

// Execute runs the shell command.
// WARNING: This tool is prone to command injection if the LLM is not trusted
// or if the inputs are not properly sanitized. Use with caution in production.
func (b *BashTool) Execute(ctx context.Context, args string) (string, error) {
	// Parse arguments (simple JSON extraction or just assume it's correctly formatted)
	// For Phase 1, we'll keep it simple.
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", input.Command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Return the combined output with a nil error so the LLM sees the actual
		// command output needed to diagnose the failure (exit code alone is not useful).
		return fmt.Sprintf("exit error: %v\n%s", err, string(out)), nil
	}
	return string(out), nil
}
