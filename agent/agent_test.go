package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/prompt"
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

type errorProvider struct {
	mockProvider
	err error
}

func (p *errorProvider) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return nil, p.err
}

type contextBlockingProvider struct {
	mockProvider
	started chan struct{}
}

func (p *contextBlockingProvider) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	p.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

type rejectCanceledWriter struct {
	events []session.Event
}

func (w *rejectCanceledWriter) Save(ctx context.Context, e session.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.events = append(w.events, e)
	return nil
}

type recordingProvider struct {
	mockProvider
	lastMessages []llm.Message
	lastTools    []string
}

func (p *recordingProvider) Generate(
	_ context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	p.lastMessages = append(p.lastMessages[:0], req.Messages...)
	p.lastTools = p.lastTools[:0]
	for _, spec := range req.Tools {
		p.lastTools = append(p.lastTools, spec.Name)
	}
	return &llm.Response{Content: "ok"}, nil
}

type developerRoleRecordingProvider struct {
	recordingProvider
}

func (p *developerRoleRecordingProvider) Capabilities(string) llm.Capabilities {
	caps := llm.DefaultCapabilities()
	caps.SystemRole = llm.RoleDeveloper
	return caps
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

type blockingTool struct {
	name     string
	metadata tool.Metadata
	started  chan struct{}
	release  chan struct{}
}

func (t *blockingTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        t.name,
		Description: "A blocking test tool",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (t *blockingTool) Metadata() tool.Metadata { return t.metadata }

func (t *blockingTool) Execute(_ context.Context, _ string) (string, error) {
	close(t.started)
	<-t.release
	return t.name, nil
}

type orderedTool struct {
	name     string
	metadata tool.Metadata
	started  chan struct{}
	release  <-chan struct{}
}

func (t *orderedTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        t.name,
		Description: "An ordered test tool",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (t *orderedTool) Metadata() tool.Metadata { return t.metadata }

func (t *orderedTool) Execute(_ context.Context, _ string) (string, error) {
	close(t.started)
	if t.release != nil {
		<-t.release
	}
	return t.name, nil
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

func TestStepSkipsEmptyAssistantMessage(t *testing.T) {
	usage := llm.Usage{InputTokens: 4, OutputTokens: 1, TotalTokens: 5}
	p := &mockProvider{
		responses: []*llm.Response{
			{Usage: usage},
		},
	}
	a := New("test-agent", "You are helpful.", "gpt-4", p, nil)
	s := userSession("s-empty-step", "Hi!")

	result, err := a.Step(t.Context(), s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Usage.TotalTokens != usage.TotalTokens {
		t.Fatalf("usage = %+v, want %+v", result.Usage, usage)
	}

	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected only user message, got %#v", msgs)
	}
}

func TestStepSkipsWhitespaceOnlyAssistantMessage(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.Response{
			{Content: " \n\t ", Reasoning: "  "},
		},
	}
	a := New("test-agent", "You are helpful.", "gpt-4", p, nil)
	s := userSession("s-whitespace-step", "Hi!")

	if _, err := a.Step(t.Context(), s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected only user message, got %#v", msgs)
	}
}

func TestStepPreservesReasoningOnlyAssistantMessage(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.Response{
			{Reasoning: "reasoning only"},
		},
	}
	a := New("test-agent", "You are helpful.", "gpt-4", p, nil)
	s := userSession("s-reasoning-step", "Hi!")

	if _, err := a.Step(t.Context(), s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected assistant reasoning message, got %#v", msgs)
	}
	if msgs[1].Reasoning != "reasoning only" {
		t.Fatalf("reasoning = %q, want reasoning only", msgs[1].Reasoning)
	}
}

func TestTurnRecordsTerminalEventOnProviderError(t *testing.T) {
	providerErr := fmt.Errorf("provider unavailable")
	a := New("test-agent", "You are helpful.", "gpt-4", &errorProvider{err: providerErr}, nil)
	s := userSession("s-provider-error-turn", "Hi!")

	_, err := a.Turn(t.Context(), s)
	if err == nil {
		t.Fatal("expected provider error")
	}

	var found bool
	for _, ev := range s.Events() {
		if ev.Type != session.TurnCompleted {
			continue
		}
		data, ok, err := ev.TurnCompletedData()
		if err != nil {
			t.Fatalf("decode turn completed: %v", err)
		}
		if !ok {
			continue
		}
		found = true
		if !strings.Contains(data.Error, providerErr.Error()) {
			t.Fatalf("turn error = %q, want %q", data.Error, providerErr.Error())
		}
	}
	if !found {
		t.Fatal("expected turn completed event")
	}
}

func TestTurnRecordsTerminalEventOnCanceledContext(t *testing.T) {
	p := &contextBlockingProvider{started: make(chan struct{}, 1)}
	a := New("test-agent", "You are helpful.", "gpt-4", p, nil)
	s := userSession("s-canceled-turn", "Hi!")
	ctx, cancel := context.WithCancel(t.Context())

	errCh := make(chan error, 1)
	go func() {
		_, err := a.Turn(ctx, s)
		errCh <- err
	}()

	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider call")
	}
	cancel()

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Fatalf("turn error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled turn")
	}

	assertTurnCompletedError(t, s, context.Canceled.Error())
}

func TestTurnRecordsStepCompletedOnCanceledContext(t *testing.T) {
	p := &contextBlockingProvider{started: make(chan struct{}, 1)}
	a := New("test-agent", "You are helpful.", "gpt-4", p, nil)
	writer := &rejectCanceledWriter{}
	s := session.New("s-canceled-step").WithWriter(writer)
	if err := s.Append(t.Context(), session.NewEvent(s.ID(), session.MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Hi!",
	})); err != nil {
		t.Fatalf("append user: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())

	errCh := make(chan error, 1)
	go func() {
		_, err := a.Turn(ctx, s)
		errCh <- err
	}()

	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider call")
	}
	cancel()

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Fatalf("turn error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled turn")
	}

	var found bool
	for _, ev := range writer.events {
		if ev.Type != session.StepCompleted {
			continue
		}
		data, ok, err := ev.StepCompletedData()
		if err != nil {
			t.Fatalf("decode step completed: %v", err)
		}
		if !ok {
			continue
		}
		found = true
		if !strings.Contains(data.Error, context.Canceled.Error()) {
			t.Fatalf("step error = %q, want context canceled", data.Error)
		}
	}
	if !found {
		t.Fatal("expected step completed event")
	}
}

func assertTurnCompletedError(t *testing.T, s *session.Session, want string) {
	t.Helper()

	var found bool
	for _, ev := range s.Events() {
		if ev.Type != session.TurnCompleted {
			continue
		}
		data, ok, err := ev.TurnCompletedData()
		if err != nil {
			t.Fatalf("decode turn completed: %v", err)
		}
		if !ok {
			continue
		}
		found = true
		if !strings.Contains(data.Error, want) {
			t.Fatalf("turn error = %q, want %q", data.Error, want)
		}
	}
	if !found {
		t.Fatal("expected turn completed event")
	}
}

func TestNewAgentUsesLazyToolsByDefault(t *testing.T) {
	reg := tool.NewRegistry()
	for i := range 25 {
		reg.Register(&simpleTool{name: fmt.Sprintf("tool_%d", i), output: "ok"})
	}

	p := &recordingProvider{}
	a := New("lazy-agent", "You are helpful.", "gpt-4", p, reg)
	s := userSession("lazy-session", "find a tool")

	if _, err := a.Step(t.Context(), s); err != nil {
		t.Fatalf("step: %v", err)
	}

	if len(p.lastTools) != 1 || p.lastTools[0] != tool.SearchToolName {
		t.Fatalf("expected only search_tools in prompt, got %#v", p.lastTools)
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
	runner.Register(hook.FromFunc(
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
	runner.Register(hook.FromFunc(
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

func TestToolIdempotencyKeyIgnoresVolatileCallID(t *testing.T) {
	callA := llm.Call{
		ID:   "call-a",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "read_file", Arguments: `{"path":"main.go"}`},
	}
	callB := callA
	callB.ID = "call-b"

	keyA := toolIdempotencyKey("sess", "step-1", callA, 0)
	keyB := toolIdempotencyKey("sess", "step-1", callB, 0)

	if keyA != keyB {
		t.Fatalf("expected stable idempotency key across call-id churn: %q != %q", keyA, keyB)
	}
}

func TestStepToolEventsCarryMatchingIdempotencyKey(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(tool.Func(
		"echo",
		"Echo input",
		map[string]any{"type": "object"},
		func(_ context.Context, args string) (string, error) {
			return args, nil
		},
	))

	p := &mockProvider{
		responses: []*llm.Response{
			{
				Content: "",
				Calls: []llm.Call{{
					ID:   "volatile-call-id",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "echo", Arguments: `{"value":1}`},
				}},
			},
		},
	}
	a := New("test-agent", "You are helpful.", "gpt-4", p, reg)
	s := userSession("s-tool-idempotency", "use tool")

	if _, err := a.Step(context.Background(), s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var started, completed string
	for _, event := range s.Events() {
		switch event.Type {
		case session.ToolStarted:
			data, ok, err := event.ToolStartedData()
			if err != nil {
				t.Fatalf("decode tool started: %v", err)
			}
			if !ok {
				t.Fatal("expected tool started data")
			}
			started = data.IdempotencyKey
		case session.ToolCompleted:
			data, ok, err := event.ToolCompletedData()
			if err != nil {
				t.Fatalf("decode tool completed: %v", err)
			}
			if !ok {
				t.Fatal("expected tool completed data")
			}
			completed = data.IdempotencyKey
		}
	}

	if started == "" || completed == "" {
		t.Fatalf(
			"expected non-empty tool idempotency keys, got started=%q completed=%q",
			started,
			completed,
		)
	}
	if started != completed {
		t.Fatalf("expected matching tool event idempotency keys, got %q vs %q", started, completed)
	}
}

func TestRunTools_ReusesCompletedOutputForMatchingIdempotencyKey(t *testing.T) {
	reg := tool.NewRegistry()
	calls := 0
	reg.Register(tool.Func(
		"echo",
		"Echo input",
		map[string]any{"type": "object"},
		func(_ context.Context, args string) (string, error) {
			calls++
			return args, nil
		},
	))

	s := session.New("s-acrfence-reuse")
	call := llm.Call{
		ID:   "volatile-call-id",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "echo", Arguments: `{"value":1}`},
	}
	key := toolIdempotencyKey(s.ID(), "assistant-msg-1", call, 0)
	if err := s.Append(context.Background(), session.NewToolCompletedEvent(s.ID(), session.ToolCompletedData{
		Tool:           "echo",
		ID:             "old-call-id",
		IdempotencyKey: key,
		Output:         "cached output",
	})); err != nil {
		t.Fatalf("append prior tool completion: %v", err)
	}

	res, err := runTools(
		context.Background(),
		s,
		[]llm.Call{call},
		reg,
		nil,
		nil,
		nil,
		10,
		"assistant-msg-1",
	)
	if err != nil {
		t.Fatalf("runTools: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected tool execution to be reused, got %d calls", calls)
	}
	if len(res.ToolResults) != 1 || res.ToolResults[0].Content != "cached output" {
		t.Fatalf("unexpected reused tool results: %#v", res.ToolResults)
	}
}

func TestRunTools_ACRFenceRejectsStartedOnlyExecution(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(tool.Func(
		"echo",
		"Echo input",
		map[string]any{"type": "object"},
		func(_ context.Context, args string) (string, error) { return args, nil },
	))

	s := session.New("s-acrfence-ambiguous")
	call := llm.Call{
		ID:   "volatile-call-id",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "echo", Arguments: `{"value":1}`},
	}
	key := toolIdempotencyKey(s.ID(), "assistant-msg-1", call, 0)
	if err := s.Append(context.Background(), session.NewToolStartedEvent(s.ID(), session.ToolStartedData{
		Tool:           "echo",
		Arguments:      `{"value":1}`,
		ID:             "old-call-id",
		IdempotencyKey: key,
	})); err != nil {
		t.Fatalf("append prior tool start: %v", err)
	}

	if _, err := runTools(
		context.Background(),
		s,
		[]llm.Call{call},
		reg,
		nil,
		nil,
		nil,
		10,
		"assistant-msg-1",
	); err == nil {
		t.Fatal("expected ambiguous replay error, got nil")
	}
}

func TestStepToolConcurrencyPartitioningPreservesSerializedBarriers(t *testing.T) {
	newBlocking := func(name string, mode tool.ConcurrencyMode) *blockingTool {
		return &blockingTool{
			name:     name,
			metadata: tool.Metadata{Concurrency: mode},
			started:  make(chan struct{}),
			release:  make(chan struct{}),
		}
	}

	parallelA := newBlocking("parallel_a", tool.Parallel)
	parallelB := newBlocking("parallel_b", tool.Parallel)
	serialC := newBlocking("serial_c", tool.Serialized)

	reg := tool.NewRegistry()
	reg.Register(parallelA)
	reg.Register(parallelB)
	reg.Register(serialC)

	p := &mockProvider{
		responses: []*llm.Response{
			{
				Content: "",
				Calls: []llm.Call{
					{
						ID:   "c1",
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{Name: "parallel_a", Arguments: `{}`},
					},
					{
						ID:   "c2",
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{Name: "parallel_b", Arguments: `{}`},
					},
					{
						ID:   "c3",
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{Name: "serial_c", Arguments: `{}`},
					},
				},
			},
		},
	}
	a := New("test-agent", "You are helpful.", "gpt-4", p, reg, WithMaxParallelTools(4))
	s := userSession("s-tool-partition", "run tools")

	done := make(chan error, 1)
	go func() {
		_, err := a.Step(context.Background(), s)
		done <- err
	}()

	waitStarted := func(name string, ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(250 * time.Millisecond):
			t.Fatalf("timed out waiting for %s to start", name)
		}
	}

	waitStarted("parallel_a", parallelA.started)
	waitStarted("parallel_b", parallelB.started)

	select {
	case <-serialC.started:
		t.Fatal("serialized tool started before parallel wave completed")
	case <-time.After(100 * time.Millisecond):
	}

	close(parallelA.release)
	close(parallelB.release)
	waitStarted("serial_c", serialC.started)
	close(serialC.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("step did not complete")
	}

	msgs := s.Messages()
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages (user, assistant, 3 tool results), got %d", len(msgs))
	}
	if msgs[2].Name != "parallel_a" || msgs[2].Content != "parallel_a" {
		t.Fatalf("unexpected first tool result: %+v", msgs[2])
	}
	if msgs[3].Name != "parallel_b" || msgs[3].Content != "parallel_b" {
		t.Fatalf("unexpected second tool result: %+v", msgs[3])
	}
	if msgs[4].Name != "serial_c" || msgs[4].Content != "serial_c" {
		t.Fatalf("unexpected third tool result: %+v", msgs[4])
	}
}

func TestRunTools_PreflightCompletesBeforeParallelExecution(t *testing.T) {
	preflightDone := make(chan struct{})
	var (
		mu             sync.Mutex
		preflightCount int
		executions     []string
	)

	makeTool := func(name string) tool.Tool {
		return tool.FuncWithMetadata(
			name,
			"records execution",
			map[string]any{"type": "object"},
			tool.Metadata{Concurrency: tool.Parallel},
			func(_ context.Context, _ string) (string, error) {
				select {
				case <-preflightDone:
				default:
					return "", fmt.Errorf("%s executed before preflight completed", name)
				}
				mu.Lock()
				executions = append(executions, name)
				mu.Unlock()
				return name, nil
			},
		)
	}

	reg := tool.NewRegistry()
	reg.Register(makeTool("a"))
	reg.Register(makeTool("b"))

	hooks := hook.NewRunner()
	hooks.Register(hook.FromFunc(
		"preflight-recorder",
		[]hook.Event{hook.EventPreToolUse},
		func(_ context.Context, payload *hook.Payload) *hook.Result {
			mu.Lock()
			defer mu.Unlock()
			if len(executions) > 0 {
				return &hook.Result{
					Action: hook.ActionBlock,
					Error:  fmt.Errorf("execution started before all preflight hooks completed"),
				}
			}
			preflightCount++
			if preflightCount == 2 {
				close(preflightDone)
			}
			return &hook.Result{Action: hook.ActionProceed}
		},
	))

	s := session.New("s-preflight-before-execute")
	calls := []llm.Call{
		{
			ID:   "c1",
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "a", Arguments: `{}`},
		},
		{
			ID:   "c2",
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "b", Arguments: `{}`},
		},
	}

	res, err := runTools(context.Background(), s, calls, reg, hooks, nil, nil, 2, "step-1")
	if err != nil {
		t.Fatalf("runTools: %v", err)
	}
	if len(res.ToolResults) != 2 {
		t.Fatalf("expected 2 tool results, got %d", len(res.ToolResults))
	}
	if preflightCount != 2 {
		t.Fatalf("expected 2 preflight hooks, got %d", preflightCount)
	}
}

func TestRunTools_ParallelResultsEmitInSourceOrder(t *testing.T) {
	releaseA := make(chan struct{})
	toolA := &orderedTool{
		name:     "a",
		metadata: tool.Metadata{Concurrency: tool.Parallel},
		started:  make(chan struct{}),
		release:  releaseA,
	}
	toolB := &orderedTool{
		name:     "b",
		metadata: tool.Metadata{Concurrency: tool.Parallel},
		started:  make(chan struct{}),
	}

	reg := tool.NewRegistry()
	reg.Register(toolA)
	reg.Register(toolB)
	s := session.New("s-parallel-source-order")
	calls := []llm.Call{
		{
			ID:   "c1",
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "a", Arguments: `{}`},
		},
		{
			ID:   "c2",
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "b", Arguments: `{}`},
		},
	}

	done := make(chan struct {
		res StepResult
		err error
	}, 1)
	go func() {
		res, err := runTools(context.Background(), s, calls, reg, nil, nil, nil, 2, "step-1")
		done <- struct {
			res StepResult
			err error
		}{res: res, err: err}
	}()

	for name, ch := range map[string]<-chan struct{}{
		"a": toolA.started,
		"b": toolB.started,
	} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s to start", name)
		}
	}
	close(releaseA)

	var out struct {
		res StepResult
		err error
	}
	select {
	case out = <-done:
	case <-time.After(time.Second):
		t.Fatal("runTools did not complete")
	}
	if out.err != nil {
		t.Fatalf("runTools: %v", out.err)
	}
	if len(out.res.ToolResults) != 2 {
		t.Fatalf("expected 2 tool results, got %d", len(out.res.ToolResults))
	}
	if out.res.ToolResults[0].Name != "a" || out.res.ToolResults[1].Name != "b" {
		t.Fatalf("tool results out of source order: %#v", out.res.ToolResults)
	}

	msgs := s.Messages()
	if len(msgs) != 2 || msgs[0].Name != "a" || msgs[1].Name != "b" {
		t.Fatalf("session tool messages out of source order: %#v", msgs)
	}
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

func TestBaseAgentConfigureRuntimeOverridesPromptToolSet(t *testing.T) {
	provider := &recordingProvider{}
	full := tool.NewRegistry()
	full.Register(&simpleTool{name: "alpha", output: "a"})
	full.Register(&simpleTool{name: "beta", output: "b"})
	scoped := tool.NewRegistry()
	scoped.Register(&simpleTool{name: "beta", output: "b"})

	base := New("a", "sys", "m", provider, full)
	configurable, ok := any(base).(RuntimeConfigurable)
	if !ok {
		t.Fatal("expected BaseAgent to implement RuntimeConfigurable")
	}

	runtimeAgent := configurable.ConfigureRuntime(RuntimeConfig{Tools: scoped})
	sess := userSession("s-runtime-tools", "hi")
	if _, err := runtimeAgent.Turn(t.Context(), sess); err != nil {
		t.Fatalf("turn: %v", err)
	}

	if got := strings.Join(provider.lastTools, ","); got != "beta" {
		t.Fatalf("provider tool set = %q, want beta", got)
	}
}

func TestBaseAgentConfigureRuntimeProcessorsRunBeforeCacheAlignment(t *testing.T) {
	provider := &recordingProvider{}
	base := New("a", "base", "m", provider, nil)
	configurable, ok := any(base).(RuntimeConfigurable)
	if !ok {
		t.Fatal("expected BaseAgent to implement RuntimeConfigurable")
	}

	runtimeAgent := configurable.ConfigureRuntime(RuntimeConfig{
		RequestProcessors: []prompt.RequestProcessor{
			prompt.RequestProcessorFunc(
				func(
					ctx context.Context,
					p llm.Provider,
					model string,
					sess *session.Session,
					req *llm.Request,
				) error {
					return prompt.Instructions("runtime").ApplyRequest(ctx, p, model, sess, req)
				},
			),
		},
	})
	if _, err := runtimeAgent.Turn(t.Context(), userSession("s-runtime-cache", "hi")); err != nil {
		t.Fatalf("turn: %v", err)
	}

	if len(provider.lastMessages) == 0 {
		t.Fatal("expected provider request messages")
	}
	system := provider.lastMessages[0]
	if got, want := system.Content, "runtime\n\nbase"; got != want {
		t.Fatalf("system content = %q, want %q", got, want)
	}
	if system.CacheControl == nil {
		t.Fatal("expected cache alignment to include runtime system instructions")
	}
}

func TestBaseAgentDefaultBuilderLeavesProviderCapabilityPrepToProvider(t *testing.T) {
	provider := &developerRoleRecordingProvider{}
	a := New("a", "base", "m", provider, nil)

	if _, err := a.Turn(t.Context(), userSession("s-neutral-provider-prep", "hi")); err != nil {
		t.Fatalf("turn: %v", err)
	}

	if len(provider.lastMessages) == 0 {
		t.Fatal("expected provider request messages")
	}
	if got := provider.lastMessages[0].Role; got != llm.RoleSystem {
		t.Fatalf("default builder preformatted provider role = %q, want neutral system", got)
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

func TestTurnStopReasonContinuesWhenStepProducedToolResults(t *testing.T) {
	s := userSession("s-turn-stop-tool-results", "hello")
	if err := s.Append(t.Context(), session.NewEvent(s.ID(), session.MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "external input raced in",
	})); err != nil {
		t.Fatalf("append raced user input: %v", err)
	}

	got := turnStopReasonForTurn(
		StepResult{
			ToolResults: []llm.Message{{
				Role:    llm.RoleTool,
				Content: "tool output",
				ToolID:  "c1",
				Name:    "echo",
			}},
		},
		s,
		1,
		4,
	)
	if got != "" {
		t.Fatalf("expected turn to continue when step emitted tool results, got %q", got)
	}
}

func TestTurnStopReasonCompletedWithoutToolResults(t *testing.T) {
	s := userSession("s-turn-stop-complete", "hello")
	got := turnStopReasonForTurn(StepResult{}, s, 1, 4)
	if got != TurnStopCompleted {
		t.Fatalf("expected completed turn stop reason, got %q", got)
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

func TestTurnStopsCleanlyWhenBudgetGuardTrips(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.Response{
			{Content: "should not run"},
		},
	}
	a := New("a", "sys", "m", p, nil, WithBudgetGuard(1.0))
	s := userSession("s-budget-stop", "hello")

	e := session.NewEvent(s.ID(), session.MessageAdded, llm.Message{
		Role:    llm.RoleAssistant,
		Content: "prior cost",
	})
	e.Cost = 1.0
	if err := s.Append(t.Context(), e); err != nil {
		t.Fatalf("append prior cost: %v", err)
	}

	result, err := a.Turn(t.Context(), s)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result.TurnStopReason != TurnStopBudgetExhausted {
		t.Fatalf(
			"expected turn stop reason %q, got %q",
			TurnStopBudgetExhausted,
			result.TurnStopReason,
		)
	}

	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected no new assistant output after budget stop, got %d messages", len(msgs))
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

type failingTool struct{}

func (t *failingTool) Spec() llm.Spec {
	return llm.Spec{Name: "fail", Parameters: map[string]any{}}
}

func (t *failingTool) Execute(_ context.Context, _ string) (string, error) {
	return "partial output", fmt.Errorf("tool failed")
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

	_, err := runTools(context.Background(), s, calls, reg, nil, nil, nil, 10, "step-1")
	if err == nil {
		t.Fatal("expected error from panicking tool, got nil")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("expected panic error message, got: %v", err)
	}
}

func TestRunToolsRecordsToolCompletedError(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&failingTool{})

	s := session.New("s-tool-error")
	calls := []llm.Call{{
		ID:   "c1",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "fail", Arguments: `{}`},
	}}

	result, err := runTools(context.Background(), s, calls, reg, nil, nil, nil, 10, "step-1")
	if err != nil {
		t.Fatalf("runTools: %v", err)
	}
	if len(result.ToolResults) != 1 {
		t.Fatalf("tool results = %d, want 1", len(result.ToolResults))
	}
	if !strings.Contains(result.ToolResults[0].Content, "tool failed") {
		t.Fatalf("tool result content = %q, want tool failed", result.ToolResults[0].Content)
	}

	var found bool
	for _, ev := range s.Events() {
		if ev.Type != session.ToolCompleted {
			continue
		}
		data, ok, err := ev.ToolCompletedData()
		if err != nil {
			t.Fatalf("decode tool completed: %v", err)
		}
		if !ok {
			continue
		}
		found = true
		if data.Error != "tool failed" {
			t.Fatalf("tool error = %q, want tool failed", data.Error)
		}
		if !strings.Contains(data.Output, "partial output") {
			t.Fatalf("tool output = %q, want partial output", data.Output)
		}
	}
	if !found {
		t.Fatal("expected tool completed event")
	}
}

func TestRunToolsRecordsCanceledToolResult(t *testing.T) {
	reg := tool.NewRegistry()
	ctx, cancel := context.WithCancel(t.Context())
	reg.Register(tool.Func("cancel", "cancels", nil,
		func(ctx context.Context, _ string) (string, error) {
			cancel()
			return "", ctx.Err()
		}))
	writer := &rejectCanceledWriter{}
	s := session.New("s-tool-canceled").WithWriter(writer)
	calls := []llm.Call{{
		ID:   "c1",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "cancel", Arguments: `{}`},
	}}

	result, err := runTools(ctx, s, calls, reg, nil, nil, nil, 10, "step-1")
	if err != nil {
		t.Fatalf("runTools: %v", err)
	}
	if len(result.ToolResults) != 1 {
		t.Fatalf("tool results = %d, want 1", len(result.ToolResults))
	}
	if !strings.Contains(result.ToolResults[0].Content, context.Canceled.Error()) {
		t.Fatalf("tool result content = %q, want context canceled", result.ToolResults[0].Content)
	}

	var completed bool
	var toolMessage bool
	for _, ev := range writer.events {
		switch ev.Type {
		case session.ToolCompleted:
			data, ok, err := ev.ToolCompletedData()
			if err != nil {
				t.Fatalf("decode tool completed: %v", err)
			}
			if ok && strings.Contains(data.Error, context.Canceled.Error()) {
				completed = true
			}
		case session.MessageAdded:
			var msg llm.Message
			if err := ev.UnmarshalData(&msg); err != nil {
				t.Fatalf("decode message: %v", err)
			}
			if msg.Role == llm.RoleTool &&
				strings.Contains(msg.Content, context.Canceled.Error()) {
				toolMessage = true
			}
		}
	}
	if !completed {
		t.Fatal("expected tool completed event with cancellation error")
	}
	if !toolMessage {
		t.Fatal("expected tool result message with cancellation error")
	}
}

func TestRunTools_ApprovalAllow(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&gatedTool{simpleTool{name: "gated", output: "ok"}})
	manager := approval.NewGate(nil)
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
		res, err := runTools(context.Background(), s, calls, reg, nil, manager, nil, 10, "step-1")
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
	manager := approval.NewGate(nil)
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
		_, err := runTools(context.Background(), s, calls, reg, nil, manager, nil, 10, "step-1")
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
