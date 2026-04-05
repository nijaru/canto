package tools

import (
	"context"
	"iter"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/tool"
)

// BashTool executes shell commands.
type BashTool struct {
	Executor *Executor
}

var _ tool.StreamingTool = (*BashTool)(nil)

// Spec returns the tool specification.
func (b *BashTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "bash",
		Description: "Execute a bash command and return its output. WARNING: This tool executes arbitrary shell commands with no sandboxing. Only use with trusted inputs.",
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

	executor := b.Executor
	if executor == nil {
		executor = DefaultExecutor
	}
	result, err := executor.Run(ctx, Command{
		Name: "bash",
		Args: []string{"-c", input.Command},
	})
	if err != nil {
		return result.Combined, err
	}
	return result.Combined, nil
}

func (b *BashTool) ExecuteStreaming(ctx context.Context, args string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		var input struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(args), &input); err != nil {
			yield("", err)
			return
		}

		executor := b.Executor
		if executor == nil {
			executor = DefaultExecutor
		}

		type item struct {
			text string
			err  error
		}
		ch := make(chan item, 10)
		cmd := Command{
			Name: "bash",
			Args: []string{"-c", input.Command},
			OnOutput: func(c OutputChunk) {
				ch <- item{text: c.Text}
			},
		}

		go func() {
			_, err := executor.Run(ctx, cmd)
			if err != nil {
				ch <- item{err: err}
			}
			close(ch)
		}()

		for it := range ch {
			if !yield(it.text, it.err) {
				return
			}
		}
	}
}

func (b *BashTool) ApprovalRequirement(args string) (approval.Requirement, bool, error) {
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return approval.Requirement{}, false, err
	}
	return approval.Requirement{
		Category:  string(safety.CategoryExecute),
		Operation: "exec",
		Resource:  input.Command,
	}, true, nil
}
