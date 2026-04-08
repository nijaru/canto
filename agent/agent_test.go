package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// mockProvider queues responses and returns them in order.
type mockProvider struct {
	llm.Provider
	responses []*llm.Response
}

func (m *mockProvider) ID() string                             { return "mock" }
func (m *mockProvider) Capabilities(_ string) llm.Capabilities { return llm.DefaultCapabilities() }
func (m *mockProvider) IsTransient(_ error) bool               { return false }

func (m *mockProvider) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	if len(m.responses) == 0 {
		return &llm.Response{Content: "no more responses"}, nil
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return resp, nil
}

type flakyProvider struct {
	mockProvider
	failures int
}

func (m *flakyProvider) Generate(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if m.failures > 0 {
		m.failures--
		return nil, fmt.Errorf("transient provider failure")
	}
	return m.mockProvider.Generate(ctx, req)
}

func (m *flakyProvider) IsTransient(err error) bool {
	return err != nil && strings.Contains(err.Error(), "transient")
}

// simpleTool is an inline tool for use in tests.
type simpleTool struct {
	name   string
	output string
}

func (t *simpleTool) Spec() llm.Spec {
	return llm.Spec{
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
	_ = s.Append(
		context.Background(),
		session.NewEvent(id, session.MessageAdded, llm.Message{
			Role:    llm.RoleUser,
			Content: content,
		}),
	)
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
		Calls: []llm.Call{{
			ID: "c1",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "transfer_to_writer", Arguments: `{"reason":"done"}`},
		}},
	}
	_ = s.Append(
		context.Background(),
		session.NewEvent("s2", session.MessageAdded, assistantMsg),
	)

	// Append the tool result containing a Handoff payload.
	h := Handoff{TargetAgentID: "writer", Reason: "done"}
	raw, _ := json.Marshal(h)
	toolMsg := llm.Message{
		Role:    llm.RoleTool,
		Content: string(raw),
		ToolID:  "c1",
		Name:    "transfer_to_writer",
	}
	_ = s.Append(
		context.Background(),
		session.NewEvent("s2", session.MessageAdded, toolMsg),
	)

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
	_ = s.Append(
		context.Background(),
		session.NewEvent("s3", session.MessageAdded, toolMsg),
	)

	got := extractHandoff(s, []string{"writer"})
	if got != nil {
		t.Errorf("expected nil for unknown target, got %+v", got)
	}
}

func TestExtractHandoffStopsAtNonToolRole(t *testing.T) {
	s := session.New("s4")

	// Append an assistant message (non-tool role).
	assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: "hello"}
	_ = s.Append(
		context.Background(),
		session.NewEvent("s4", session.MessageAdded, assistantMsg),
	)

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
	_ = RecordHandoff(context.Background(), s, h)

	events := s.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != session.Handoff {
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
		responses: []*llm.Response{
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

func TestStepRecordsPromptCacheFingerprint(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.Response{
			{Content: "first"},
			{Content: "second"},
		},
	}
	a := New("test-agent", "You are helpful.", "gpt-4", p, nil)
	s := userSession("cache-session", "Hi!")

	if _, err := a.Step(context.Background(), s); err != nil {
		t.Fatalf("first step: %v", err)
	}
	if _, err := a.Step(context.Background(), s); err != nil {
		t.Fatalf("second step: %v", err)
	}

	var fingerprints []session.PromptCacheData
	for _, e := range s.Events() {
		if e.Type != session.StepStarted {
			continue
		}
		data, ok, err := e.StepStartedData()
		if err != nil {
			t.Fatalf("decode step started data: %v", err)
		}
		if !ok {
			t.Fatal("expected step started data")
		}
		if data.PromptCache == (session.PromptCacheData{}) {
			t.Fatal("expected prompt cache fingerprint on step start")
		}
		fingerprints = append(fingerprints, data.PromptCache)
	}

	if len(fingerprints) != 2 {
		t.Fatalf("expected 2 step fingerprints, got %d", len(fingerprints))
	}
	if fingerprints[0] != fingerprints[1] {
		t.Fatalf(
			"expected stable fingerprint across ordinary history growth, got %v then %v",
			fingerprints[0],
			fingerprints[1],
		)
	}
}

func TestStepWithToolCallAndRegistry(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&simpleTool{name: "echo", output: "pong"})

	p := &mockProvider{
		responses: []*llm.Response{
			{
				Content: "",
				Calls: []llm.Call{{
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

func TestStepPreToolHookCanRewriteCall(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(tool.FuncWithMetadata(
		"echo",
		"Echo args",
		map[string]any{"type": "object"},
		tool.Metadata{Category: "workspace", ReadOnly: true},
		func(_ context.Context, args string) (string, error) {
			return args, nil
		},
	))

	runner := hook.NewRunner()
	runner.Register(hook.NewFunc(
		"rewrite-call",
		[]hook.Event{hook.EventPreToolUse},
		func(_ context.Context, p *hook.Payload) *hook.Result {
			got, ok := p.Data["metadata"].(tool.Metadata)
			if !ok {
				t.Fatalf("expected tool metadata payload, got %#v", p.Data["metadata"])
			}
			if got.Category != "workspace" || !got.ReadOnly {
				t.Fatalf("unexpected metadata payload: %#v", got)
			}
			return &hook.Result{
				Action: hook.ActionProceed,
				Data: map[string]any{
					"args": `{"rewritten":true}`,
				},
			}
		},
	))

	p := &mockProvider{
		responses: []*llm.Response{
			{
				Content: "",
				Calls: []llm.Call{{
					ID:   "c1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "echo", Arguments: `{"original":true}`},
				}},
			},
		},
	}
	a := New("test-agent", "You are helpful.", "gpt-4", p, reg)
	a.hooks = runner
	s := userSession("s-hook-rewrite", "use tool")

	if _, err := a.Step(context.Background(), s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := s.Messages()
	if got := msgs[len(msgs)-1].Content; got != `{"rewritten":true}` {
		t.Fatalf("expected rewritten tool args in result, got %q", got)
	}

	for _, event := range s.Events() {
		if event.Type != session.ToolStarted {
			continue
		}
		data, ok, err := event.ToolStartedData()
		if err != nil {
			t.Fatalf("decode tool started data: %v", err)
		}
		if !ok {
			t.Fatal("expected tool started data")
		}
		if data.Arguments != `{"rewritten":true}` {
			t.Fatalf("expected rewritten args in tool_started event, got %q", data.Arguments)
		}
		return
	}
	t.Fatal("expected tool_started event")
}

func TestStepPostToolHookCanRewriteOutput(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(tool.Func(
		"echo",
		"Echo secret",
		map[string]any{"type": "object"},
		func(_ context.Context, _ string) (string, error) {
			return "secret-output", nil
		},
	))

	runner := hook.NewRunner()
	runner.Register(hook.NewFunc(
		"redact-output",
		[]hook.Event{hook.EventPostToolUse},
		func(_ context.Context, _ *hook.Payload) *hook.Result {
			return &hook.Result{
				Action: hook.ActionProceed,
				Data: map[string]any{
					"output": "[redacted]",
				},
			}
		},
	))

	p := &mockProvider{
		responses: []*llm.Response{
			{
				Content: "",
				Calls: []llm.Call{{
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
	a.hooks = runner
	s := userSession("s-hook-redact", "use tool")

	if _, err := a.Step(context.Background(), s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := s.Messages()
	if got := msgs[len(msgs)-1].Content; got != "[redacted]" {
		t.Fatalf("expected redacted tool output, got %q", got)
	}

	for _, event := range s.Events() {
		if event.Type != session.ToolCompleted {
			continue
		}
		data, ok, err := event.ToolCompletedData()
		if err != nil {
			t.Fatalf("decode tool completed data: %v", err)
		}
		if !ok {
			t.Fatal("expected tool completed data")
		}
		if data.Output != "[redacted]" {
			t.Fatalf("expected redacted output in tool_completed event, got %q", data.Output)
		}
		return
	}
	t.Fatal("expected tool_completed event")
}

func TestStepNoRegistryReturnsErrorString(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.Response{
			{
				Content: "",
				Calls: []llm.Call{{
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
		responses: []*llm.Response{
			{Content: "Hello! How can I help?"},
		},
	}

	a := New("test-agent", "You are a helpful assistant.", "gpt-4", p, nil)
	s := userSession("test-session", "Hi!")

	result, err := a.Turn(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if result.TurnStopReason != TurnStopCompleted {
		t.Fatalf("expected turn stop reason %q, got %q", TurnStopCompleted, result.TurnStopReason)
	}

	messages := s.Messages()
	if len(messages) != 2 {
		t.Errorf("expected 2 messages (user + assistant), got %d", len(messages))
	}
	if messages[1].Content != "Hello! How can I help?" {
		t.Errorf("expected response 'Hello! How can I help?', got '%s'", messages[1].Content)
	}
}

func TestTurnMaxStepsSetsTurnStopReason(t *testing.T) {
	// Provider always returns a tool call so the loop never terminates naturally.
	makeToolResponse := func() *llm.Response {
		return &llm.Response{
			Content: "",
			Calls: []llm.Call{{
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
	responses := make([]*llm.Response, maxSteps)
	for i := range responses {
		responses[i] = makeToolResponse()
	}
	p := &mockProvider{responses: responses}

	a := New("test-agent", "You are helpful.", "gpt-4", p, reg)
	a.maxSteps = maxSteps
	s := userSession("s9", "loop forever")

	result, err := a.Turn(context.Background(), s)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result.TurnStopReason != TurnStopMaxTurnsHit {
		t.Fatalf("expected turn stop reason %q, got %q", TurnStopMaxTurnsHit, result.TurnStopReason)
	}
}

func TestTurnPopulatesContent(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.Response{
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

func TestTurnMaxSteps_PreservesUsage(t *testing.T) {
	makeToolResponse := func() *llm.Response {
		return &llm.Response{
			Calls: []llm.Call{{
				ID:   "c1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "echo", Arguments: `{}`},
			}},
			Usage: llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		}
	}

	reg := tool.NewRegistry()
	reg.Register(&simpleTool{name: "echo", output: "pong"})

	maxSteps := 2
	responses := make([]*llm.Response, maxSteps)
	for i := range responses {
		responses[i] = makeToolResponse()
	}
	p := &mockProvider{responses: responses}

	a := New("test-agent", "You are helpful.", "gpt-4", p, reg)
	a.maxSteps = maxSteps
	s := userSession("s-maxsteps-usage", "loop forever")

	result, err := a.Turn(context.Background(), s)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	// Each step contributes TotalTokens=15; total across maxSteps=2 should be 30.
	want := 15 * maxSteps
	if int(result.Usage.TotalTokens) != want {
		t.Errorf(
			"Usage.TotalTokens = %d, want %d (accumulated across all steps)",
			result.Usage.TotalTokens,
			want,
		)
	}
	if result.TurnStopReason != TurnStopMaxTurnsHit {
		t.Fatalf("expected turn stop reason %q, got %q", TurnStopMaxTurnsHit, result.TurnStopReason)
	}
}

func TestWithMaxSteps(t *testing.T) {
	a := New("a", "", "m", &mockProvider{}, nil, WithMaxSteps(5))
	if a.maxSteps != 5 {
		t.Fatalf("maxSteps = %d, want 5", a.maxSteps)
	}
}

func TestWithModel(t *testing.T) {
	a := New("a", "", "base", &mockProvider{}, nil, WithModel("gpt-4o"))
	if a.model != "gpt-4o" {
		t.Fatalf("model = %q, want gpt-4o", a.model)
	}
}

func TestTurnHandoffStopsEarly(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(HandoffTool("writer"))

	// First response: LLM calls the handoff tool.
	p := &mockProvider{
		responses: []*llm.Response{
			{
				Content: "",
				Calls: []llm.Call{{
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
	if result.TurnStopReason != TurnStopHandoff {
		t.Fatalf("expected turn stop reason %q, got %q", TurnStopHandoff, result.TurnStopReason)
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
		responses: []*llm.Response{
			{
				Content: "",
				Calls: []llm.Call{{
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
	if result.TurnStopReason != TurnStopCompleted {
		t.Fatalf("expected turn stop reason %q, got %q", TurnStopCompleted, result.TurnStopReason)
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

func TestAgentRunIterator(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&simpleTool{name: "echo", output: "world"})

	p := &mockProvider{
		responses: []*llm.Response{
			{
				Content: "",
				Calls: []llm.Call{{
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
	s := userSession("s-run", "say hello")

	var steps []StepResult
	for step, err := range Run(context.Background(), a, s, a.maxSteps, a.provider, a.maxEscalations) {
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		steps = append(steps, step)
	}

	if len(steps) != 2 {
		t.Fatalf("expected 2 yielded steps, got %d", len(steps))
	}
	if len(steps[0].ToolResults) != 1 {
		t.Fatalf("expected first step to execute a tool, got %+v", steps[0].ToolResults)
	}
	if len(steps[1].ToolResults) != 0 {
		t.Fatalf("expected final step to be tool-free, got %+v", steps[1].ToolResults)
	}
	if steps[1].TurnStopReason != TurnStopCompleted {
		t.Fatalf(
			"expected final step turn stop reason %q, got %q",
			TurnStopCompleted,
			steps[1].TurnStopReason,
		)
	}
}

func TestTurnRetriesTransientModelError(t *testing.T) {
	p := &flakyProvider{
		mockProvider: mockProvider{
			responses: []*llm.Response{{Content: "recovered"}},
		},
		failures: 1,
	}

	a := New("a", "sys", "m", p, nil, WithMaxEscalations(2))
	s := userSession("s-transient", "hello")

	result, err := a.Turn(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "recovered" {
		t.Fatalf("Content = %q, want recovered", result.Content)
	}

	var retries int
	for _, e := range s.Events() {
		if e.Type == session.EscalationRetried {
			retries++
		}
	}
	if retries != 1 {
		t.Fatalf("expected 1 escalation retry event, got %d", retries)
	}
}

func TestTurnWithholdsRecoverableToolFailure(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&panicTool{})

	p := &mockProvider{
		responses: []*llm.Response{
			{
				Calls: []llm.Call{{
					ID:   "c1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "panic", Arguments: `{}`},
				}},
			},
			{Content: "fallback after tool failure"},
		},
	}

	a := New("a", "sys", "m", p, reg, WithMaxEscalations(2))
	s := userSession("s-tool-retry", "trigger tool")

	result, err := a.Turn(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "fallback after tool failure" {
		t.Fatalf("Content = %q, want fallback after tool failure", result.Content)
	}

	msgs := s.Messages()
	var sawToolError bool
	for _, msg := range msgs {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "tool panicked") {
			sawToolError = true
			break
		}
	}
	if !sawToolError {
		t.Fatal("expected withheld tool failure to be appended as a tool message")
	}
}

type panicTool struct{}

func (t *panicTool) Spec() llm.Spec {
	return llm.Spec{Name: "panic", Parameters: map[string]any{}}
}

func (t *panicTool) Execute(_ context.Context, _ string) (string, error) {
	panic("tool boom")
}

type gatedTool struct {
	simpleTool
}

func (t *gatedTool) ApprovalRequirement(args string) (approval.Requirement, bool, error) {
	return approval.Requirement{
		Category:  "workspace",
		Operation: "write_file",
		Resource:  "note.txt",
	}, true, nil
}

func TestRunTools_PanicRecovery(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&panicTool{})

	s := session.New("s-panic")
	calls := []llm.Call{{
		ID:   "c1",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "panic", Arguments: `{}`},
	}}

	_, err := runTools(context.Background(), s, calls, reg, nil, nil, nil, 10)
	if err == nil {
		t.Fatal("expected error from panicking tool, got nil")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("expected panic error message, got: %v", err)
	}
}

func TestRunTools_ApprovalAllow(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&gatedTool{simpleTool{name: "gated", output: "ok"}})
	manager := approval.NewManager(nil)
	s := session.New("s-approval-allow")
	calls := []llm.Call{{
		ID:   "c1",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "gated", Arguments: `{}`},
	}}

	done := make(chan StepResult, 1)
	errs := make(chan error, 1)
	go func() {
		res, err := runTools(context.Background(), s, calls, reg, nil, manager, nil, 10)
		if err != nil {
			errs <- err
			return
		}
		done <- res
	}()

	time.Sleep(10 * time.Millisecond)
	pending := manager.Pending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(pending))
	}
	if err := manager.Resolve(pending[0], approval.DecisionAllow, "ok"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case err := <-errs:
		t.Fatalf("runTools: %v", err)
	case res := <-done:
		if len(res.ToolResults) != 1 || res.ToolResults[0].Content != "ok" {
			t.Fatalf("unexpected tool results: %#v", res.ToolResults)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approved tool")
	}
}

func TestRunTools_ApprovalDeny(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&gatedTool{simpleTool{name: "gated", output: "ok"}})
	manager := approval.NewManager(nil)
	s := session.New("s-approval-deny")
	calls := []llm.Call{{
		ID:   "c1",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "gated", Arguments: `{}`},
	}}

	errCh := make(chan error, 1)
	go func() {
		_, err := runTools(context.Background(), s, calls, reg, nil, manager, nil, 10)
		errCh <- err
	}()

	time.Sleep(10 * time.Millisecond)
	pending := manager.Pending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(pending))
	}
	if err := manager.Resolve(pending[0], approval.DecisionDeny, "unsafe"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "approval denied: unsafe") {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for denied tool")
	}
}
