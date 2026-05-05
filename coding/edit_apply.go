package coding

import (
	"fmt"
	"strings"

	"github.com/nijaru/canto/workspace"
)

type preparedEdit struct {
	path         string
	replacements int
	before       string
	after        string
}

func applyEdit(root workspace.WorkspaceFS, path, before, after string) (string, int, error) {
	data, err := root.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	return applyEditToContent(path, string(data), before, after)
}

func applyEditToContent(path, content, before, after string) (string, int, error) {
	count := strings.Count(content, before)
	if count == 0 {
		return "", 0, fmt.Errorf("edit %s: exact match not found", path)
	}
	if count > 1 {
		return "", 0, fmt.Errorf("edit %s: exact match is ambiguous (%d matches)", path, count)
	}
	return strings.Replace(content, before, after, 1), 1, nil
}

func previewChange(before, after string) string {
	return fmt.Sprintf("--- before\n%s\n+++ after\n%s", before, after)
}
