package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

// Handler executes a typed service/API operation.
type Handler[A, R any] func(context.Context, A) (R, error)

// Config describes one typed service/API tool.
type Config[A, R any] struct {
	Name        string
	Description string
	Schema      any
	Metadata    tool.Metadata
	Execute     Handler[A, R]
	Approval    ApprovalFunc[A]
	Retry       RetryPolicy
}

// Tool adapts a typed service/API handler to Canto's tool interfaces.
type Tool[A, R any] struct {
	spec     llm.Spec
	metadata tool.Metadata
	execute  Handler[A, R]
	approval ApprovalFunc[A]
	retry    RetryPolicy
}

var (
	_ tool.Tool         = (*Tool[struct{}, struct{}])(nil)
	_ tool.MetadataTool = (*Tool[struct{}, struct{}])(nil)
	_ tool.ApprovalTool = (*Tool[struct{}, struct{}])(nil)
)

// New constructs a typed service/API tool.
func New[A, R any](cfg Config[A, R]) (*Tool[A, R], error) {
	if cfg.Name == "" {
		return nil, errors.New("service tool: name is required")
	}
	if cfg.Description == "" {
		return nil, errors.New("service tool: description is required")
	}
	if cfg.Execute == nil {
		return nil, errors.New("service tool: execute handler is required")
	}

	schema := cfg.Schema
	if schema == nil {
		var err error
		schema, err = SchemaFor[A]()
		if err != nil {
			return nil, err
		}
	}

	return &Tool[A, R]{
		spec: llm.Spec{
			Name:        cfg.Name,
			Description: cfg.Description,
			Parameters:  schema,
		},
		metadata: cfg.Metadata,
		execute:  cfg.Execute,
		approval: cfg.Approval,
		retry:    cfg.Retry,
	}, nil
}

// Must constructs a typed service/API tool and panics if the configuration is invalid.
func Must[A, R any](cfg Config[A, R]) *Tool[A, R] {
	t, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return t
}

// Register constructs and registers a typed service/API tool.
func Register[A, R any](r *tool.Registry, cfg Config[A, R]) (*Tool[A, R], error) {
	if r == nil {
		return nil, errors.New("service tool: registry is required")
	}
	t, err := New(cfg)
	if err != nil {
		return nil, err
	}
	r.Register(t)
	return t, nil
}

func (t *Tool[A, R]) Spec() llm.Spec { return t.spec }

func (t *Tool[A, R]) Metadata() tool.Metadata { return t.metadata }

func (t *Tool[A, R]) Execute(ctx context.Context, args string) (string, error) {
	var input A
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("service tool %s: decode args: %w", t.spec.Name, err)
	}

	result, err := t.executeWithRetry(ctx, input)
	if err != nil {
		return "", fmt.Errorf("service tool %s: %w", t.spec.Name, err)
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("service tool %s: encode result: %w", t.spec.Name, err)
	}
	return string(out), nil
}

func (t *Tool[A, R]) ApprovalRequirement(args string) (approval.Requirement, bool, error) {
	if t.approval == nil {
		return approval.Requirement{}, false, nil
	}
	var input A
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return approval.Requirement{}, false, fmt.Errorf(
			"service tool %s: decode approval args: %w",
			t.spec.Name,
			err,
		)
	}
	return t.approval(input)
}
