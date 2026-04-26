package coding

import (
	"context"
	"iter"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/tool"
)

// ShellTool executes shell commands.
type ShellTool struct {
	Executor    *Executor
	Dir         string
	Shell       string
	CommandFlag string
}

var _ tool.StreamingTool = (*ShellTool)(nil)

// Spec returns the tool specification.
func (b *ShellTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "shell",
		Description: "Execute a shell command and return its output. WARNING: This tool executes arbitrary shell commands with no sandboxing. Only use with trusted inputs.",
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
func (b *ShellTool) Execute(ctx context.Context, args string) (string, error) {
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
		Name: b.shell(),
		Args: []string{b.commandFlag(), input.Command},
		Dir:  b.Dir,
	})
	if err != nil {
		return result.Combined, err
	}
	return result.Combined, nil
}

func (b *ShellTool) ExecuteStreaming(ctx context.Context, args string) iter.Seq2[string, error] {
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
			Name: b.shell(),
			Args: []string{b.commandFlag(), input.Command},
			Dir:  b.Dir,
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

func (b *ShellTool) ApprovalRequirement(args string) (approval.Requirement, bool, error) {
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

func (b *ShellTool) shell() string {
	if b.Shell != "" {
		return b.Shell
	}
	return "sh"
}

func (b *ShellTool) commandFlag() string {
	if b.CommandFlag != "" {
		return b.CommandFlag
	}
	return "-c"
}
