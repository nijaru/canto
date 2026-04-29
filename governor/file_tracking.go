package governor

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// toolFileActions classifies tools into read vs modify operations.
var toolFileActions = map[string]string{
	"read":       "read",
	"read_file":  "read",
	"list":       "read",
	"list_dir":   "read",
	"grep":       "read",
	"glob":       "read",
	"search":     "read",
	"write":      "write",
	"write_file": "write",
	"edit":       "edit",
	"multi_edit": "edit",
}

// extractFilePaths scans tool calls and tool results in the given messages to
// build deduplicated read and modified file lists.
func extractFilePaths(messages []llm.Message) (read, modified []string) {
	seenRead := make(map[string]bool)
	seenMod := make(map[string]bool)

	for _, m := range messages {
		for _, call := range m.Calls {
			action, ok := toolFileActions[call.Function.Name]
			if !ok {
				continue
			}
			path := extractPath(call.Function.Arguments)
			if path == "" {
				continue
			}
			switch action {
			case "read":
				if !seenRead[path] {
					seenRead[path] = true
					read = append(read, path)
				}
			case "write", "edit":
				if !seenMod[path] {
					seenMod[path] = true
					modified = append(modified, path)
				}
			}
		}
	}

	// Modified files are also implicitly read.
	for _, p := range modified {
		if !seenRead[p] {
			seenRead[p] = true
			read = append(read, p)
		}
	}

	// Remove modified files from read-only list.
	read = subtractPaths(read, modified)

	sort.Strings(read)
	sort.Strings(modified)
	return read, modified
}

// extractPath attempts to pull a path value from common coding-tool argument
// shapes.
func extractPath(args string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil {
		return ""
	}
	for _, key := range []string{"path", "file_path"} {
		p, ok := obj[key]
		if !ok {
			continue
		}
		s, ok := p.(string)
		if !ok {
			continue
		}
		return s
	}
	return ""
}

// subtractPaths returns a - b.
func subtractPaths(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, p := range b {
		set[p] = true
	}
	out := make([]string, 0, len(a))
	for _, p := range a {
		if !set[p] {
			out = append(out, p)
		}
	}
	return out
}

// mergeFileLists combines new and inherited file lists, deduplicating.
func mergeFileLists(new, inherited []string) []string {
	seen := make(map[string]bool, len(inherited))
	for _, p := range inherited {
		seen[p] = true
	}
	out := slices.Clone(inherited)
	for _, p := range new {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// previousFileLists reads ReadFiles and ModifiedFiles from the most recent
// compaction snapshot in the session, if one exists.
func previousFileLists(sess *session.Session) (read, modified []string) {
	for _, e := range sess.Events() {
		snap, ok, err := e.CompactionSnapshot()
		if err != nil || !ok {
			continue
		}
		read = snap.ReadFiles
		modified = snap.ModifiedFiles
	}
	return
}
