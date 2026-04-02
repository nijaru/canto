package approval

import (
	"context"
	"errors"
	"testing"
)

func TestPolicyChain_LastHandledWins(t *testing.T) {
	chain := NewChain(
		PolicyFunc(func(context.Context, Request) (Result, bool, error) {
			return Result{Decision: DecisionDeny, Reason: "base"}, true, nil
		}),
		PolicyFunc(func(context.Context, Request) (Result, bool, error) {
			return Result{Decision: DecisionAllow, Reason: "override"}, true, nil
		}),
	)

	res, handled, err := chain.Decide(context.Background(), Request{Category: "tool"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !handled {
		t.Fatal("expected request to be handled")
	}
	if !res.Allowed() || res.Reason != "override" {
		t.Fatalf("unexpected chain result: %#v", res)
	}
}

func TestPolicyChain_DefersWhenUnmanaged(t *testing.T) {
	chain := NewChain(
		PolicyFunc(func(context.Context, Request) (Result, bool, error) {
			return Result{}, false, nil
		}),
	)

	_, handled, err := chain.Decide(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if handled {
		t.Fatal("expected chain to defer")
	}
}

func TestPolicyChain_PropagatesErrors(t *testing.T) {
	want := errors.New("boom")
	chain := NewChain(
		PolicyFunc(func(context.Context, Request) (Result, bool, error) {
			return Result{}, false, want
		}),
	)

	_, _, err := chain.Decide(context.Background(), Request{})
	if !errors.Is(err, want) {
		t.Fatalf("Decide error = %v, want %v", err, want)
	}
}
