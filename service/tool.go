package service

import (
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-json-experiment/json"
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/tool"
)

// Handler executes a typed service/API operation.
type Handler[A, R any] func(context.Context, A) (R, error)

// ApprovalFunc returns the approval requirement for a typed invocation.
type ApprovalFunc[A any] func(A) (approval.Requirement, bool, error)

// RetryPolicy controls retry behavior for transient service/API failures.
type RetryPolicy struct {
	MaxAttempts int
	Delay       time.Duration
	Retryable   func(error) bool
}

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

func (t *Tool[A, R]) executeWithRetry(ctx context.Context, input A) (R, error) {
	var zero R
	attempts := t.retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := range attempts {
		result, err := t.execute(ctx, input)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt == attempts-1 || !t.retry.shouldRetry(err) {
			return zero, err
		}
		if t.retry.Delay <= 0 {
			continue
		}
		timer := time.NewTimer(t.retry.Delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, lastErr
}

func (p RetryPolicy) shouldRetry(err error) bool {
	if p.Retryable == nil {
		return false
	}
	return p.Retryable(err)
}

// SchemaFor infers a JSON Schema for A and returns it as a JSON-compatible map.
func SchemaFor[A any]() (map[string]any, error) {
	schema, err := jsonschema.For[A](nil)
	if err != nil {
		return nil, fmt.Errorf("service schema: %w", err)
	}
	data, err := stdjson.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("service schema: marshal: %w", err)
	}
	var out map[string]any
	if err := stdjson.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("service schema: unmarshal: %w", err)
	}
	return out, nil
}

// Requirement builds an approval function with typed access to the arguments.
func Requirement[A any](
	category safety.Category,
	operation string,
	resource func(A) string,
	metadata map[string]any,
) ApprovalFunc[A] {
	return func(args A) (approval.Requirement, bool, error) {
		req := approval.Requirement{
			Category:  string(category),
			Operation: operation,
			Metadata:  cloneMetadata(metadata),
		}
		if resource != nil {
			req.Resource = resource(args)
		}
		return req, true, nil
	}
}

// ReadOnly marks an external service/API operation as a read.
func ReadOnly[A any](operation string, resource func(A) string) ApprovalFunc[A] {
	return Requirement(safety.CategoryRead, operation, resource, nil)
}

// Mutation marks an external service/API operation as a write.
func Mutation[A any](operation string, resource func(A) string) ApprovalFunc[A] {
	return Requirement(safety.CategoryWrite, operation, resource, nil)
}

// Execution marks an external service/API operation as command execution.
func Execution[A any](operation string, resource func(A) string) ApprovalFunc[A] {
	return Requirement(safety.CategoryExecute, operation, resource, nil)
}

func cloneMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
