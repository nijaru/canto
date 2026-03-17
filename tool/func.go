package tool

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/llm"
)

// funcTool adapts a plain function to the Tool interface.
type funcTool struct {
	spec llm.ToolSpec
	fn   func(ctx context.Context, args string) (string, error)
}

func (f *funcTool) Spec() llm.ToolSpec { return f.spec }

func (f *funcTool) Execute(ctx context.Context, args string) (string, error) {
	res, err := f.fn(ctx, args)
	if err != nil {
		return "", fmt.Errorf("tool %s: %w", f.spec.Name, err)
	}
	return res, nil
}

// Func constructs a Tool from a function, eliminating struct boilerplate
// for stateless single-function tools.
func Func(
	name, desc string,
	schema any,
	fn func(ctx context.Context, args string) (string, error),
) Tool {
	return &funcTool{
		spec: llm.ToolSpec{Name: name, Description: desc, Parameters: schema},
		fn:   fn,
	}
}
