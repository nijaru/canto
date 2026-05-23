package canto

import (
	"fmt"

	"github.com/nijaru/canto/executor"
	"github.com/nijaru/canto/executortool"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/workspacetool"
)

// EnvironmentToolConfig selects capability tools to construct from a Harness
// Environment. It is opt-in; Environment alone does not register tools.
type EnvironmentToolConfig struct {
	Workspace    bool
	Executor     bool
	CodeLanguage string
}

// ToolsFromEnvironment builds capability-oriented tools from env.
func ToolsFromEnvironment(env Environment, cfg EnvironmentToolConfig) ([]tool.Tool, error) {
	tools := make([]tool.Tool, 0, environmentToolCount(cfg))
	if cfg.Workspace {
		if env.Workspace == nil {
			return nil, fmt.Errorf("canto environment tools: workspace is required")
		}
		tools = append(
			tools,
			workspacetool.NewReadFileTool(env.Workspace),
			workspacetool.NewWriteFileTool(env.Workspace),
			workspacetool.NewListDirTool(env.Workspace),
			workspacetool.NewEditTool(env.Workspace),
		)
	}

	if cfg.Executor || cfg.CodeLanguage != "" {
		exec := executorFromEnvironment(env)
		if exec == nil {
			return nil, fmt.Errorf("canto environment tools: executor is required")
		}
		if cfg.Executor {
			tools = append(tools, &executortool.ShellTool{Executor: exec})
		}
		if cfg.CodeLanguage != "" {
			code := executortool.NewCodeExecutionTool(cfg.CodeLanguage)
			code.Executor = exec
			tools = append(tools, code)
		}
	}
	return tools, nil
}

func environmentToolCount(cfg EnvironmentToolConfig) int {
	count := 0
	if cfg.Workspace {
		count += 4
	}
	if cfg.Executor {
		count++
	}
	if cfg.CodeLanguage != "" {
		count++
	}
	return count
}

func executorFromEnvironment(env Environment) *executor.Executor {
	if env.Executor == nil {
		return nil
	}
	exec := *env.Executor
	if env.Sandbox != nil {
		exec.Sandbox = env.Sandbox
	}
	if env.Secrets != nil {
		exec.SecretInjector = env.Secrets
	}
	return &exec
}
