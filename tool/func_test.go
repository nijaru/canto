package tool

import (
	"context"
	"errors"
	"testing"
)

func TestFunc_Spec(t *testing.T) {
	schema := map[string]any{"type": "object"}
	tool := Func(
		"echo",
		"echoes args",
		schema,
		func(_ context.Context, args string) (string, error) {
			return args, nil
		},
	)

	spec := tool.Spec()
	if spec.Name != "echo" {
		t.Fatalf("name = %q, want %q", spec.Name, "echo")
	}
	if spec.Description != "echoes args" {
		t.Fatalf("description = %q, want %q", spec.Description, "echoes args")
	}
}

func TestFunc_Execute(t *testing.T) {
	tool := Func("echo", "", nil, func(_ context.Context, args string) (string, error) {
		return "got: " + args, nil
	})

	out, err := tool.Execute(context.Background(), `{"x":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `got: {"x":1}` {
		t.Fatalf("output = %q", out)
	}
}

func TestFunc_ExecuteError(t *testing.T) {
	want := errors.New("boom")
	tool := Func("fail", "", nil, func(_ context.Context, _ string) (string, error) {
		return "", want
	})

	_, err := tool.Execute(context.Background(), "")
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}
