package environmenttool

import (
	"fmt"

	"github.com/nijaru/canto/executor"
	"github.com/nijaru/canto/executortool"
	"github.com/nijaru/canto/workspacetool"
	"github.com/nijaru/ion/safety"
	"github.com/nijaru/ion/tool"
	"github.com/nijaru/ion/workspace"
)

// Capabilities groups host-provided effect boundaries for optional capability
// tools. It describes where effects happen; it does not encode product policy.
type Capabilities struct {
	Workspace workspace.WorkspaceFS
	Executor  *executor.Executor
	Sandbox   safety.Sandbox
	Secrets   safety.SecretInjector
}

// Config selects capability tools to construct from host capabilities.
type Config struct {
	Workspace    bool
	Executor     bool
	CodeLanguage string
}

// Tools builds capability-oriented tools from host capabilities.
func Tools(cap Capabilities, cfg Config) ([]tool.Tool, error) {
	tools := make([]tool.Tool, 0, toolCount(cfg))
	if cfg.Workspace {
		if cap.Workspace == nil {
			return nil, fmt.Errorf("canto environment tools: workspace is required")
		}
		tools = append(
			tools,
			workspacetool.NewReadFileTool(cap.Workspace),
			workspacetool.NewWriteFileTool(cap.Workspace),
			workspacetool.NewListDirTool(cap.Workspace),
			workspacetool.NewEditTool(cap.Workspace),
		)
	}

	if cfg.Executor || cfg.CodeLanguage != "" {
		exec := executorFromCapabilities(cap)
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

func toolCount(cfg Config) int {
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

func executorFromCapabilities(cap Capabilities) *executor.Executor {
	if cap.Executor == nil {
		return nil
	}
	exec := *cap.Executor
	if cap.Sandbox != nil {
		exec.Sandbox = cap.Sandbox
	}
	if cap.Secrets != nil {
		exec.SecretInjector = cap.Secrets
	}
	return &exec
}
