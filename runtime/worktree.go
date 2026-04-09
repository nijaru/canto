package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/oklog/ulid/v2"
)

// WorktreeSpec configures an optional isolated git worktree for a child run.
type WorktreeSpec struct {
	RepositoryPath string
	Path           string
	Ref            string
	Keep           bool
}

// Worktree owns the lifecycle of one detached git worktree.
type Worktree struct {
	repositoryPath string
	path           string
	keep           bool
}

// PrepareWorktree creates a detached git worktree for child execution.
func PrepareWorktree(ctx context.Context, spec WorktreeSpec) (*Worktree, error) {
	if spec.RepositoryPath == "" {
		return nil, fmt.Errorf("prepare worktree: repository path is required")
	}
	repo, err := filepath.Abs(spec.RepositoryPath)
	if err != nil {
		return nil, fmt.Errorf("prepare worktree: %w", err)
	}
	ref := spec.Ref
	if ref == "" {
		ref = "HEAD"
	}
	path := spec.Path
	if path == "" {
		path = filepath.Join(os.TempDir(), "canto-worktree-"+ulid.Make().String())
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("prepare worktree path: %w", err)
	}

	if err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return nil, fmt.Errorf("prepare worktree: verify repo: %w", err)
	}
	if err := runGit(ctx, repo, "worktree", "add", "--detach", path, ref); err != nil {
		return nil, fmt.Errorf("prepare worktree: add: %w", err)
	}
	return &Worktree{
		repositoryPath: repo,
		path:           path,
		keep:           spec.Keep,
	}, nil
}

func (w *Worktree) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

func (w *Worktree) RepositoryPath() string {
	if w == nil {
		return ""
	}
	return w.repositoryPath
}

func (w *Worktree) Close() {
	if w == nil || w.path == "" || w.keep {
		return
	}
	_ = runGit(context.Background(), w.repositoryPath, "worktree", "remove", "--force", w.path)
	_ = os.RemoveAll(w.path)
}

func workspacePath(w *Worktree) string {
	if w == nil {
		return ""
	}
	return w.Path()
}

func runGit(ctx context.Context, repo string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, string(out))
	}
	return nil
}
