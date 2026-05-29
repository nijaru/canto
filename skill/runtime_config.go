package skill

import (
	"context"

	agentskills "github.com/nijaru/agentskills"
	"github.com/nijaru/canto/agent"
	"github.com/nijaru/ion/prompt"
	"github.com/nijaru/ion/tool"
)

// RuntimeConfig validates and preloads skills for a runtime-scoped agent view.
func RuntimeConfig(
	ctx context.Context,
	reg *tool.Registry,
	skills ...*agentskills.Skill,
) (agent.RuntimeConfig, error) {
	if len(skills) == 0 {
		return agent.RuntimeConfig{Tools: reg}, nil
	}

	hooks := DefaultSecurityHooks()
	if err := hooks.Validate(ctx, skills...); err != nil {
		return agent.RuntimeConfig{}, err
	}
	scopedTools, err := hooks.ScopeRegistry(reg, skills...)
	if err != nil {
		return agent.RuntimeConfig{}, err
	}
	return agent.RuntimeConfig{
		Tools:             scopedTools,
		RequestProcessors: []prompt.RequestProcessor{PreloadPrompt(skills...)},
	}, nil
}
