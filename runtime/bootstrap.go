package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/workspace"
)

// Bootstrap captures the initial workspace and tool context for a session.
type Bootstrap struct {
	CWD   string
	Files []string
	Tools []string
}

// CaptureBootstrap snapshots the workspace root and available tools.
func CaptureBootstrap(
	ctx context.Context,
	root *workspace.Root,
	reg *tool.Registry,
) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if root == nil {
		return Bootstrap{}, errors.New("bootstrap: nil workspace root")
	}

	entries, err := root.ReadDir(".")
	if err != nil {
		return Bootstrap{}, fmt.Errorf("bootstrap: read workspace: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return Bootstrap{}, err
		}
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		files = append(files, name)
	}
	slices.Sort(files)

	var tools []string
	if reg != nil {
		tools = reg.Names()
	}

	return Bootstrap{
		CWD:   root.Path(),
		Files: files,
		Tools: tools,
	}, nil
}

// Render formats the snapshot as a compact system prompt block.
func (b Bootstrap) Render() string {
	var out strings.Builder
	out.WriteString("# Workspace Snapshot\n")
	out.WriteString("- cwd: ")
	out.WriteString(b.CWD)
	out.WriteByte('\n')
	out.WriteString("- files:\n")
	if len(b.Files) == 0 {
		out.WriteString("  - (none)\n")
	} else {
		for _, file := range b.Files {
			out.WriteString("  - ")
			out.WriteString(file)
			out.WriteByte('\n')
		}
	}
	out.WriteString("- tools:\n")
	if len(b.Tools) == 0 {
		out.WriteString("  - (none)\n")
	} else {
		for _, name := range b.Tools {
			out.WriteString("  - ")
			out.WriteString(name)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// Message returns the bootstrap snapshot as a system message.
func (b Bootstrap) Message() llm.Message {
	return llm.Message{
		Role:    llm.RoleSystem,
		Content: b.Render(),
	}
}

// Append records the bootstrap snapshot as the first system message in sess.
func (b Bootstrap) Append(ctx context.Context, sess *session.Session) error {
	if sess == nil {
		return errors.New("bootstrap: nil session")
	}
	return sess.Append(ctx, session.NewMessage(sess.ID(), b.Message()))
}
