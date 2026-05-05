package governor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/llm"
	xtesting "github.com/nijaru/canto/x/testing"
)

func overflowProvider(steps ...xtesting.Step) *xtesting.FauxProvider {
	p := xtesting.NewFauxProvider("recovery-test", steps...)
	p.IsContextOverflowFn = func(err error) bool {
		return err != nil && err.Error() == "context_length_exceeded"
	}
	return p
}

func newRecoveryProvider(
	t *testing.T,
	inner llm.Provider,
	compact governor.CompactFunc,
) *governor.RecoveryProvider {
	t.Helper()
	rp, err := governor.NewRecoveryProvider(inner, compact)
	if err != nil {
		t.Fatalf("NewRecoveryProvider: %v", err)
	}
	return rp
}

func TestRecoveryProvider_PassThrough(t *testing.T) {
	mock := xtesting.NewFauxProvider("test",
		xtesting.Step{Content: "ok"},
	)

	compactCalled := false
	rp := newRecoveryProvider(t, mock, func(_ context.Context) error {
		compactCalled = true
		return nil
	})

	resp, err := rp.Generate(t.Context(), &llm.Request{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("expected 'ok', got %q", resp.Content)
	}
	if compactCalled {
		t.Fatal("compact should not be called on success")
	}
}

func TestRecoveryProvider_OverflowThenSuccess(t *testing.T) {
	mock := overflowProvider(
		xtesting.Step{Err: errors.New("context_length_exceeded")},
		xtesting.Step{Content: "recovered"},
	)

	compactCalls := 0
	rp := newRecoveryProvider(t, mock, func(_ context.Context) error {
		compactCalls++
		return nil
	})

	resp, err := rp.Generate(t.Context(), &llm.Request{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "recovered" {
		t.Fatalf("expected 'recovered', got %q", resp.Content)
	}
	if compactCalls != 1 {
		t.Fatalf("expected 1 compact call, got %d", compactCalls)
	}
}

func TestRecoveryProvider_DoubleOverflow(t *testing.T) {
	mock := overflowProvider(
		xtesting.Step{Err: errors.New("context_length_exceeded")},
		xtesting.Step{Err: errors.New("context_length_exceeded")},
	)

	compactCalls := 0
	rp := newRecoveryProvider(t, mock, func(_ context.Context) error {
		compactCalls++
		return nil
	})

	_, err := rp.Generate(t.Context(), &llm.Request{Model: "test"})
	if err == nil {
		t.Fatal("expected error on double overflow")
	}
	if err.Error() != "context_length_exceeded" {
		t.Fatalf("expected original overflow error, got: %v", err)
	}
	if compactCalls != 1 {
		t.Fatalf("expected exactly 1 compact call, got %d", compactCalls)
	}
}

func TestRecoveryProvider_CompactFailure(t *testing.T) {
	mock := overflowProvider(
		xtesting.Step{Err: errors.New("context_length_exceeded")},
	)

	rp := newRecoveryProvider(t, mock, func(_ context.Context) error {
		return errors.New("disk full")
	})

	_, err := rp.Generate(t.Context(), &llm.Request{Model: "test"})
	if err == nil {
		t.Fatal("expected error when compact fails")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error")
	}
}

func TestRecoveryProvider_NonOverflowError(t *testing.T) {
	mock := xtesting.NewFauxProvider("test",
		xtesting.Step{Err: errors.New("rate limited")},
	)

	compactCalled := false
	rp := newRecoveryProvider(t, mock, func(_ context.Context) error {
		compactCalled = true
		return nil
	})

	_, err := rp.Generate(t.Context(), &llm.Request{Model: "test"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if err.Error() != "rate limited" {
		t.Fatalf("expected 'rate limited', got: %v", err)
	}
	if compactCalled {
		t.Fatal("compact should not be called for non-overflow errors")
	}
}

func TestRecoveryProvider_StreamOverflow(t *testing.T) {
	mock := overflowProvider(
		xtesting.Step{Err: errors.New("context_length_exceeded")},
		xtesting.Step{Content: "recovered"},
	)

	compactCalls := 0
	rp := newRecoveryProvider(t, mock, func(_ context.Context) error {
		compactCalls++
		return nil
	})

	s, err := rp.Stream(t.Context(), &llm.Request{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil stream")
	}
	s.Close()
	if compactCalls != 1 {
		t.Fatalf("expected 1 compact call, got %d", compactCalls)
	}
}

func TestRecoveryProvider_ContextCancellation(t *testing.T) {
	mock := overflowProvider(
		xtesting.Step{Err: errors.New("context_length_exceeded")},
	)

	ctx, cancel := context.WithCancel(t.Context())

	rp := newRecoveryProvider(t, mock, func(compactCtx context.Context) error {
		cancel()
		return compactCtx.Err()
	})

	_, err := rp.Generate(ctx, &llm.Request{Model: "test"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestRecoveryProvider_NilCompactReturnsError(t *testing.T) {
	mock := xtesting.NewFauxProvider("test")
	_, err := governor.NewRecoveryProvider(mock, nil)
	if err == nil {
		t.Fatal("expected nil compact error")
	}
}
