package testing

import (
	"context"
	"errors"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func TestMockProvider_ConsumeSteps(t *testing.T) {
	mock := NewMockProvider("test",
		Step{Content: "step 1"},
		Step{Content: "step 2"},
	)

	resp, err := mock.Generate(context.Background(), &llm.LLMRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "step 1" {
		t.Fatalf("content = %q, want step 1", resp.Content)
	}

	resp, err = mock.Generate(context.Background(), &llm.LLMRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "step 2" {
		t.Fatalf("content = %q, want step 2", resp.Content)
	}

	if mock.Remaining() != 0 {
		t.Fatalf("remaining = %d, want 0", mock.Remaining())
	}
}

func TestMockProvider_Exhausted(t *testing.T) {
	mock := NewMockProvider("test", Step{Content: "only"})
	mock.Generate(context.Background(), &llm.LLMRequest{}) //nolint

	_, err := mock.Generate(context.Background(), &llm.LLMRequest{})
	if err == nil {
		t.Fatal("expected error when steps exhausted")
	}
}

func TestMockProvider_StepError(t *testing.T) {
	want := errors.New("provider down")
	mock := NewMockProvider("test", Step{Err: want})

	_, err := mock.Generate(context.Background(), &llm.LLMRequest{})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestMockProvider_RecordsCalls(t *testing.T) {
	mock := NewMockProvider("test", Step{Content: "ok"})

	req := &llm.LLMRequest{Model: "gpt-4o"}
	mock.Generate(context.Background(), req) //nolint

	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls len = %d, want 1", len(calls))
	}
	if calls[0].Model != "gpt-4o" {
		t.Fatalf("model = %q, want gpt-4o", calls[0].Model)
	}
}

func TestMockProvider_AssertExhausted(t *testing.T) {
	mock := NewMockProvider("test", Step{Content: "unused"})

	inner := &testing.T{}
	mock.AssertExhausted(inner)
	if !inner.Failed() {
		t.Fatal("expected AssertExhausted to fail with unconsumed steps")
	}
}

func TestAssertToolCalled(t *testing.T) {
	sess := session.New("s1")
	msg := llm.Message{
		Role: llm.RoleAssistant,
		Calls: []llm.ToolCall{
			{ID: "1", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "bash", Arguments: "{}"}},
		},
	}
	_ = sess.Append(
		context.Background(),
		session.NewEvent("s1", session.EventTypeMessageAdded, msg),
	)

	inner := &testing.T{}
	AssertToolCalled(inner, sess, "bash")
	if inner.Failed() {
		t.Fatal("expected AssertToolCalled to pass")
	}

	inner2 := &testing.T{}
	AssertToolCalled(inner2, sess, "read_file")
	if !inner2.Failed() {
		t.Fatal("expected AssertToolCalled to fail for uncalled tool")
	}
}

func TestAssertToolNotCalled(t *testing.T) {
	sess := session.New("s2")

	inner := &testing.T{}
	AssertToolNotCalled(inner, sess, "bash")
	if inner.Failed() {
		t.Fatal("expected AssertToolNotCalled to pass on empty session")
	}
}
