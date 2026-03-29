package llm

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/sashabaranov/go-openai"
)

type mockProvider struct {
	id     string
	genFn  func(ctx context.Context, req *Request) (*Response, error)
	models []Model
}

func (m *mockProvider) ID() string { return m.id }
func (m *mockProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	return m.genFn(ctx, req)
}

func (m *mockProvider) Stream(ctx context.Context, req *Request) (Stream, error) {
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
func (m *mockProvider) IsTransient(err error) bool         { return IsRateLimit(err) }

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
