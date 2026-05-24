package governor

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
	"github.com/nijaru/canto/session"
)

type mockProvider struct {
	id    string
	genFn func(ctx context.Context, req *llm.Request) (*llm.Response, error)
}

func (m *mockProvider) ID() string { return m.id }

func (m *mockProvider) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	return m.genFn(ctx, req)
}

func (m *mockProvider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return nil, nil
}

func (m *mockProvider) Models(ctx context.Context) ([]llm.Model, error) {
	return nil, nil
}

func (m *mockProvider) CountTokens(
	ctx context.Context,
	model string,
	messages []llm.Message,
) (int, error) {
	return 0, nil
}

func (m *mockProvider) Cost(ctx context.Context, model string, usage llm.Usage) float64 {
	return 0
}
func (m *mockProvider) Capabilities(_ string) llm.Capabilities { return llm.DefaultCapabilities() }
func (m *mockProvider) IsTransient(_ error) bool               { return false }
func (m *mockProvider) IsContextOverflow(_ error) bool         { return false }

func TestSummarizer(t *testing.T) {
	sess := session.New("test-session")
	history := []llm.Message{
		{Role: llm.RoleSystem, Content: "System prompt"},
		{Role: llm.RoleUser, Content: "Hello 1"},   // candidate
		{Role: llm.RoleAssistant, Content: "Hi 1"}, // candidate
		{Role: llm.RoleUser, Content: "Hello 2"},   // candidate
		{Role: llm.RoleAssistant, Content: "Hi 2"}, // candidate
		{Role: llm.RoleUser, Content: "Hello 3"},   // recent 1
		{Role: llm.RoleAssistant, Content: "Hi 3"}, // recent 2
		{Role: llm.RoleUser, Content: "Hello 4"},   // recent 3
	}

	// Expand content to trigger threshold
	longStr := ""
	for i := 0; i < 100; i++ {
		longStr += "word "
	}
	for i := range history {
		if i == 0 {
			continue
		}
		history[i].Content += longStr
	}
	for _, msg := range history {
		if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	provider := &mockProvider{
		id: "mock",
		genFn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			return &llm.Response{Content: "Summarized conversation"}, nil
		},
	}

	processor := NewSummarizer(100, provider, "mock-model")
	if err := processor.Mutate(context.Background(), nil, "", sess); err != nil {
		t.Fatalf("processor failed: %v", err)
	}

	req := &llm.Request{Messages: []llm.Message{}}
	if err := prompt.History().ApplyRequest(context.Background(), nil, "", sess, req); err != nil {
		t.Fatalf("history rebuild failed: %v", err)
	}

	if len(req.Messages) != 6 { // 1 summary + 3 recent user turns
		t.Fatalf("expected 6 messages, got %d", len(req.Messages))
	}

	if req.Messages[0].Role != llm.RoleUser {
		t.Errorf(
			"expected first message to be summary transcript context, got %s",
			req.Messages[0].Role,
		)
	}

	expectedSummary := "<conversation_summary>\nSummarized conversation\n</conversation_summary>"
	if req.Messages[0].Content != expectedSummary {
		t.Errorf(
			"expected summary content '%s', got '%s'",
			expectedSummary,
			req.Messages[0].Content,
		)
	}

	if req.Messages[1].Content != "Hello 2"+longStr {
		t.Errorf(
			"expected first retained turn to start with 'Hello 2...', got '%s'",
			req.Messages[1].Content,
		)
	}

	if req.Messages[5].Content != "Hello 4"+longStr {
		t.Errorf("expected last message to be 'Hello 4...', got '%s'", req.Messages[5].Content)
	}

	historyReq := &llm.Request{}
	if err := prompt.History().ApplyRequest(context.Background(), nil, "", sess, historyReq); err != nil {
		t.Fatalf("history rebuild failed: %v", err)
	}
	if len(historyReq.Messages) != 6 {
		t.Fatalf("expected 6 rebuilt history messages, got %d", len(historyReq.Messages))
	}
	if historyReq.Messages[0].Content != expectedSummary {
		t.Fatalf("expected persisted summary, got %q", historyReq.Messages[0].Content)
	}
}

func TestSummarizerMinKeepTurnsRetainsWholeRecentTurns(t *testing.T) {
	sess := session.New("turn-aware-summary")
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("old user ", 30)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("old assistant ", 30)},
		{Role: llm.RoleUser, Content: strings.Repeat("recent user one ", 20)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("recent assistant one ", 20)},
		{Role: llm.RoleUser, Content: strings.Repeat("recent user two ", 20)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("recent assistant two ", 20)},
	} {
		if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	provider := &mockProvider{
		id: "mock",
		genFn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			return &llm.Response{Content: "Old turn summary"}, nil
		},
	}

	processor := NewSummarizer(100, provider, "mock-model")
	processor.ThresholdPct = 0.10
	processor.MinKeepTurns = 2
	if err := processor.Mutate(context.Background(), nil, "", sess); err != nil {
		t.Fatalf("processor failed: %v", err)
	}

	messages, err := sess.EffectiveMessages()
	if err != nil {
		t.Fatalf("EffectiveMessages: %v", err)
	}
	if len(messages) != 5 {
		t.Fatalf("expected summary plus 2 complete recent turns, got %d", len(messages))
	}
	for _, want := range []string{
		"recent user one",
		"recent assistant one",
		"recent user two",
		"recent assistant two",
	} {
		if !containsMessageContent(messages, want) {
			t.Fatalf("expected compacted history to retain %q: %#v", want, messages)
		}
	}
	for _, older := range []string{"old user", "old assistant"} {
		for _, msg := range messages[1:] {
			if strings.Contains(msg.Content, older) {
				t.Fatalf("expected %q to be summarized, got raw message %#v", older, msg)
			}
		}
	}
}

func TestSummarizerSkipsPreCompactWhenTooFewTurns(t *testing.T) {
	sess := session.New("short-session")
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("hello ", 80)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("world ", 80)},
	} {
		if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	compactCalled := false
	provider := &mockProvider{
		id: "mock",
		genFn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			return &llm.Response{Content: "unused"}, nil
		},
	}

	processor := NewSummarizer(100, provider, "mock-model")
	processor.OnPreCompact = func(ctx context.Context, sess *session.Session) {
		compactCalled = true
	}

	if err := processor.Mutate(context.Background(), nil, "", sess); err != nil {
		t.Fatalf("processor failed: %v", err)
	}
	if compactCalled {
		t.Fatal("expected OnPreCompact to stay idle when summarizer has no candidates")
	}
}

func TestSummarizerUsesPreviousSummaryUpdatePrompt(t *testing.T) {
	sess := session.New("update-summary")
	first := session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "original request",
	})
	if err := sess.Append(context.Background(), first); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if err := sess.Append(context.Background(), session.NewCompactionEvent(
		sess.ID(),
		session.CompactionSnapshot{
			Strategy:      "summarize",
			CutoffEventID: first.ID.String(),
			Entries: []session.HistoryEntry{{
				Message: llm.Message{
					Role:    llm.RoleSystem,
					Content: "<conversation_summary>\nPrevious stable summary\n</conversation_summary>",
				},
			}},
		},
	)); err != nil {
		t.Fatalf("append compaction: %v", err)
	}
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("new user details ", 20)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("new assistant details ", 20)},
		{Role: llm.RoleUser, Content: strings.Repeat("latest request ", 20)},
	} {
		if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}

	var captured string
	provider := &mockProvider{
		id: "mock",
		genFn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			captured = req.Messages[len(req.Messages)-1].Content
			return &llm.Response{Content: "Updated summary"}, nil
		},
	}
	processor := NewSummarizer(100, provider, "mock-model")
	processor.ThresholdPct = 0.10
	processor.MinKeepTurns = 1

	if err := processor.Mutate(context.Background(), nil, "", sess); err != nil {
		t.Fatalf("processor failed: %v", err)
	}
	if !strings.Contains(captured, "<existing_summary>") ||
		!strings.Contains(captured, "Previous stable summary") ||
		!strings.Contains(captured, "<new_segments>") {
		t.Fatalf("expected update prompt content, got %q", captured)
	}
}

func TestSummarizerMinKeepTurnsKeepsActiveToolTurn(t *testing.T) {
	sess := session.New("split-turn")
	call := llm.Call{ID: "call-1", Type: "function"}
	call.Function.Name = "read_file"
	call.Function.Arguments = `{"path":"README.md"}`
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("older user ", 20)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("older assistant ", 20)},
		{Role: llm.RoleUser, Content: "inspect README.md"},
		{Role: llm.RoleAssistant, Calls: []llm.Call{call}},
		{Role: llm.RoleTool, Name: "read_file", ToolID: "call-1", Content: "README content"},
	} {
		if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}

	var mu sync.Mutex
	requests := make([]*llm.Request, 0, 2)
	provider := &mockProvider{
		id: "mock",
		genFn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			mu.Lock()
			requests = append(requests, req)
			mu.Unlock()
			if strings.Contains(req.Messages[0].Content, "partial agent turn") {
				return &llm.Response{Content: "Active tool call prefix summary"}, nil
			}
			return &llm.Response{Content: "Stable history summary"}, nil
		},
	}
	processor := NewSummarizer(100, provider, "mock-model")
	processor.ThresholdPct = 0.10
	processor.MinKeepTurns = 1

	if err := processor.Mutate(context.Background(), nil, "", sess); err != nil {
		t.Fatalf("processor failed: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("summary requests = %d, want 1", len(requests))
	}

	entries, err := sess.EffectiveEntries()
	if err != nil {
		t.Fatalf("EffectiveEntries: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected compacted entries, got %d", len(entries))
	}
	summary := entries[0].Message.Content
	if !strings.Contains(summary, "Stable history summary") {
		t.Fatalf("summary missing stable history: %s", summary)
	}
	if strings.Contains(summary, "Active tool call prefix summary") ||
		strings.Contains(summary, "## Active Turn Prefix") {
		t.Fatalf("summary should not consume the retained active turn: %s", summary)
	}
}

func TestSummarizerSplitTurnKeepsToolResultWithAssistantCall(t *testing.T) {
	sess := session.New("split-turn-tool-group")
	call := llm.Call{ID: "call-1", Type: "function"}
	call.Function.Name = "read_file"
	call.Function.Arguments = `{"path":"README.md"}`
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("older user ", 20)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("older assistant ", 20)},
		{Role: llm.RoleUser, Content: "inspect README.md"},
		{Role: llm.RoleAssistant, Calls: []llm.Call{call}},
		{Role: llm.RoleTool, Name: "read_file", ToolID: "call-1", Content: "README content"},
	} {
		if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}

	provider := &mockProvider{
		id: "mock",
		genFn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			if strings.Contains(req.Messages[0].Content, "partial agent turn") {
				return &llm.Response{Content: "Active request summary"}, nil
			}
			return &llm.Response{Content: "Stable history summary"}, nil
		},
	}
	processor := NewSummarizer(100, provider, "mock-model")
	processor.ThresholdPct = 0.10
	processor.MinKeepTurns = 1

	if err := processor.Mutate(context.Background(), nil, "", sess); err != nil {
		t.Fatalf("processor failed: %v", err)
	}

	messages, err := sess.EffectiveMessages()
	if err != nil {
		t.Fatalf("EffectiveMessages: %v", err)
	}
	toolCallIndex, toolResultIndex := -1, -1
	for i, msg := range messages {
		if msg.Role == llm.RoleAssistant && len(msg.Calls) == 1 &&
			msg.Calls[0].ID == "call-1" {
			toolCallIndex = i
		}
		if msg.Role == llm.RoleTool && msg.ToolID == "call-1" &&
			msg.Content == "README content" {
			toolResultIndex = i
		}
	}
	if toolCallIndex < 0 || toolResultIndex < 0 || toolResultIndex < toolCallIndex {
		t.Fatalf(
			"compacted history lost assistant/tool group, call=%d result=%d messages=%#v",
			toolCallIndex,
			toolResultIndex,
			messages,
		)
	}
}

func containsMessageContent(messages []llm.Message, want string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, want) {
			return true
		}
	}
	return false
}
