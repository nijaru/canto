package governor

import (
	"slices"
	"testing"

	"github.com/nijaru/canto/llm"
)

func TestExtractFilePathsTracksCommonCodingToolNames(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleAssistant, Calls: []llm.Call{
			toolCall("read", `{"file_path":"README.md"}`),
			toolCall("list", `{"path":"governor"}`),
			toolCall("grep", `{"path":"session","pattern":"Effective"}`),
			toolCall("write", `{"file_path":"tmp/out.txt"}`),
			toolCall("edit", `{"file_path":"internal/app.go"}`),
			toolCall("multi_edit", `{"file_path":"internal/model.go"}`),
		}},
	}

	read, modified := extractFilePaths(messages)

	wantRead := []string{"README.md", "governor", "session"}
	wantModified := []string{"internal/app.go", "internal/model.go", "tmp/out.txt"}
	if !slices.Equal(read, wantRead) {
		t.Fatalf("read paths = %#v, want %#v", read, wantRead)
	}
	if !slices.Equal(modified, wantModified) {
		t.Fatalf("modified paths = %#v, want %#v", modified, wantModified)
	}
}

func toolCall(name, args string) llm.Call {
	call := llm.Call{ID: "call-" + name, Type: "function"}
	call.Function.Name = name
	call.Function.Arguments = args
	return call
}
