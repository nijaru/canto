package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func (r *ChildRunner) materializeChildSession(
	ctx context.Context,
	parent *session.Session,
	childSessionID string,
	spec ChildSpec,
) (*session.Session, error) {
	var child *session.Session
	var err error

	switch spec.Mode {
	case session.ChildModeFork:
		child, err = parent.Branch(ctx, childSessionID, session.ForkOptions{})
		if err != nil {
			return nil, fmt.Errorf("materialize forked child session: %w", err)
		}
	default:
		child = session.New(childSessionID).WithWriter(r.store)
	}

	for _, msg := range childSeedMessages(spec) {
		if err := child.Append(ctx, session.NewMessage(child.ID(), msg)); err != nil {
			return nil, fmt.Errorf("materialize child initial message: %w", err)
		}
	}

	return child, nil
}

func childSeedMessages(spec ChildSpec) []llm.Message {
	if len(spec.InitialMessages) > 0 {
		return append([]llm.Message(nil), spec.InitialMessages...)
	}
	if spec.Mode != session.ChildModeHandoff {
		return nil
	}

	var parts []string
	if spec.Task != "" {
		parts = append(parts, "Task: "+spec.Task)
	}
	if spec.Context != "" {
		parts = append(parts, "Context: "+spec.Context)
	}
	if len(parts) == 0 {
		return nil
	}

	return []llm.Message{{
		Role:    llm.RoleUser,
		Content: strings.Join(parts, "\n"),
	}}
}
