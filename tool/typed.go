package tool

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
)

// Handler executes a typed tool operation.
type Handler[A, R any] func(context.Context, A) (R, error)

// ApprovalFunc maps typed tool arguments to an optional approval requirement.
type ApprovalFunc[A any] func(A) (approval.Requirement, bool, error)

// TypedConfig describes one typed Go-authored tool.
type TypedConfig[A, R any] struct {
	Name        string
	Description string
	Schema      any
	Metadata    Metadata
	Execute     Handler[A, R]
	Approval    ApprovalFunc[A]
}

// TypedTool adapts a typed Go handler to Canto's raw provider-facing tool
// contract. JSON decoding and encoding stay at this boundary.
type TypedTool[A, R any] struct {
	spec     llm.Spec
	metadata Metadata
	execute  Handler[A, R]
	approval ApprovalFunc[A]
}

var (
	_ Tool         = (*TypedTool[struct{}, struct{}])(nil)
	_ MetadataTool = (*TypedTool[struct{}, struct{}])(nil)
	_ ApprovalTool = (*TypedTool[struct{}, struct{}])(nil)
)

// NewTyped constructs a tool from a typed Go handler.
func NewTyped[A, R any](cfg TypedConfig[A, R]) (*TypedTool[A, R], error) {
	if cfg.Name == "" {
		return nil, errors.New("typed tool: name is required")
	}
	if cfg.Description == "" {
		return nil, errors.New("typed tool: description is required")
	}
	if cfg.Execute == nil {
		return nil, errors.New("typed tool: execute handler is required")
	}

	schema := cfg.Schema
	if schema == nil {
		var err error
		schema, err = SchemaFor[A]()
		if err != nil {
			return nil, err
		}
	}

	return &TypedTool[A, R]{
		spec: llm.Spec{
			Name:        cfg.Name,
			Description: cfg.Description,
			Parameters:  schema,
		},
		metadata: cfg.Metadata,
		execute:  cfg.Execute,
		approval: cfg.Approval,
	}, nil
}

// MustTyped constructs a typed tool and panics if the configuration is invalid.
func MustTyped[A, R any](cfg TypedConfig[A, R]) *TypedTool[A, R] {
	t, err := NewTyped(cfg)
	if err != nil {
		panic(err)
	}
	return t
}

// RegisterTyped constructs and registers a typed tool.
func RegisterTyped[A, R any](r *Registry, cfg TypedConfig[A, R]) (*TypedTool[A, R], error) {
	if r == nil {
		return nil, errors.New("typed tool: registry is required")
	}
	t, err := NewTyped(cfg)
	if err != nil {
		return nil, err
	}
	r.Register(t)
	return t, nil
}

func (t *TypedTool[A, R]) Spec() llm.Spec { return t.spec }

func (t *TypedTool[A, R]) Metadata() Metadata { return t.metadata }

func (t *TypedTool[A, R]) Execute(ctx context.Context, args string) (string, error) {
	var input A
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("typed tool %s: decode args: %w", t.spec.Name, err)
	}

	result, err := t.execute(ctx, input)
	if err != nil {
		return "", fmt.Errorf("typed tool %s: %w", t.spec.Name, err)
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("typed tool %s: encode result: %w", t.spec.Name, err)
	}
	return string(out), nil
}

func (t *TypedTool[A, R]) ApprovalRequirement(args string) (approval.Requirement, bool, error) {
	if t.approval == nil {
		return approval.Requirement{}, false, nil
	}
	var input A
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return approval.Requirement{}, false, fmt.Errorf(
			"typed tool %s: decode approval args: %w",
			t.spec.Name,
			err,
		)
	}
	return t.approval(input)
}
