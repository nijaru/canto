package context

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/memory"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

func TestBuilder_Build(t *testing.T) {
	sess := session.New("test-session")
	_ = sess.Append(context.Background(), session.NewEvent(sess.ID(), session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Hello world",
	}))

	reg := tool.NewRegistry()
	// Add a mock tool
	// ... (assuming registry works)

	builder := NewBuilder(
		InstructionProcessor("You are a helpful assistant."),
		HistoryProcessor(),
		ToolProcessor(reg),
	)

	req := &llm.LLMRequest{
		Model: "gpt-4o",
	}

	err := builder.Build(context.Background(), nil, "", sess, req)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	// Verify messages
	if len(req.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != llm.RoleSystem {
		t.Errorf("expected first message to be system, got %s", req.Messages[0].Role)
	}
	if req.Messages[1].Content != "Hello world" {
		t.Errorf("expected second message to be 'Hello world', got %s", req.Messages[1].Content)
	}
}

func TestOffloadProcessor(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "canto-offload-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	sess := session.New("test-session")

	// Large tool result
	largeContent := ""
	for i := 0; i < 2000; i++ {
		largeContent += "large content "
	}

	req := &llm.LLMRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "request"},
			{Role: llm.RoleAssistant, Content: "calling tool..."},
			{Role: llm.RoleTool, Content: largeContent, ToolID: "t1"},
			{Role: llm.RoleAssistant, Content: "done"},
			{Role: llm.RoleUser, Content: "next"},
		},
	}

	// Threshold is 60%, MaxTokens = 1000.
	// largeContent is ~3000 tokens (chars/4 heuristic).
	offloader := NewOffloadProcessor(1000, tempDir)
	offloader.MinKeepTurns = 2 // Keep last 2 messages

	err = offloader.Process(context.Background(), nil, "", sess, req)
	if err != nil {
		t.Fatalf("offload failed: %v", err)
	}

	// Message 2 (RoleTool) should be offloaded because it's older than last 2
	if len(req.Messages[2].Content) > 1000 {
		t.Errorf(
			"expected message to be offloaded, but still have %d chars",
			len(req.Messages[2].Content),
		)
	}

	// Verify file exists
	files, err := filepath.Glob(filepath.Join(tempDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Errorf("expected offload file to be created")
	}
}

// --- CoreMemoryProcessor ---

func newTestCoreStore(t *testing.T) *memory.CoreStore {
	t.Helper()
	dsn := "file::memory:?cache=shared&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	store, err := memory.NewCoreStore(dsn)
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCoreMemoryProcessor_NoPersona(t *testing.T) {
	store := newTestCoreStore(t)
	sess := session.New("sess-no-persona")
	req := &llm.LLMRequest{}

	proc := CoreMemoryProcessor(store)
	if err := proc.Process(context.Background(), nil, "", sess, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No persona set — messages should remain empty.
	if len(req.Messages) != 0 {
		t.Errorf("expected no messages, got %d", len(req.Messages))
	}
}

func TestCoreMemoryProcessor_InjectsBlock(t *testing.T) {
	store := newTestCoreStore(t)
	ctx := context.Background()
	sessID := "sess-with-persona"

	if err := store.SetPersona(ctx, sessID, &memory.Persona{
		Name:        "Aria",
		Description: "A helpful assistant",
		Directives:  "Be concise",
	}); err != nil {
		t.Fatalf("SetPersona: %v", err)
	}

	sess := session.New(sessID)
	req := &llm.LLMRequest{}

	proc := CoreMemoryProcessor(store)
	if err := proc.Process(ctx, nil, "", sess, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	msg := req.Messages[0]
	if msg.Role != llm.RoleSystem {
		t.Errorf("expected system role, got %s", msg.Role)
	}
	if !strings.Contains(msg.Content, "<core_memory>") {
		t.Errorf("expected <core_memory> block, got: %s", msg.Content)
	}
	if !strings.Contains(msg.Content, "Aria") {
		t.Errorf("expected persona name in block, got: %s", msg.Content)
	}
}

func TestCoreMemoryProcessor_PrependToExistingSystemMessage(t *testing.T) {
	store := newTestCoreStore(t)
	ctx := context.Background()
	sessID := "sess-prepend"

	if err := store.SetPersona(ctx, sessID, &memory.Persona{
		Name:        "Bot",
		Description: "A bot",
		Directives:  "Be brief",
	}); err != nil {
		t.Fatalf("SetPersona: %v", err)
	}

	sess := session.New(sessID)
	req := &llm.LLMRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a coding assistant."},
		},
	}

	proc := CoreMemoryProcessor(store)
	if err := proc.Process(ctx, nil, "", sess, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	content := req.Messages[0].Content
	if !strings.Contains(content, "<core_memory>") {
		t.Errorf("expected core_memory block, got: %s", content)
	}
	if !strings.Contains(content, "You are a coding assistant.") {
		t.Errorf("expected original system text preserved, got: %s", content)
	}
	// Block should precede the original text.
	coreIdx := strings.Index(content, "<core_memory>")
	origIdx := strings.Index(content, "You are")
	if coreIdx >= origIdx {
		t.Errorf("expected core_memory block before original instructions")
	}
}

func TestCoreMemoryProcessor_ReplacesExistingBlock(t *testing.T) {
	store := newTestCoreStore(t)
	ctx := context.Background()
	sessID := "sess-replace"

	if err := store.SetPersona(ctx, sessID, &memory.Persona{
		Name:        "Updated",
		Description: "New description",
		Directives:  "New directives",
	}); err != nil {
		t.Fatalf("SetPersona: %v", err)
	}

	existing := "<core_memory>\nAgent Name: Old\nPersona Context: Old desc\nDirectives: Old\n</core_memory>"
	sess := session.New(sessID)
	req := &llm.LLMRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: existing + "\n\nOriginal system text."},
		},
	}

	proc := CoreMemoryProcessor(store)
	if err := proc.Process(ctx, nil, "", sess, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := req.Messages[0].Content
	// Old persona name should be gone.
	if strings.Contains(content, "Agent Name: Old") {
		t.Errorf("old block not replaced, got: %s", content)
	}
	// New persona name should be present.
	if !strings.Contains(content, "Updated") {
		t.Errorf("expected updated persona in block, got: %s", content)
	}
	// Original system text should still be there.
	if !strings.Contains(content, "Original system text.") {
		t.Errorf("expected original system text preserved, got: %s", content)
	}
}

func TestCoreMemoryProcessor_NilStore(t *testing.T) {
	sess := session.New("sess-nil-store")
	req := &llm.LLMRequest{}
	proc := CoreMemoryProcessor(nil)
	if err := proc.Process(context.Background(), nil, "", sess, req); err != nil {
		t.Fatalf("unexpected error with nil store: %v", err)
	}
	if len(req.Messages) != 0 {
		t.Errorf("expected no messages with nil store, got %d", len(req.Messages))
	}
}

// --- TokenGuardProcessor ---

func TestTokenGuard_PassingCase(t *testing.T) {
	guard := NewTokenGuard(10000)
	sess := session.New("sess-tg-pass")
	req := &llm.LLMRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
		},
	}
	if err := guard.Process(context.Background(), nil, "", sess, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTokenGuard_Exceeded(t *testing.T) {
	guard := NewTokenGuard(1) // 1 token max — trivially exceeded
	sess := session.New("sess-tg-exceed")
	req := &llm.LLMRequest{
		Messages: []llm.Message{
			{
				Role:    llm.RoleUser,
				Content: "This is a message with plenty of content to exceed the limit.",
			},
		},
	}
	err := guard.Process(context.Background(), nil, "", sess, req)
	if err == nil {
		t.Fatal("expected token budget error, got nil")
	}
	if !strings.Contains(err.Error(), "token budget exceeded") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestTokenGuard_ZeroMaxTokensSkips(t *testing.T) {
	guard := NewTokenGuard(0)
	sess := session.New("sess-tg-zero")
	// Even enormous content should pass when MaxTokens == 0.
	big := strings.Repeat("x", 100_000)
	req := &llm.LLMRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: big},
		},
	}
	if err := guard.Process(context.Background(), nil, "", sess, req); err != nil {
		t.Fatalf("expected no error with zero limit, got: %v", err)
	}
}

// --- BudgetGuard ---

func TestBudgetGuard_PassingCase(t *testing.T) {
	guard := NewBudgetGuard(10.0)
	sess := session.New("sess-bg-pass")

	e := session.NewEvent(sess.ID(), session.EventTypeMessageAdded, nil)
	e.Cost = 0.50
	_ = sess.Append(context.Background(), e)

	req := &llm.LLMRequest{}
	if err := guard.Process(context.Background(), nil, "", sess, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBudgetGuard_Exceeded(t *testing.T) {
	guard := NewBudgetGuard(1.0)
	sess := session.New("sess-bg-exceed")

	e := session.NewEvent(sess.ID(), session.EventTypeMessageAdded, nil)
	e.Cost = 1.50
	_ = sess.Append(context.Background(), e)

	req := &llm.LLMRequest{}
	err := guard.Process(context.Background(), nil, "", sess, req)
	if err == nil {
		t.Fatal("expected budget exceeded error, got nil")
	}
	if !strings.Contains(err.Error(), "budget exceeded") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBudgetGuard_ZeroLimitSkips(t *testing.T) {
	guard := NewBudgetGuard(0)
	sess := session.New("sess-bg-zero")

	e := session.NewEvent(sess.ID(), session.EventTypeMessageAdded, nil)
	e.Cost = 999.99
	_ = sess.Append(context.Background(), e)

	req := &llm.LLMRequest{}
	if err := guard.Process(context.Background(), nil, "", sess, req); err != nil {
		t.Fatalf("expected no error with zero limit, got: %v", err)
	}
}

func TestBudgetGuard_ExactlyAtLimit(t *testing.T) {
	guard := NewBudgetGuard(1.0)
	sess := session.New("sess-bg-exact")

	e := session.NewEvent(sess.ID(), session.EventTypeMessageAdded, nil)
	e.Cost = 1.0
	_ = sess.Append(context.Background(), e)

	req := &llm.LLMRequest{}
	err := guard.Process(context.Background(), nil, "", sess, req)
	// >= limit triggers error
	if err == nil {
		t.Fatal("expected budget exceeded error at exact limit, got nil")
	}
}

// ---------------------------------------------------------------------------
// CapabilitiesProcessor
// ---------------------------------------------------------------------------

type capProvider struct {
	caps llm.Capabilities
}

func (p *capProvider) ID() string { return "cap" }
func (p *capProvider) Generate(_ context.Context, _ *llm.LLMRequest) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{}, nil
}

func (p *capProvider) Stream(_ context.Context, _ *llm.LLMRequest) (llm.Stream, error) {
	return nil, nil
}
func (p *capProvider) Models(_ context.Context) ([]catwalk.Model, error) { return nil, nil }
func (p *capProvider) CountTokens(_ context.Context, _ string, _ []llm.Message) (int, error) {
	return 0, nil
}
func (p *capProvider) Cost(_ context.Context, _ string, _ llm.Usage) float64 { return 0 }
func (p *capProvider) Capabilities(_ string) llm.Capabilities                { return p.caps }
func (p *capProvider) IsTransient(err error) bool                            { return false }

func TestCapabilitiesProcessor_StandardModel(t *testing.T) {
	proc := CapabilitiesProcessor()
	p := &capProvider{caps: llm.DefaultCapabilities()}
	sess := session.New("caps-1")
	req := &llm.LLMRequest{
		Temperature: 0.7,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are helpful."},
			{Role: llm.RoleUser, Content: "Hello"},
		},
	}
	if err := proc.Process(context.Background(), p, "gpt-4o", sess, req); err != nil {
		t.Fatal(err)
	}
	if req.Temperature != 0.7 {
		t.Errorf("standard model: temperature should be unchanged, got %v", req.Temperature)
	}
	if req.Messages[0].Role != llm.RoleSystem {
		t.Errorf("standard model: system message should remain, got %s", req.Messages[0].Role)
	}
}

func TestCapabilitiesProcessor_ReasoningModel_SystemToUser(t *testing.T) {
	proc := CapabilitiesProcessor()
	caps := llm.Capabilities{
		SystemRole:  llm.RoleUser,
		Temperature: false,
		Streaming:   true,
		Tools:       true,
	}
	p := &capProvider{caps: caps}
	sess := session.New("caps-2")
	req := &llm.LLMRequest{
		Temperature: 1.0,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Be concise."},
			{Role: llm.RoleUser, Content: "What is 2+2?"},
		},
	}
	if err := proc.Process(context.Background(), p, "model", sess, req); err != nil {
		t.Fatal(err)
	}
	if req.Temperature != 0 {
		t.Errorf("reasoning model: temperature should be zeroed, got %v", req.Temperature)
	}
	if req.Messages[0].Role != llm.RoleUser {
		t.Errorf(
			"reasoning model: system message should be converted to user, got %s",
			req.Messages[0].Role,
		)
	}
	if !strings.HasPrefix(req.Messages[0].Content, "Instructions:") {
		t.Errorf(
			"reasoning model: converted message should start with Instructions:, got %q",
			req.Messages[0].Content,
		)
	}
}

func TestCapabilitiesProcessor_ReasoningModel_SystemToDeveloper(t *testing.T) {
	proc := CapabilitiesProcessor()
	caps := llm.Capabilities{
		SystemRole:  llm.RoleDeveloper,
		Temperature: false,
		Streaming:   true,
		Tools:       true,
	}
	p := &capProvider{caps: caps}
	sess := session.New("caps-4")
	req := &llm.LLMRequest{
		Temperature: 1.0,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Be concise."},
			{Role: llm.RoleUser, Content: "What is 2+2?"},
		},
	}
	if err := proc.Process(context.Background(), p, "model", sess, req); err != nil {
		t.Fatal(err)
	}
	if req.Messages[0].Role != llm.RoleDeveloper {
		t.Errorf("expected developer role, got %s", req.Messages[0].Role)
	}
	if req.Messages[0].Content != "Be concise." {
		t.Errorf("developer role should not add prefix, got %q", req.Messages[0].Content)
	}
}

func TestCapabilitiesProcessor_NilProvider(t *testing.T) {
	proc := CapabilitiesProcessor()
	req := &llm.LLMRequest{Temperature: 0.5}
	err := proc.Process(context.Background(), nil, "any-model", session.New("caps-3"), req)
	if err != nil {
		t.Fatal(err)
	}
	// nil provider: no-op, temperature unchanged
	if req.Temperature != 0.5 {
		t.Errorf("nil provider: temperature should be unchanged, got %v", req.Temperature)
	}
}
