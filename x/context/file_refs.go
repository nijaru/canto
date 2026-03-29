package context

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/workspace"
)

type FileReferenceOptions struct {
	MaxFiles int
	MaxBytes int
	Prefix   string
}

func FileReferencePrompt(
	root *workspace.Root,
	opts FileReferenceOptions,
) ccontext.RequestProcessor {
	prefix := opts.Prefix
	if prefix == "" {
		prefix = "@"
	}
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 5
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 16 << 10
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta(prefix) + `([^\s]+)`)

	return ccontext.RequestProcessorFunc(func(
		ctx context.Context,
		p llm.Provider,
		model string,
		sess *session.Session,
		req *llm.Request,
	) error {
		if root == nil {
			return nil
		}
		messages, err := sess.EffectiveMessages()
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}
		last := messages[len(messages)-1]
		if last.Role != llm.RoleUser || last.Content == "" {
			return nil
		}
		matches := pattern.FindAllStringSubmatch(last.Content, maxFiles)
		if len(matches) == 0 {
			return nil
		}
		var sb strings.Builder
		sb.WriteString("<file_references>\n")
		for _, match := range matches {
			path := match[1]
			data, err := root.ReadFile(path)
			if err != nil {
				return fmt.Errorf("file reference %s: %w", path, err)
			}
			content := string(data)
			if len(content) > maxBytes {
				content = content[:maxBytes]
			}
			fmt.Fprintf(&sb, "<file path=%q>\n%s\n</file>\n", path, content)
		}
		sb.WriteString("</file_references>")
		injectFileReferences(req, sb.String())
		return nil
	})
}

var fileReferenceRegex = regexp.MustCompile(`(?s)<file_references>.*?</file_references>\n*`)

func injectFileReferences(req *llm.Request, block string) {
	for i, msg := range req.Messages {
		if msg.Role != llm.RoleSystem {
			continue
		}
		if loc := fileReferenceRegex.FindStringIndex(msg.Content); loc != nil {
			req.Messages[i].Content = msg.Content[:loc[0]] + block + "\n\n" + msg.Content[loc[1]:]
		} else {
			req.Messages[i].Content = block + "\n\n" + msg.Content
		}
		return
	}
	req.Messages = append([]llm.Message{{Role: llm.RoleSystem, Content: block}}, req.Messages...)
}
