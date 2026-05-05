package runtime

import (
	"errors"
	"fmt"
	"slices"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/oklog/ulid/v2"
)

func validateChildSpec(spec ChildSpec) error {
	switch spec.Mode {
	case session.ChildModeFork, session.ChildModeHandoff, session.ChildModeFresh:
		return nil
	case "":
		return nil
	default:
		return fmt.Errorf("spawn child: unsupported mode %q", spec.Mode)
	}
}

func configureChildAgent(a agent.Agent, reg *tool.Registry) (agent.Agent, error) {
	return configureChildAgentWithRuntime(a, agent.RuntimeConfig{Tools: reg})
}

func configureChildAgentWithRuntime(
	a agent.Agent,
	cfg agent.RuntimeConfig,
) (agent.Agent, error) {
	if cfg.Tools == nil && len(cfg.RequestProcessors) == 0 {
		return a, nil
	}
	configurable, ok := a.(agent.RuntimeConfigurable)
	if !ok {
		return nil, fmt.Errorf("spawn child: agent %q does not support runtime overrides", a.ID())
	}
	return configurable.ConfigureRuntime(cfg), nil
}

func normalizeChildSpec(spec ChildSpec) (ChildSpec, error) {
	if spec.Agent == nil {
		return ChildSpec{}, errors.New("spawn child: nil agent")
	}
	if spec.Mode == "" {
		spec.Mode = session.ChildModeHandoff
	}
	if err := validateChildSpec(spec); err != nil {
		return ChildSpec{}, err
	}
	if spec.ID == "" {
		spec.ID = ulid.Make().String()
	}
	if spec.SessionID == "" {
		spec.SessionID = spec.ID
	}
	return spec, nil
}

func mergeMetadata(base map[string]any, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && text == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	keys := make([]string, 0, len(out))
	for key := range out {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	ordered := make(map[string]any, len(out))
	for _, key := range keys {
		ordered[key] = out[key]
	}
	return ordered
}
