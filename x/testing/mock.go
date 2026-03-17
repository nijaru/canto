// Package testing provides test utilities for agents built with canto.
package testing

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"charm.land/catwalk/pkg/catwalk"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Step is a pre-programmed LLM response returned by MockProvider.
type Step struct {
	Content        string
	Reasoning      string
	ThinkingBlocks []llm.ThinkingBlock
	Calls          []llm.Call
	Err            error
	// Chunks, if set, causes Stream() to return these chunks instead of using
	// Content/Calls. Use this to test streaming code paths.
	Chunks []llm.Chunk
}

// MockProvider is a programmable llm.Provider for testing agent logic
// without making real API calls. Responses are consumed in order.
type MockProvider struct {
	mu    sync.Mutex
	id    string
	steps []Step
	pos   int
	calls []*llm.Request // record of all Generate calls
}

// NewMockProvider creates a MockProvider with the given step sequence.
func NewMockProvider(id string, steps ...Step) *MockProvider {
	return &MockProvider{id: id, steps: steps}
}

func (m *MockProvider) ID() string { return m.id }

// Generate returns the next pre-programmed step. Fails the test if steps are exhausted.
func (m *MockProvider) Generate(_ context.Context, req *llm.Request) (*llm.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, req)

	if m.pos >= len(m.steps) {
		return nil, fmt.Errorf(
			"MockProvider: no more steps (called %d times, have %d)",
			m.pos+1,
			len(m.steps),
		)
	}
	s := m.steps[m.pos]
	m.pos++

	if s.Err != nil {
		return nil, s.Err
	}
	return &llm.Response{
		Content:        s.Content,
		Reasoning:      s.Reasoning,
		ThinkingBlocks: s.ThinkingBlocks,
		Calls:          s.Calls,
	}, nil
}

// Stream returns a MockStream built from the next step's Chunks.
// If the step has no Chunks set, it synthesises a single content chunk from
// the step's Content and Calls, so streaming and non-streaming tests can use
// the same Step definitions when chunk granularity doesn't matter.
func (m *MockProvider) Stream(_ context.Context, req *llm.Request) (llm.Stream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, req)

	if m.pos >= len(m.steps) {
		return nil, fmt.Errorf(
			"MockProvider: no more steps (called %d times, have %d)",
			m.pos+1,
			len(m.steps),
		)
	}
	s := m.steps[m.pos]
	m.pos++

	if s.Err != nil {
		return nil, s.Err
	}
	chunks := s.Chunks
	if chunks == nil {
		// Synthesise from Content/Calls for tests that don't need chunk granularity.
		chunks = []llm.Chunk{{Content: s.Content, Calls: s.Calls}}
	}
	return &MockStream{chunks: chunks}, nil
}

// MockStream is a pre-programmed llm.Stream for testing streaming code paths.
type MockStream struct {
	chunks []llm.Chunk
	pos    int
	err    error
}

// NewMockStream creates a MockStream that emits the given chunks in order.
func NewMockStream(chunks ...llm.Chunk) *MockStream {
	return &MockStream{chunks: chunks}
}

func (s *MockStream) Next() (*llm.Chunk, bool) {
	if s.pos >= len(s.chunks) {
		return nil, false
	}
	c := s.chunks[s.pos]
	s.pos++
	return &c, true
}

func (s *MockStream) Err() error   { return s.err }
func (s *MockStream) Close() error { return nil }

// Models returns an empty list.
func (m *MockProvider) Models(_ context.Context) ([]catwalk.Model, error) {
	return nil, nil
}

// CountTokens returns 0.
func (m *MockProvider) CountTokens(_ context.Context, _ string, _ []llm.Message) (int, error) {
	return 0, nil
}

// Cost returns 0.
func (m *MockProvider) Cost(_ context.Context, _ string, _ llm.Usage) float64 { return 0 }

// Capabilities returns default capabilities.
func (m *MockProvider) Capabilities(_ string) llm.Capabilities {
	return llm.DefaultCapabilities()
}

func (m *MockProvider) IsTransient(_ error) bool { return false }

// Calls returns all requests processed by the provider.
func (m *MockProvider) Calls() []*llm.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*llm.Request, len(m.calls))
	copy(out, m.calls)
	return out
}

// Remaining returns the number of unconsumed steps.
func (m *MockProvider) Remaining() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.steps) - m.pos
}

// AssertExhausted fails t if there are unconsumed steps.
func (m *MockProvider) AssertExhausted(t *testing.T) {
	t.Helper()
	if r := m.Remaining(); r != 0 {
		t.Errorf("MockProvider %q: %d step(s) not consumed", m.id, r)
	}
}

// AssertToolCalled fails t if toolName was not called in the session event log.
func AssertToolCalled(t *testing.T, sess *session.Session, toolName string) {
	t.Helper()
	for _, e := range sess.Events() {
		if e.Type != session.MessageAdded {
			continue
		}
		var msg llm.Message
		if err := unmarshalJSON(e.Data, &msg); err != nil {
			continue
		}
		for _, call := range msg.Calls {
			if call.Function.Name == toolName {
				return
			}
		}
	}
	t.Errorf("tool %q was never called in session %q", toolName, sess.ID())
}

// AssertToolNotCalled fails t if toolName was called in the session event log.
func AssertToolNotCalled(t *testing.T, sess *session.Session, toolName string) {
	t.Helper()
	for _, e := range sess.Events() {
		if e.Type != session.MessageAdded {
			continue
		}
		var msg llm.Message
		if err := unmarshalJSON(e.Data, &msg); err != nil {
			continue
		}
		for _, call := range msg.Calls {
			if call.Function.Name == toolName {
				t.Errorf(
					"tool %q was called but should not have been (session %q)",
					toolName,
					sess.ID(),
				)
				return
			}
		}
	}
}
