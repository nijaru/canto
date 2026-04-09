package skill

import (
	"context"
	"fmt"
	"slices"

	agentskills "github.com/nijaru/agentskills"
	"github.com/nijaru/canto/tool"
)

type Validator interface {
	ValidateSkill(ctx context.Context, skill *agentskills.Skill) error
}

type ValidatorFunc func(ctx context.Context, skill *agentskills.Skill) error

func (f ValidatorFunc) ValidateSkill(
	ctx context.Context,
	skill *agentskills.Skill,
) error {
	return f(ctx, skill)
}

type SecurityHooks struct {
	Validators []Validator
}

func DefaultSecurityHooks() SecurityHooks {
	return SecurityHooks{
		Validators: []Validator{
			ValidatorFunc(func(_ context.Context, skill *agentskills.Skill) error {
				if skill == nil {
					return fmt.Errorf("skill security: nil skill")
				}
				return skill.Validate()
			}),
		},
	}
}

func (h SecurityHooks) Validate(
	ctx context.Context,
	skills ...*agentskills.Skill,
) error {
	for _, skill := range skills {
		for _, validator := range h.Validators {
			if validator == nil {
				continue
			}
			if err := validator.ValidateSkill(ctx, skill); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h SecurityHooks) ScopeRegistry(
	reg *tool.Registry,
	skills ...*agentskills.Skill,
) (*tool.Registry, error) {
	allowed := allowedTools(skills...)
	if len(allowed) == 0 {
		return reg, nil
	}
	if reg == nil {
		return nil, fmt.Errorf(
			"skill security: active skills declare allowed-tools but no registry was provided",
		)
	}
	return reg.Subset(allowed...)
}

func allowedTools(skills ...*agentskills.Skill) []string {
	set := make(map[string]struct{})
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		for _, name := range skill.AllowedTools {
			if name == "" {
				continue
			}
			set[name] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
