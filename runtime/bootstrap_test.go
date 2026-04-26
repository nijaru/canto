package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/workspace"
)

func TestCaptureBootstrapBuildsDeterministicSnapshot(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(rootDir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := workspace.Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	reg := tool.NewRegistry()
	reg.Register(&scopedTool{name: "beta", output: "b"})
	reg.Register(&scopedTool{name: "alpha", output: "a"})

	snap, err := CaptureBootstrap(t.Context(), root, reg)
	if err != nil {
		t.Fatalf("capture bootstrap: %v", err)
	}

	if snap.CWD != root.Path() {
		t.Fatalf("cwd = %q, want %q", snap.CWD, root.Path())
	}
	if got := strings.Join(snap.Files, ","); got != "README.md,cmd/" {
		t.Fatalf("files = %q, want README.md,cmd/", got)
	}
	if got := strings.Join(snap.Tools, ","); got != "alpha,beta" {
		t.Fatalf("tools = %q, want alpha,beta", got)
	}

	rendered := snap.Render()
	if !strings.Contains(rendered, "cwd: "+root.Path()) {
		t.Fatalf("rendered snapshot missing cwd: %q", rendered)
	}
	if !strings.Contains(rendered, "  - alpha") || !strings.Contains(rendered, "  - beta") {
		t.Fatalf("rendered snapshot missing tools: %q", rendered)
	}
}

func TestRunnerBootstrapAppendsContextSnapshot(t *testing.T) {
	store, err := session.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	runner := NewRunner(store, &echoAgent{})
	defer runner.Close()

	sess := session.New("bootstrap-session").WithWriter(store)
	runner.sessions[sess.ID()] = sess

	snap := Bootstrap{
		CWD:   "/workspace",
		Files: []string{"README.md"},
		Tools: []string{"alpha"},
	}
	if err := runner.Bootstrap(t.Context(), sess.ID(), snap); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	reloaded, err := store.Load(t.Context(), sess.ID())
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if len(reloaded.Messages()) != 0 {
		t.Fatalf("expected bootstrap outside transcript, got %#v", reloaded.Messages())
	}
	messages, err := reloaded.EffectiveMessages()
	if err != nil {
		t.Fatalf("effective messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected bootstrap context message, got %#v", messages)
	}
	if got := messages[0]; got.Role != llm.RoleUser ||
		!strings.Contains(got.Content, "/workspace") {
		t.Fatalf("bootstrap context = %#v", got)
	}
}
