package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// mockProvider queues responses and returns them in order.
type mockProvider struct {
	llm.Provider
	responses []*llm.LLMResponse
}

func (m *mockProvider) ID() string                             { return "mock" }
func (m *mockProvider) Capabilities(_ string) llm.Capabilities { return llm.DefaultCapabilities() }

func (m *mockProvider) Generate(
	ctx context.Context,
	req *llm.LLMRequest,
) (*llm.LLMResponse, error) {
	if len(m.responses) == 0 {
		return &llm.LLMResponse{Content: "no more responses"}, nil
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return resp, nil
}

// simpleTool is an inline tool for use in tests.
type simpleTool struct {
	name   string
	output string
}

func (t *simpleTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        t.name,
		Description: "A simple test tool",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (t *simpleTool) Execute(_ context.Context, _ string) (string, error) {
	return t.output, nil
}

// userSession returns a session with a single user message appended.
func userSession(id, content string) *session.Session {
	s := session.New(id)
	s.Append(session.NewEvent(id, session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: content,
	}))
	return s
}

// ---------------------------------------------------------------------------
// HandoffTool — constructor, Spec, Execute
// ---------------------------------------------------------------------------

func TestHandoffToolName(t *testing.T) {
	ht := HandoffTool("writer")
	spec := ht.Spec()
	if spec.Name != "transfer_to_writer" {
		t.Errorf("expected name 'transfer_to_writer', got '%s'", spec.Name)
	}
}

func TestHandoffToolSpecContainsTarget(t *testing.T) {
	ht := HandoffTool("editor")
	spec := ht.Spec()
	if !strings.Contains(spec.Description, "editor") {
		t.Errorf(
			"expected description to contain target agent id 'editor', got '%s'",
			spec.Description,
		)
	}
	if spec.Parameters == nil {
		t.Error("expected non-nil parameters")
	}
}

func TestHandoffToolExecuteHappyPath(t *testing.T) {
	ht := HandoffTool("writer")
	args := `{"reason":"task complete","context":"draft ready"}`
	out, err := ht.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var h Handoff
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if h.TargetAgentID != "writer" {
		t.Errorf("expected target 'writer', got '%s'", h.TargetAgentID)
	}
	if h.Reason != "task complete" {
		t.Errorf("expected reason 'task complete', got '%s'", h.Reason)
	}
	if h.Context != "draft ready" {
		t.Errorf("expected context 'draft ready', got '%s'", h.Context)
	}
}

func TestHandoffToolExecuteInvalidJSON(t *testing.T) {
	ht := HandoffTool("writer")
	_, err := ht.Execute(context.Background(), "not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON args, got nil")
	}
}

func TestHandoffToolExecuteReasonOnly(t *testing.T) {
	ht := HandoffTool("reviewer")
	args := `{"reason":"needs review"}`
	out, err := ht.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var h Handoff
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if h.TargetAgentID != "reviewer" {
		t.Errorf("expected target 'reviewer', got '%s'", h.TargetAgentID)
	}
}

// ---------------------------------------------------------------------------
// extractHandoff
// ---------------------------------------------------------------------------

func TestExtractHandoffEmptyTargetIDs(t *testing.T) {
	s := userSession("s1", "hello")
	if extractHandoff(s, nil) != nil {
		t.Error("expected nil when targetIDs is empty")
	}
	if extractHandoff(s, []string{}) != nil {
		t.Error("expected nil when targetIDs is an empty slice")
	}
}

func TestExtractHandoffMatchesKnownTarget(t *testing.T) {
	s := session.New("s2")

	// Append an assistant message with a tool call.
	assistantMsg := llm.Message{
		Role:    llm.RoleAssistant,
		Content: "",
		Calls: []llm.ToolCall{{
			ID: "c1",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "transfer_to_writer", Arguments: `{"reason":"done"}`},
		}},
	}
	s.Append(session.NewEvent("s2", session.EventTypeMessageAdded, assistantMsg))

	// Append the tool result containing a Handoff payload.
	h := Handoff{TargetAgentID: "writer", Reason: "done"}
	raw, _ := json.Marshal(h)
	toolMsg := llm.Message{
		Role:    llm.RoleTool,
		Content: string(raw),
		ToolID:  "c1",
		Name:    "transfer_to_writer",
	}
	s.Append(session.NewEvent("s2", session.EventTypeMessageAdded, toolMsg))

	got := extractHandoff(s, []string{"writer"})
	if got == nil {
		t.Fatal("expected non-nil Handoff")
	}
	if got.TargetAgentID != "writer" {
		t.Errorf("expected target 'writer', got '%s'", got.TargetAgentID)
	}
}

func TestExtractHandoffSkipsUnknownTarget(t *testing.T) {
	s := session.New("s3")

	// Tool result with a target that is NOT in the known list.
	h := Handoff{TargetAgentID: "unknown-agent", Reason: "nope"}
	raw, _ := json.Marshal(h)
	toolMsg := llm.Message{
		Role:    llm.RoleTool,
		Content: string(raw),
		ToolID:  "c2",
		Name:    "transfer_to_unknown-agent",
	}
	s.Append(session.NewEvent("s3", session.EventTypeMessageAdded, toolMsg))

	got := extractHandoff(s, []string{"writer"})
	if got != nil {
		t.Errorf("expected nil for unknown target, got %+v", got)
	}
}

func TestExtractHandoffStopsAtNonToolRole(t *testing.T) {
	s := session.New("s4")

	// Append an assistant message (non-tool role).
	assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: "hello"}
	s.Append(session.NewEvent("s4", session.EventTypeMessageAdded, assistantMsg))

	// A tool result that would match, but placed before the scan starts
	// (the scanner walks backward and stops at non-tool roles).
	// Since the last message is assistant, no tool block is scanned at all.
	got := extractHandoff(s, []string{"writer"})
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// RecordHandoff
// ---------------------------------------------------------------------------

func TestRecordHandoff(t *testing.T) {
	s := session.New("s5")
	h := &Handoff{TargetAgentID: "writer", Reason: "done"}
	RecordHandoff(s, h)

	events := s.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != session.EventTypeHandoff {
		t.Errorf("expected event type 'handoff', got '%s'", events[0].Type)
	}

	var recorded Handoff
	if err := json.Unmarshal(events[0].Data, &recorded); err != nil {
		t.Fatalf("failed to unmarshal handoff event data: %v", err)
	}
	if recorded.TargetAgentID != "writer" {
		t.Errorf("expected target 'writer', got '%s'", recorded.TargetAgentID)
	}
}

// ---------------------------------------------------------------------------
// loop.go — Step paths
// ---------------------------------------------------------------------------

func TestStepNoToolCalls(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.LLMResponse{
			{Content: "Hello! How can I help?"},
		},
	}
	a := New("test-agent", "You are helpful.", "gpt-4", p, nil)
	s := userSession("s6", "Hi!")

	result, err := a.Step(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Handoff != nil {
		t.Errorf("expected no handoff, got %+v", result.Handoff)
	}

	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].Content != "Hello! How can I help?" {
		t.Errorf("unexpected assistant content: %s", msgs[1].Content)
	}
}

func TestStepWithToolCallAndRegistry(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&simpleTool{name: "echo", output: "pong"})

	p := &mockProvider{
		responses: []*llm.LLMResponse{
			{
				Content: "",
				Calls: []llm.ToolCall{{
					ID:   "c1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "echo", Arguments: `{}`},
				}},
			},
		},
	}
	a := New("test-agent", "You are helpful.", "gpt-4", p, reg)
	s := userSession("s7", "ping")

	result, err := a.Step(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Handoff != nil {
		t.Errorf("expected no handoff, got %+v", result.Handoff)
	}

	msgs := s.Messages()
	// user, assistant (with call), tool result
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	last := msgs[2]
	if last.Role != llm.RoleTool {
		t.Errorf("expected last role 'tool', got '%s'", last.Role)
	}
	if last.Content != "pong" {
		t.Errorf("expected tool content 'pong', got '%s'", last.Content)
	}
}

func TestStepNoRegistryReturnsErrorString(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.LLMResponse{
			{
				Content: "",
				Calls: []llm.ToolCall{{
					ID:   "c1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "some_tool", Arguments: `{}`},
				}},
			},
		},
	}
	// nil registry — Step should produce an error string in the tool result.
	a := New("test-agent", "You are helpful.", "gpt-4", p, nil)
	s := userSession("s8", "use tool")

	result, err := a.Step(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Handoff != nil {
		t.Errorf("expected no handoff, got %+v", result.Handoff)
	}

	msgs := s.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (user, assistant, tool-error), got %d", len(msgs))
	}
	toolMsg := msgs[2]
	if !strings.Contains(toolMsg.Content, "Error") {
		t.Errorf("expected error string in tool result, got '%s'", toolMsg.Content)
	}
}

// ---------------------------------------------------------------------------
// loop.go — Turn paths
// ---------------------------------------------------------------------------

func TestAgentTurn(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.LLMResponse{
			{Content: "Hello! How can I help?"},
		},
	}

	a := New("test-agent", "You are a helpful assistant.", "gpt-4", p, nil)
	s := userSession("test-session", "Hi!")

	_, err := a.Turn(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}

	messages := s.Messages()
	if len(messages) != 2 {
		t.Errorf("expected 2 messages (user + assistant), got %d", len(messages))
	}
	if messages[1].Content != "Hello! How can I help?" {
		t.Errorf("expected response 'Hello! How can I help?', got '%s'", messages[1].Content)
	}
}

func TestTurnMaxStepsReturnsError(t *testing.T) {
	// Provider always returns a tool call so the loop never terminates naturally.
	makeToolResponse := func() *llm.LLMResponse {
		return &llm.LLMResponse{
			Content: "",
			Calls: []llm.ToolCall{{
				ID:   "c1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "echo", Arguments: `{}`},
			}},
		}
	}

	reg := tool.NewRegistry()
	reg.Register(&simpleTool{name: "echo", output: "pong"})

	// Queue enough responses to exhaust MaxSteps.
	maxSteps := 3
	responses := make([]*llm.LLMResponse, maxSteps)
	for i := range responses {
		responses[i] = makeToolResponse()
	}
	p := &mockProvider{responses: responses}

	a := New("test-agent", "You are helpful.", "gpt-4", p, reg)
	a.MaxSteps = maxSteps
	s := userSession("s9", "loop forever")

	_, err := a.Turn(context.Background(), s)
	if err == nil {
		t.Fatal("expected MaxSteps error, got nil")
	}
	if !errors.Is(err, ErrMaxSteps) {
		t.Errorf("expected errors.Is(err, ErrMaxSteps), got %v", err)
	}
}

func TestTurnPopulatesContent(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.LLMResponse{
			{Content: "final answer"},
		},
	}
	a := New("a", "sys", "m", p, nil)
	s := userSession("sc", "question")

	result, err := a.Turn(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final answer" {
		t.Fatalf("Content = %q, want %q", result.Content, "final answer")
	}
}

func TestWithMaxSteps(t *testing.T) {
	a := New("a", "", "m", &mockProvider{}, nil, WithMaxSteps(5))
	if a.MaxSteps != 5 {
		t.Fatalf("MaxSteps = %d, want 5", a.MaxSteps)
	}
}

func TestWithModel(t *testing.T) {
	a := New("a", "", "base", &mockProvider{}, nil, WithModel("gpt-4o"))
	if a.Model != "gpt-4o" {
		t.Fatalf("Model = %q, want gpt-4o", a.Model)
	}
}

func TestTurnHandoffStopsEarly(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(HandoffTool("writer"))

	// First response: LLM calls the handoff tool.
	p := &mockProvider{
		responses: []*llm.LLMResponse{
			{
				Content: "",
				Calls: []llm.ToolCall{{
					ID:   "c1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      "transfer_to_writer",
						Arguments: `{"reason":"task complete","context":"draft ready"}`,
					},
				}},
			},
		},
	}

	a := New("test-agent", "You are helpful.", "gpt-4", p, reg)
	s := userSession("s10", "write something")

	result, err := a.Turn(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Handoff == nil {
		t.Fatal("expected non-nil Handoff in result")
	}
	if result.Handoff.TargetAgentID != "writer" {
		t.Errorf("expected handoff target 'writer', got '%s'", result.Handoff.TargetAgentID)
	}
	if result.Handoff.Reason != "task complete" {
		t.Errorf("expected reason 'task complete', got '%s'", result.Handoff.Reason)
	}
}

func TestAgentToolTurn(t *testing.T) {
	// Verifies a two-step turn: tool call then assistant text response.
	reg := tool.NewRegistry()
	reg.Register(&simpleTool{name: "echo", output: "world"})

	p := &mockProvider{
		responses: []*llm.LLMResponse{
			{
				Content: "",
				Calls: []llm.ToolCall{{
					ID:   "c1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "echo", Arguments: `{}`},
				}},
			},
			{Content: "Tool called, result was: world"},
		},
	}

	a := New("test-agent", "You are helpful.", "gpt-4", p, reg)
	s := userSession("s11", "say hello")

	result, err := a.Turn(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Handoff != nil {
		t.Errorf("expected no handoff, got %+v", result.Handoff)
	}

	msgs := s.Messages()
	// user, assistant (tool call), tool result, assistant (final)
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	final := msgs[3]
	if final.Role != llm.RoleAssistant {
		t.Errorf("expected final role 'assistant', got '%s'", final.Role)
	}
	if final.Content != "Tool called, result was: world" {
		t.Errorf("unexpected final content: %s", final.Content)
	}
}
