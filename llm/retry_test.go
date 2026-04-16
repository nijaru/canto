package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

type retryProviderStub struct {
	generateFn    func(context.Context, *Request) (*Response, error)
	streamFn      func(context.Context, *Request) (Stream, error)
	isTransientFn func(error) bool
	isOverflowFn  func(error) bool
	generateCalls int
	streamCalls   int
}

func (p *retryProviderStub) ID() string { return "retry-stub" }

func (p *retryProviderStub) Generate(ctx context.Context, req *Request) (*Response, error) {
	p.generateCalls++
	return p.generateFn(ctx, req)
}

func (p *retryProviderStub) Stream(ctx context.Context, req *Request) (Stream, error) {
	p.streamCalls++
	return p.streamFn(ctx, req)
}

func (p *retryProviderStub) Models(context.Context) ([]Model, error) { return nil, nil }

func (p *retryProviderStub) CountTokens(context.Context, string, []Message) (int, error) {
	return 0, nil
}

func (p *retryProviderStub) Cost(context.Context, string, Usage) float64 { return 0 }

func (p *retryProviderStub) Capabilities(string) Capabilities { return DefaultCapabilities() }

func (p *retryProviderStub) IsTransient(err error) bool {
	if p.isTransientFn != nil {
		return p.isTransientFn(err)
	}
	return false
}

func (p *retryProviderStub) IsContextOverflow(err error) bool {
	if p.isOverflowFn != nil {
		return p.isOverflowFn(err)
	}
	return false
}

func TestRetryProvider_NormalizesZeroAttempts(t *testing.T) {
	inner := &retryProviderStub{
		generateFn: func(context.Context, *Request) (*Response, error) {
			return nil, errors.New("transient")
		},
		isTransientFn: func(error) bool { return true },
	}
	rp := &RetryProvider{
		Provider: inner,
		Config: RetryConfig{
			MaxAttempts: 0,
		},
	}

	resp, err := rp.Generate(t.Context(), &Request{})
	if err == nil {
		t.Fatal("expected retry provider to return the transient error")
	}
	if resp != nil {
		t.Fatalf("expected nil response, got %#v", resp)
	}
	if inner.generateCalls != 1 {
		t.Fatalf("expected exactly one attempt, got %d", inner.generateCalls)
	}
}

func TestRetryProvider_CancelsDuringBackoff(t *testing.T) {
	inner := &retryProviderStub{
		generateFn: func(context.Context, *Request) (*Response, error) {
			return nil, errors.New("transient")
		},
		isTransientFn: func(error) bool { return true },
	}
	rp := &RetryProvider{
		Provider: inner,
		Config: RetryConfig{
			MaxAttempts: 2,
			MinInterval: 50 * time.Millisecond,
			MaxInterval: 50 * time.Millisecond,
			Multiplier:  2,
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	resp, err := rp.Generate(ctx, &Request{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got resp=%#v err=%v", resp, err)
	}
	if inner.generateCalls != 1 {
		t.Fatalf("expected a single attempt before cancellation, got %d", inner.generateCalls)
	}
}
