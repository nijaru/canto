package llm_test

import (
	"context"
	"testing"

	"github.com/nijaru/canto/llm"
)

type mockClassifierProvider struct {
	Response string
	Usage    llm.Usage
	Err      error
}

func (m *mockClassifierProvider) ID() string { return "mock" }

func (m *mockClassifierProvider) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return &llm.Response{Content: m.Response, Usage: m.Usage}, nil
}

func (m *mockClassifierProvider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return nil, nil
}

func (m *mockClassifierProvider) Models(
	ctx context.Context,
) ([]llm.Model, error) {
	return nil, nil
}

func (m *mockClassifierProvider) CountTokens(
	ctx context.Context,
	model string,
	messages []llm.Message,
) (int, error) {
	return 0, nil
}

func (m *mockClassifierProvider) Cost(ctx context.Context, model string, usage llm.Usage) float64 {
	return 0
}

func (m *mockClassifierProvider) Capabilities(model string) llm.Capabilities {
	return llm.Capabilities{}
}
func (m *mockClassifierProvider) IsTransient(err error) bool       { return false }
func (m *mockClassifierProvider) IsContextOverflow(err error) bool { return false }

func TestStandardClassifier(t *testing.T) {
	pr := &mockClassifierProvider{
		Response: `{"label": "allow", "reason": "safe tool call"}`,
		Usage:    llm.Usage{TotalTokens: 100},
	}
	classifier := llm.NewStandardClassifier(pr, "model", "prompt")

	res, err := classifier.Classify(context.Background(), "input", []string{"allow", "deny"})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	if res.Label != "allow" {
		t.Errorf("got label %q, want allow", res.Label)
	}
	if res.Reason != "safe tool call" {
		t.Errorf("got reason %q, want safe tool call", res.Reason)
	}
	if res.Usage.TotalTokens != 100 {
		t.Errorf("got tokens %d, want 100", res.Usage.TotalTokens)
	}
}

func TestStandardClassifier_InvalidJSON(t *testing.T) {
	pr := &mockClassifierProvider{
		Response: `not json`,
	}
	classifier := llm.NewStandardClassifier(pr, "model", "prompt")

	_, err := classifier.Classify(context.Background(), "input", []string{"allow", "deny"})
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}
