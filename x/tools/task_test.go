package tools

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestTaskTool_AddListDoneLog(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	tt := &TaskTool{root: root, Project: "proj"}

	ctx := context.Background()

	// add
	out, err := tt.Execute(ctx, `{"action":"add","title":"My task","priority":2}`)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.HasPrefix(out, "created proj-") {
		t.Fatalf("unexpected add output: %s", out)
	}
	// extract ref from output like "created proj-xxxx: My task"
	parts := strings.SplitN(out, " ", 3)
	ref := strings.TrimSuffix(parts[1], ":") // "proj-xxxx"

	// list all
	out, err = tt.Execute(ctx, `{"action":"list"}`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, ref) {
		t.Fatalf("list output doesn't contain ref %s:\n%s", ref, out)
	}

	// log
	out, err = tt.Execute(ctx, `{"action":"log","ref":"`+ref+`","message":"progress note"}`)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(out, "logged") {
		t.Errorf("unexpected log output: %s", out)
	}

	// verify log entry persisted
	f, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}
	rec, err := tt.readTask(entries[0].Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Logs) != 1 || rec.Logs[0].Msg != "progress note" {
		t.Errorf("unexpected logs: %v", rec.Logs)
	}

	// done
	out, err = tt.Execute(ctx, `{"action":"done","ref":"`+ref+`"}`)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(out, "marked done") {
		t.Errorf("unexpected done output: %s", out)
	}

	// list with status filter — should appear
	out, err = tt.Execute(ctx, `{"action":"list","status":"done"}`)
	if err != nil {
		t.Fatalf("list done: %v", err)
	}
	if !strings.Contains(out, ref) {
		t.Fatalf("list done missing ref %s:\n%s", ref, out)
	}

	// list with open filter — should be absent
	out, err = tt.Execute(ctx, `{"action":"list","status":"open"}`)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if strings.Contains(out, ref) {
		t.Errorf("list open unexpectedly contains done task %s", ref)
	}
}

func TestTaskTool_PreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	tt := &TaskTool{root: root, Project: "proj"}

	const filename = "proj-abcd.json"
	if err := root.WriteFile(filename, []byte(`{
  "project": "proj",
  "ref": "abcd",
  "title": "Preserve me",
  "status": "open",
  "priority": 2,
  "labels": [],
  "assignees": [],
  "blocked_by": [],
  "logs": [],
  "created_at": "2026-05-05T12:00:00Z",
  "updated_at": "2026-05-05T12:00:00Z",
  "external": {},
  "custom_field": {"nested": true},
  "custom_list": [1, 2, 3]
}
`), 0o644); err != nil {
		t.Fatalf("write seed task: %v", err)
	}

	if _, err := tt.Execute(t.Context(), `{"action":"log","ref":"proj-abcd","message":"note"}`); err != nil {
		t.Fatalf("log: %v", err)
	}
	updated, err := root.ReadFile(filename)
	if err != nil {
		t.Fatalf("read updated task: %v", err)
	}
	text := string(updated)
	if !strings.Contains(text, `"custom_field": {`) ||
		!strings.Contains(text, `"nested": true`) ||
		!strings.Contains(text, `"custom_list": [`) {
		t.Fatalf("expected unknown fields to be preserved, got:\n%s", text)
	}
}

func TestTaskTool_Errors(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	tt := &TaskTool{root: root, Project: "p"}
	ctx := context.Background()

	if _, err := tt.Execute(ctx, `{"action":"add"}`); err == nil {
		t.Error("expected error for add without title")
	}
	if _, err := tt.Execute(ctx, `{"action":"done"}`); err == nil {
		t.Error("expected error for done without ref")
	}
	if _, err := tt.Execute(ctx, `{"action":"log","ref":"p-xxxx"}`); err == nil {
		t.Error("expected error for log without message")
	}
	if _, err := tt.Execute(ctx, `{"action":"done","ref":"p-notexist"}`); err == nil {
		t.Error("expected error for done with nonexistent ref")
	}
}
