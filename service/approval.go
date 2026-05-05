package service

import (
	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/safety"
)

// ApprovalFunc returns the approval requirement for a typed invocation.
type ApprovalFunc[A any] func(A) (approval.Requirement, bool, error)

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
