package governor

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nijaru/canto/artifact"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

type compactTestProvider struct {
	id      string
	countFn func(ctx context.Context, model string, messages []llm.Message) (int, error)
	genFn   func(ctx context.Context, req *llm.Request) (*llm.Response, error)
}

func (p *compactTestProvider) ID() string { return p.id }

func (p *compactTestProvider) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	if p.genFn == nil {
		return &llm.Response{}, nil
	}
	return p.genFn(ctx, req)
}

func (p *compactTestProvider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return nil, nil
}

func (p *compactTestProvider) Models(ctx context.Context) ([]llm.Model, error) {
	return nil, nil
}

func (p *compactTestProvider) CountTokens(
	ctx context.Context,
	model string,
	messages []llm.Message,
) (int, error) {
	if p.countFn == nil {
		return 0, errors.New("count tokens unsupported")
	}
	return p.countFn(ctx, model, messages)
}

func (p *compactTestProvider) Cost(ctx context.Context, model string, usage llm.Usage) float64 {
	return 0
}

func (p *compactTestProvider) Capabilities(model string) llm.Capabilities {
	return llm.DefaultCapabilities()
}

func (p *compactTestProvider) IsTransient(err error) bool       { return false }
func (p *compactTestProvider) IsContextOverflow(err error) bool { return false }

type noopArtifactStore struct{}

func (noopArtifactStore) Put(
	ctx context.Context,
	desc artifact.Descriptor,
	r io.Reader,
) (artifact.Descriptor, error) {
	return desc, nil
}

func (noopArtifactStore) Stat(ctx context.Context, id string) (artifact.Descriptor, error) {
	return artifact.Descriptor{}, nil
}

func (noopArtifactStore) Open(
	ctx context.Context,
	id string,
) (io.ReadCloser, artifact.Descriptor, error) {
	return io.NopCloser(strings.NewReader("")), artifact.Descriptor{}, nil
}

func TestCompactSessionValidation(t *testing.T) {
	validProvider := &compactTestProvider{id: "mock"}
	validSession := session.New("compact-validation")
	validOpts := CompactOptions{
		MaxTokens:  100,
		OffloadDir: t.TempDir(),
	}

	tests := []struct {
		name     string
		provider llm.Provider
		model    string
		sess     *session.Session
		opts     CompactOptions
	}{
		{
			name:  "nil provider",
			model: "mock-model",
			sess:  validSession,
			opts:  validOpts,
		},
		{
			name:     "empty model",
			provider: validProvider,
			sess:     validSession,
			opts:     validOpts,
		},
		{
			name:     "nil session",
			provider: validProvider,
			model:    "mock-model",
			opts:     validOpts,
		},
		{
			name:     "non-positive max tokens",
			provider: validProvider,
			model:    "mock-model",
			sess:     validSession,
			opts: CompactOptions{
				OffloadDir: t.TempDir(),
			},
		},
		{
			name:     "missing offload target",
			provider: validProvider,
			model:    "mock-model",
			sess:     validSession,
			opts: CompactOptions{
				MaxTokens: 100,
			},
		},
		{
			name:     "multiple offload targets",
			provider: validProvider,
			model:    "mock-model",
			sess:     validSession,
			opts: CompactOptions{
				MaxTokens:  100,
				OffloadDir: t.TempDir(),
				Artifacts:  noopArtifactStore{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CompactSession(t.Context(), tt.provider, tt.model, tt.sess, tt.opts)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCompactSessionNoOpBelowThreshold(t *testing.T) {
	sess := session.New("compact-noop")
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
		{Role: llm.RoleUser, Content: "small turn"},
	} {
		if err := sess.Append(t.Context(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	var generateCalls int
	provider := &compactTestProvider{
		id: "mock",
		genFn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			generateCalls++
			return &llm.Response{Content: "should not run"}, nil
		},
	}

	result, err := CompactSession(t.Context(), provider, "mock-model", sess, CompactOptions{
		MaxTokens:  1000,
		OffloadDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CompactSession: %v", err)
	}
	if result.Compacted {
		t.Fatal("expected no-op compaction result")
	}

	if generateCalls != 0 {
		t.Fatalf("expected summarizer provider not to run, got %d calls", generateCalls)
	}
	if got := compactionStrategies(t, sess); len(got) != 0 {
		t.Fatalf("expected no compaction events, got %v", got)
	}
}

func TestOffloaderSkipsPreCompactWhenTooFewTurns(t *testing.T) {
	sess := session.New("offload-short")
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("alpha ", 80)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("beta ", 80)},
	} {
		if err := sess.Append(t.Context(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	compactCalled := false
	provider := &compactTestProvider{
		id: "mock",
		countFn: func(ctx context.Context, model string, messages []llm.Message) (int, error) {
			return 1000, nil
		},
	}
	offloader := NewOffloader(100, t.TempDir())
	offloader.ThresholdPct = 0.50
	offloader.MinKeepTurns = 3
	offloader.OnPreCompact = func(ctx context.Context, sess *session.Session) {
		compactCalled = true
	}

	if err := offloader.Mutate(t.Context(), provider, "mock-model", sess); err != nil {
		t.Fatalf("offloader mutate: %v", err)
	}
	if compactCalled {
		t.Fatal("expected OnPreCompact to stay idle when offloader has no candidates")
	}
}

func TestCompactSessionOffloadsBeforeSummarize(t *testing.T) {
	sess := session.New("compact-heavy")
	largeToolContent := strings.Repeat("tool output ", 400)
	call := llm.Call{ID: "tool-1", Type: "function"}
	call.Function.Name = "read"
	for _, msg := range []llm.Message{
		{Role: llm.RoleSystem, Content: "You are helpful."},
		{Role: llm.RoleUser, Content: strings.Repeat("alpha ", 80)},
		{Role: llm.RoleAssistant, Content: strings.Repeat("beta ", 80), Calls: []llm.Call{call}},
		{Role: llm.RoleTool, Content: largeToolContent, ToolID: "tool-1"},
		{Role: llm.RoleAssistant, Content: strings.Repeat("gamma ", 80)},
		{Role: llm.RoleUser, Content: "recent question"},
		{Role: llm.RoleAssistant, Content: "recent answer"},
	} {
		if err := sess.Append(t.Context(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	var summaryInput string
	provider := &compactTestProvider{
		id: "mock",
		genFn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			if len(req.Messages) != 2 {
				t.Fatalf("expected summarizer prompt with 2 messages, got %d", len(req.Messages))
			}
			summaryInput = req.Messages[1].Content
			return &llm.Response{Content: "Summarized conversation"}, nil
		},
	}

	result, err := CompactSession(t.Context(), provider, "mock-model", sess, CompactOptions{
		MaxTokens:    200,
		MinKeepTurns: 2,
		OffloadDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("CompactSession: %v", err)
	}
	if !result.Compacted {
		t.Fatal("expected compaction result to report durable compaction")
	}

	if got := compactionStrategies(t, sess); strings.Join(got, ",") != "offload,summarize" {
		t.Fatalf("expected offload then summarize compaction, got %v", got)
	}
	if !strings.Contains(summaryInput, "[Content offloaded to ") {
		t.Fatalf("expected summarizer input to use offload placeholder, got %q", summaryInput)
	}
	if strings.Contains(summaryInput, largeToolContent[:64]) {
		t.Fatalf("expected summarizer input to exclude raw offloaded tool content")
	}

	effective, err := sess.EffectiveMessages()
	if err != nil {
		t.Fatalf("EffectiveMessages: %v", err)
	}
	if len(effective) != 3 {
		t.Fatalf("expected compacted effective history of 3 messages, got %d", len(effective))
	}
	if effective[0].Content != "<conversation_summary>\nSummarized conversation\n</conversation_summary>" {
		t.Fatalf("unexpected summary message: %q", effective[0].Content)
	}
	if effective[1].Content != "recent question" || effective[2].Content != "recent answer" {
		t.Fatalf("unexpected recent messages after compaction: %#v", effective)
	}
}

func compactionStrategies(t *testing.T, sess *session.Session) []string {
	t.Helper()

	var strategies []string
	for _, event := range sess.Events() {
		snapshot, ok, err := event.CompactionSnapshot()
		if err != nil {
			t.Fatalf("decode compaction snapshot: %v", err)
		}
		if ok {
			strategies = append(strategies, snapshot.Strategy)
		}
	}
	return strategies
}
