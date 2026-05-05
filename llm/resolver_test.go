package llm

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/sashabaranov/go-openai"
)

type mockProvider struct {
	id                  string
	genFn               func(ctx context.Context, req *Request) (*Response, error)
	streamFn            func(ctx context.Context, req *Request) (Stream, error)
	models              []Model
	isTransientFn       func(error) bool
	isContextOverflowFn func(error) bool
}

func (m *mockProvider) ID() string { return m.id }
func (m *mockProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	return m.genFn(ctx, req)
}

func (m *mockProvider) Stream(ctx context.Context, req *Request) (Stream, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, req)
	}
	return nil, errors.New("not implemented")
}

func (m *mockProvider) Models(ctx context.Context) ([]Model, error) {
	return m.models, nil
}

func (m *mockProvider) CountTokens(
	ctx context.Context,
	model string,
	messages []Message,
) (int, error) {
	return 0, nil
}

func (m *mockProvider) Cost(ctx context.Context, model string, usage Usage) float64 {
	return 0
}
func (m *mockProvider) Capabilities(_ string) Capabilities { return DefaultCapabilities() }
func (m *mockProvider) IsTransient(err error) bool {
	if m.isTransientFn != nil {
		return m.isTransientFn(err)
	}
	return IsRateLimit(err)
}

func (m *mockProvider) IsContextOverflow(err error) bool {
	if m.isContextOverflowFn != nil {
		return m.isContextOverflowFn(err)
	}
	return false
}

func TestFailoverProvider(t *testing.T) {
	p1 := &mockProvider{
		id: "p1",
		genFn: func(ctx context.Context, req *Request) (*Response, error) {
			return nil, errors.New("p1 failed")
		},
	}
	p2 := &mockProvider{
		id: "p2",
		genFn: func(ctx context.Context, req *Request) (*Response, error) {
			return &Response{Content: "p2 success"}, nil
		},
	}

	failover := NewFailoverProvider(p1, p2)
	resp, err := failover.Generate(context.Background(), &Request{})
	if err != nil {
		t.Fatalf("failover failed: %v", err)
	}
	if resp.Content != "p2 success" {
		t.Errorf("expected p2 success, got %s", resp.Content)
	}
}

func TestFailoverProvider_ErrorClassificationUsesAnyProvider(t *testing.T) {
	failover := NewFailoverProvider(
		&mockProvider{
			id: "p1",
			isTransientFn: func(err error) bool {
				return false
			},
			isContextOverflowFn: func(err error) bool {
				return false
			},
		},
		&mockProvider{
			id: "p2",
			isTransientFn: func(err error) bool {
				return true
			},
			isContextOverflowFn: func(err error) bool {
				return true
			},
		},
	)

	if !failover.IsTransient(errors.New("rate limited")) {
		t.Fatal("expected transient classification to match any provider")
	}
	if !failover.IsContextOverflow(errors.New("context_length_exceeded")) {
		t.Fatal("expected overflow classification to match any provider")
	}
}

func TestFailoverProviderNoProviders(t *testing.T) {
	failover := NewFailoverProvider()

	if _, err := failover.Generate(t.Context(), &Request{}); err == nil ||
		err.Error() != "no providers configured" {
		t.Fatalf("Generate error = %v, want no providers configured", err)
	}
	if _, err := failover.Stream(t.Context(), &Request{}); err == nil ||
		err.Error() != "no providers configured" {
		t.Fatalf("Stream error = %v, want no providers configured", err)
	}
}

func TestSmartResolver_RateLimit(t *testing.T) {
	p1Calls := 0
	p1 := &mockProvider{
		id: "p1",
		genFn: func(ctx context.Context, req *Request) (*Response, error) {
			p1Calls++
			return nil, &openai.APIError{
				HTTPStatusCode: http.StatusTooManyRequests,
				Message:        "rate limited",
			}
		},
	}
	p2Calls := 0
	p2 := &mockProvider{
		id: "p2",
		genFn: func(ctx context.Context, req *Request) (*Response, error) {
			p2Calls++
			return &Response{Content: "p2 success"}, nil
		},
	}

	smart := NewSmartResolver(StrategyPriority, p1, p2)

	// First call: p1 should fail with rate limit, p2 should succeed
	resp, err := smart.Generate(context.Background(), &Request{})
	if err != nil {
		t.Fatalf("smart resolver failed: %v", err)
	}
	if resp.Content != "p2 success" {
		t.Errorf("expected p2 success, got %s", resp.Content)
	}
	if p1Calls != 1 || p2Calls != 1 {
		t.Errorf("expected 1 call to p1 and p2, got p1=%d, p2=%d", p1Calls, p2Calls)
	}

	// Second call: p1 should be cooling, p2 should be called directly
	resp, err = smart.Generate(context.Background(), &Request{})
	if err != nil {
		t.Fatalf("smart resolver failed on second call: %v", err)
	}
	if p1Calls != 1 || p2Calls != 2 {
		t.Errorf(
			"expected p1 to be skipped (1 call) and p2 to be called (2 calls), got p1=%d, p2=%d",
			p1Calls,
			p2Calls,
		)
	}
}

func TestSmartResolver_RoundRobin(t *testing.T) {
	p1Calls := 0
	p1 := &mockProvider{
		id: "p1",
		genFn: func(ctx context.Context, req *Request) (*Response, error) {
			p1Calls++
			return &Response{Content: "p1 success"}, nil
		},
	}
	p2Calls := 0
	p2 := &mockProvider{
		id: "p2",
		genFn: func(ctx context.Context, req *Request) (*Response, error) {
			p2Calls++
			return &Response{Content: "p2 success"}, nil
		},
	}

	smart := NewSmartResolver(StrategyRoundRobin, p1, p2)

	// Should rotate between p1 and p2
	// Note: current implementation uses atomic.AddUint32 which starts at 0, so 1%2=1, 2%2=0
	// So it might call p2 then p1 or vice-versa depending on the first Add.

	smart.Generate(context.Background(), &Request{})
	smart.Generate(context.Background(), &Request{})

	if p1Calls != 1 || p2Calls != 1 {
		t.Errorf("expected 1 call each to p1 and p2, got p1=%d, p2=%d", p1Calls, p2Calls)
	}
}

func TestSmartResolver_ErrorClassificationUsesAnyProvider(t *testing.T) {
	smart := NewSmartResolver(
		StrategyPriority,
		&mockProvider{
			id: "p1",
			isTransientFn: func(err error) bool {
				return false
			},
			isContextOverflowFn: func(err error) bool {
				return false
			},
		},
		&mockProvider{
			id: "p2",
			isTransientFn: func(err error) bool {
				return true
			},
			isContextOverflowFn: func(err error) bool {
				return true
			},
		},
	)

	if !smart.IsTransient(errors.New("rate limited")) {
		t.Fatal("expected transient classification to match any provider")
	}
	if !smart.IsContextOverflow(errors.New("context_length_exceeded")) {
		t.Fatal("expected overflow classification to match any provider")
	}
}

func TestSmartResolverExhaustedTransientProvidersWrapsCause(t *testing.T) {
	transient := errors.New("temporary outage")
	smart := NewSmartResolver(StrategyPriority, &mockProvider{
		id: "p1",
		genFn: func(context.Context, *Request) (*Response, error) {
			return nil, transient
		},
		isTransientFn: func(err error) bool {
			return errors.Is(err, transient)
		},
	})

	_, err := smart.Generate(t.Context(), &Request{})
	if !errors.Is(err, transient) {
		t.Fatalf("Generate error = %v, want wrapping %v", err, transient)
	}
}

func TestSmartResolver_CoolsProviderAfterTransientStreamError(t *testing.T) {
	transient := errors.New("stream interrupted")
	p1Calls := 0
	p1 := &mockProvider{
		id: "p1",
		streamFn: func(context.Context, *Request) (Stream, error) {
			p1Calls++
			stream := NewFauxStream(Chunk{Content: "partial"})
			stream.err = transient
			return stream, nil
		},
		isTransientFn: func(err error) bool {
			return errors.Is(err, transient)
		},
	}
	p2Calls := 0
	p2 := &mockProvider{
		id: "p2",
		streamFn: func(context.Context, *Request) (Stream, error) {
			p2Calls++
			return NewFauxStream(Chunk{Content: "fallback"}), nil
		},
	}

	smart := NewSmartResolver(StrategyPriority, p1, p2)
	stream, err := smart.Stream(t.Context(), &Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, err := GenerateFromStream(stream); !errors.Is(err, transient) {
		t.Fatalf("GenerateFromStream error = %v, want %v", err, transient)
	}

	stream, err = smart.Stream(t.Context(), &Request{})
	if err != nil {
		t.Fatalf("second Stream: %v", err)
	}
	if _, err := GenerateFromStream(stream); err != nil {
		t.Fatalf("second GenerateFromStream: %v", err)
	}
	if p1Calls != 1 || p2Calls != 1 {
		t.Fatalf("expected p1 cooled and p2 used, got p1=%d p2=%d", p1Calls, p2Calls)
	}
}

func TestIsRateLimit(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "openai 429",
			err:  &openai.APIError{HTTPStatusCode: 429},
			want: true,
		},
		{
			name: "openai 401",
			err:  &openai.APIError{HTTPStatusCode: 401},
			want: false,
		},
		{
			name: "generic status coder 429",
			err:  statusErr(429),
			want: true,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRateLimit(tt.err); got != tt.want {
				t.Errorf("IsRateLimit() = %v, want %v", got, tt.want)
			}
		})
	}
}

type statusErr int

func (e statusErr) Error() string   { return "error" }
func (e statusErr) StatusCode() int { return int(e) }
