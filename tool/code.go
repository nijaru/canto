package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nijaru/canto/llm"
)

// CodeExecutionTool executes arbitrary code in a sandboxed environment.
// Currently supports Python.
type CodeExecutionTool struct {
	Language string
	Executor *Executor
}

// NewCodeExecutionTool creates a new tool for code execution.
func NewCodeExecutionTool(language string) *CodeExecutionTool {
	return &CodeExecutionTool{
		Language: language,
		Executor: DefaultExecutor,
	}
}

// Spec returns the tool specification.
func (c *CodeExecutionTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "execute_code",
		Description: fmt.Sprintf("Execute arbitrary %s code in a sandboxed environment and return its output. Only use with trusted inputs.", c.Language),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": fmt.Sprintf("The %s code to execute.", c.Language),
				},
			},
			"required": []string{"code"},
		},
	}
}

// Execute runs the code using the executor.
func (c *CodeExecutionTool) Execute(ctx context.Context, args string) (string, error) {
	var input struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", err
	}

	if c.Language != "python" {
		return "", fmt.Errorf("language %s not supported yet", c.Language)
	}

	// 1. Create a temporary file for the code
	tmpDir, err := os.MkdirTemp("", "canto-code-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "script.py")
	if err := os.WriteFile(tmpFile, []byte(input.Code), 0o600); err != nil {
		return "", fmt.Errorf("failed to write code to temp file: %w", err)
	}

	// 2. Execute the code using the executor
	// We use `python3` specifically to avoid potential Python 2 issues.
	return c.Executor.Execute(ctx, "python3", tmpFile)
}
