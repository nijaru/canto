package context

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/workspace"
)

type FileReferenceOptions struct {
	MaxFiles int
	MaxBytes int
	Prefix   string
}

// FileReferenceRecorder records newly referenced files into durable session
// state so later turns can avoid re-expanding the same content.
type FileReferenceRecorder struct {
	Root workspace.WorkspaceFS
	Opts FileReferenceOptions
}

// RecordFileReferences returns a mutator that records newly referenced files
// as durable internal session facts.
func RecordFileReferences(
	root workspace.WorkspaceFS,
	opts FileReferenceOptions,
) prompt.ContextMutator {
	return &FileReferenceRecorder{Root: root, Opts: opts}
}

// FileReferences returns the durable recorder and the request injector for the
// same file-reference policy.
func FileReferences(
	root workspace.WorkspaceFS,
	opts FileReferenceOptions,
) (prompt.ContextMutator, prompt.RequestProcessor) {
	return RecordFileReferences(root, opts), FileReferencePrompt(root, opts)
}

func (r *FileReferenceRecorder) Effects() prompt.SideEffects {
	return prompt.SideEffects{Session: true}
}

func (r *FileReferenceRecorder) Mutate(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
) error {
	if r == nil || r.Root == nil || sess == nil {
		return nil
	}
	currentEventID, refs, err := referencedFiles(ctx, r.Root, sess, r.Opts)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	seen, err := recordedFileReferenceIDs(sess, currentEventID)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if _, ok := seen[ref.ref.Identity()]; ok {
			continue
		}
		if err := session.RecordArtifact(ctx, sess, session.ArtifactRecordedData{
			Artifact: session.ArtifactRef{
				ID:              ref.ref.Identity(),
				Kind:            session.ArtifactKindWorkspaceFileRef,
				URI:             "workspace://" + ref.ref.Path,
				Label:           ref.ref.Path,
				MIMEType:        "text/plain",
				Size:            ref.ref.Size,
				Digest:          ref.ref.Digest,
				ProducerEventID: currentEventID,
				Metadata: map[string]any{
					"path":     ref.ref.Path,
					"mod_time": ref.ref.ModTime.UTC().Format(time.RFC3339Nano),
				},
			},
		}); err != nil {
			return fmt.Errorf("record file reference %s: %w", ref.ref.Path, err)
		}
	}
	return nil
}

func FileReferencePrompt(
	root workspace.WorkspaceFS,
	opts FileReferenceOptions,
) prompt.RequestProcessor {
	return prompt.RequestProcessorFunc(func(
		ctx context.Context,
		p llm.Provider,
		model string,
		sess *session.Session,
		req *llm.Request,
	) error {
		if root == nil {
			return nil
		}
		currentEventID, refs, err := referencedFiles(ctx, root, sess, opts)
		if err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}
		seen, err := recordedFileReferenceIDs(sess, currentEventID)
		if err != nil {
			return err
		}
		maxBytes := opts.MaxBytes
		if maxBytes <= 0 {
			maxBytes = 16 << 10
		}
		var sb strings.Builder
		sb.WriteString("<file_references>\n")
		for _, ref := range refs {
			if _, ok := seen[ref.ref.Identity()]; ok {
				fmt.Fprintf(
					&sb,
					"<file path=%q ref=%q size=%d cached=\"true\"/>\n",
					ref.ref.Path,
					ref.ref.Digest,
					ref.ref.Size,
				)
				continue
			}
			seen[ref.ref.Identity()] = struct{}{}
			content := ref.body
			if len(content) > maxBytes {
				content = content[:maxBytes]
			}
			fmt.Fprintf(
				&sb,
				"<file path=%q ref=%q size=%d>\n%s\n</file>\n",
				ref.ref.Path,
				ref.ref.Digest,
				ref.ref.Size,
				content,
			)
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

type resolvedFileReference struct {
	ref  workspace.ContentRef
	body string
}

func referencedFiles(
	ctx context.Context,
	root workspace.WorkspaceFS,
	sess *session.Session,
	opts FileReferenceOptions,
) (string, []resolvedFileReference, error) {
	prefix := opts.Prefix
	if prefix == "" {
		prefix = "@"
	}
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 5
	}

	messages, err := sess.EffectiveMessages()
	if err != nil {
		return "", nil, err
	}
	if len(messages) == 0 {
		return "", nil, nil
	}
	last := messages[len(messages)-1]
	if last.Role != llm.RoleUser || last.Content == "" {
		return "", nil, nil
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta(prefix) + `([^\s]+)`)
	matches := pattern.FindAllStringSubmatch(last.Content, maxFiles)
	if len(matches) == 0 {
		return "", nil, nil
	}

	entries, err := sess.EffectiveEntries()
	if err != nil {
		return "", nil, err
	}
	currentEventID := ""
	if len(entries) > 0 {
		currentEventID = entries[len(entries)-1].EventID
	}

	seen := make(map[string]struct{}, len(matches))
	resolved := make([]resolvedFileReference, 0, len(matches))
	for _, match := range matches {
		path := match[1]
		ref, data, err := workspace.RefFile(ctx, root, path)
		if err != nil {
			return "", nil, fmt.Errorf("file reference %s: %w", path, err)
		}
		if _, ok := seen[ref.Identity()]; ok {
			continue
		}
		seen[ref.Identity()] = struct{}{}
		resolved = append(resolved, resolvedFileReference{
			ref:  ref,
			body: string(data),
		})
	}
	return currentEventID, resolved, nil
}

func recordedFileReferenceIDs(
	sess *session.Session,
	excludeEventID string,
) (map[string]struct{}, error) {
	seen := make(map[string]struct{})
	for e := range sess.All() {
		data, ok, err := e.ArtifactRecordedData()
		if err != nil {
			return nil, err
		}
		if !ok || !session.IsWorkspaceFileReferenceArtifact(data.Artifact) {
			continue
		}
		if excludeEventID != "" && data.Artifact.ProducerEventID == excludeEventID {
			continue
		}
		if data.Artifact.Digest != "" {
			seen[data.Artifact.Digest] = struct{}{}
		}
		if data.Artifact.ID != "" {
			seen[data.Artifact.ID] = struct{}{}
		}
	}
	return seen, nil
}
