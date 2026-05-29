package workspacetool

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nijaru/ion/workspace"
)

type editReplacement struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type matchedReplacement struct {
	editIndex int
	start     int
	end       int
	after     string
}

func applyEdit(
	root workspace.WorkspaceFS,
	path string,
	edits []editReplacement,
) (string, int, error) {
	data, err := root.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	return applyEditToContent(path, string(data), edits)
}

func applyEditToContent(path, content string, edits []editReplacement) (string, int, error) {
	if len(edits) == 0 {
		return "", 0, fmt.Errorf("edit %s: edits must contain at least one replacement", path)
	}

	matches := make([]matchedReplacement, 0, len(edits))
	for i, edit := range edits {
		if edit.Before == "" {
			return "", 0, fmt.Errorf("edit %s[%d]: before must not be empty", path, i)
		}
		if edit.Before == edit.After {
			return "", 0, fmt.Errorf("edit %s[%d]: before and after are identical", path, i)
		}
		indexes := replacementIndexes(content, edit.Before)
		switch len(indexes) {
		case 0:
			return "", 0, fmt.Errorf("edit %s[%d]: exact match not found", path, i)
		case 1:
		default:
			return "", 0, fmt.Errorf(
				"edit %s[%d]: exact match is ambiguous (%d matches)",
				path,
				i,
				len(indexes),
			)
		}
		start := indexes[0]
		matches = append(matches, matchedReplacement{
			editIndex: i,
			start:     start,
			end:       start + len(edit.Before),
			after:     edit.After,
		})
	}

	slices.SortFunc(matches, func(a, b matchedReplacement) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return a.end - b.end
	})
	for i := 1; i < len(matches); i++ {
		prev := matches[i-1]
		current := matches[i]
		if prev.end > current.start {
			return "", 0, fmt.Errorf(
				"edit %s[%d] overlaps edit[%d]; merge overlapping replacements",
				path,
				prev.editIndex,
				current.editIndex,
			)
		}
	}

	updated := content
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		updated = updated[:match.start] + match.after + updated[match.end:]
	}
	if updated == content {
		return "", 0, fmt.Errorf("edit %s: replacements produced no content change", path)
	}
	return updated, len(matches), nil
}

func replacementIndexes(content, before string) []int {
	var indexes []int
	offset := 0
	for {
		index := strings.Index(content[offset:], before)
		if index < 0 {
			return indexes
		}
		absolute := offset + index
		indexes = append(indexes, absolute)
		offset = absolute + len(before)
	}
}

func previewEdits(edits []editReplacement) string {
	parts := make([]string, 0, len(edits))
	for _, edit := range edits {
		parts = append(parts, fmt.Sprintf("--- before\n%s\n+++ after\n%s", edit.Before, edit.After))
	}
	return strings.Join(parts, "\n")
}
