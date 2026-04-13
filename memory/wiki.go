package memory

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/nijaru/canto/workspace"
)

// IngestWiki walks a workspace directory of markdown files and writes them
// into the memory manager. This implements the LLM-Wiki ingest pattern, converting
// raw file trees into high-density retrievable context.
func IngestWiki(
	ctx context.Context,
	ws workspace.WorkspaceFS,
	dir string,
	writer Writer,
	namespace Namespace,
	role Role,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if ws == nil {
		return 0, fmt.Errorf("memory wiki ingest: nil workspace fs")
	}
	if writer == nil {
		return 0, fmt.Errorf("memory wiki ingest: nil writer")
	}

	count := 0
	err := fs.WalkDir(ws.FS(), dir, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			// If the directory doesn't exist, we just skip it gracefully
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		data, err := ws.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read wiki file %s: %w", path, err)
		}

		content := string(data)
		metadata := make(map[string]any)
		metadata["path"] = path
		metadata["filename"] = d.Name()

		// Attempt to extract YAML frontmatter
		if strings.HasPrefix(content, "---\n") || strings.HasPrefix(content, "---\r\n") {
			// Normalize to \n for easier splitting
			normalized := strings.ReplaceAll(content, "\r\n", "\n")
			if endIdx := strings.Index(normalized[4:], "\n---\n"); endIdx != -1 {
				frontmatter := normalized[4 : 4+endIdx]
				content = strings.TrimSpace(normalized[4+endIdx+5:])
				// Simple line-based parse for summary, title, etc
				for _, line := range strings.Split(frontmatter, "\n") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						k := strings.TrimSpace(parts[0])
						v := strings.TrimSpace(parts[1])
						v = strings.Trim(v, "\"'")
						metadata[k] = v
					}
				}
			}
		}

		key := strings.TrimSuffix(d.Name(), ".md")

		_, err = writer.Write(ctx, WriteInput{
			Namespace: namespace,
			Role:      role,
			Key:       key,
			Content:   content,
			Metadata:  metadata,
			Mode:      WriteSync,
		})
		if err != nil {
			return fmt.Errorf("ingest wiki file %s: %w", path, err)
		}
		count++
		return nil
	})

	return count, err
}
