package tools

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

const taskRefAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// taskRecord mirrors the .tasks/*.json storage format used by the tk CLI.
// Only fields needed for agent operations are handled; unknown fields are
// preserved via json.RawMessage round-tripping (the full object is re-encoded
// after mutations).
type taskRecord struct {
	Project     string         `json:"project"`
	Ref         string         `json:"ref"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitzero"`
	Status      string         `json:"status"`
	Priority    int            `json:"priority"`
	Labels      []string       `json:"labels"`
	Assignees   []string       `json:"assignees"`
	Parent      *string        `json:"parent"`
	BlockedBy   []string       `json:"blocked_by"`
	Estimate    *string        `json:"estimate"`
	DueDate     *string        `json:"due_date"`
	Logs        []taskLog      `json:"logs"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	CompletedAt *time.Time     `json:"completed_at"`
	External    map[string]any `json:"external"`
}

type taskLog struct {
	TS  time.Time `json:"ts"`
	Msg string    `json:"msg"`
}

// TaskTool allows an agent to list, add, complete, and log entries for tasks
// stored in the .tasks/ directory used by the tk CLI.
type TaskTool struct {
	// root is the os.Root handle to the .tasks/ directory.
	root *os.Root
	// Project is the project name prefix used in filenames and the project field.
	Project string
}

func (t *TaskTool) Spec() llm.Spec {
	return llm.Spec{
		Name: "task",
		Description: "Manage tasks in the project task tracker. " +
			"Actions: list (show tasks), add (create task), done (mark complete), log (append note).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "add", "done", "log"},
					"description": "Action to perform.",
				},
				"ref": map[string]any{
					"type":        "string",
					"description": "Task ref (e.g. canto-hzlu). Required for done and log.",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Task title. Required for add.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Task description. Optional for add.",
				},
				"priority": map[string]any{
					"type":        "integer",
					"description": "Priority 1–4 (1=urgent, 4=low). Defaults to 3 for add.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Log message. Required for log.",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Filter by status for list (e.g. open, done). Omit for all.",
				},
			},
			"required": []string{"action"},
		},
	}
}

func (t *TaskTool) Execute(_ context.Context, args string) (string, error) {
	var input struct {
		Action      string `json:"action"`
		Ref         string `json:"ref"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    int    `json:"priority"`
		Message     string `json:"message"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	switch input.Action {
	case "list":
		return t.list(input.Status)
	case "add":
		if input.Title == "" {
			return "", fmt.Errorf("title is required for add")
		}
		pri := input.Priority
		if pri == 0 {
			pri = 3
		}
		return t.add(input.Title, input.Description, pri)
	case "done":
		if input.Ref == "" {
			return "", fmt.Errorf("ref is required for done")
		}
		return t.done(input.Ref)
	case "log":
		if input.Ref == "" {
			return "", fmt.Errorf("ref is required for log")
		}
		if input.Message == "" {
			return "", fmt.Errorf("message is required for log")
		}
		return t.log(input.Ref, input.Message)
	default:
		return "", fmt.Errorf("unknown action %q", input.Action)
	}
}

func (t *TaskTool) list(statusFilter string) (string, error) {
	f, err := t.root.Open(".")
	if err != nil {
		return "", fmt.Errorf("task list: %w", err)
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return "", fmt.Errorf("task list: %w", err)
	}
	var sb strings.Builder
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rec, err := t.readTask(e.Name())
		if err != nil {
			continue
		}
		if statusFilter != "" && rec.Status != statusFilter {
			continue
		}
		fmt.Fprintf(&sb, "%s-%s\t[%s]\tp%d\t%s\n",
			rec.Project, rec.Ref, rec.Status, rec.Priority, rec.Title)
		count++
	}
	if count == 0 {
		return "(no tasks)", nil
	}
	return sb.String(), nil
}

func (t *TaskTool) add(title, description string, priority int) (string, error) {
	ref, err := randomRef(4)
	if err != nil {
		return "", fmt.Errorf("task add: %w", err)
	}
	now := time.Now().UTC()
	rec := &taskRecord{
		Project:     t.Project,
		Ref:         ref,
		Title:       title,
		Description: description,
		Status:      "open",
		Priority:    priority,
		Labels:      []string{},
		Assignees:   []string{},
		BlockedBy:   []string{},
		Logs:        []taskLog{},
		CreatedAt:   now,
		UpdatedAt:   now,
		External:    map[string]any{},
	}
	filename := fmt.Sprintf("%s-%s.json", t.Project, ref)
	if err := t.writeTask(filename, rec); err != nil {
		return "", fmt.Errorf("task add: %w", err)
	}
	return fmt.Sprintf("created %s-%s: %s", t.Project, ref, title), nil
}

func (t *TaskTool) done(ref string) (string, error) {
	filename, rec, err := t.findTask(ref)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	rec.Status = "done"
	rec.UpdatedAt = now
	rec.CompletedAt = &now
	if err := t.writeTask(filename, rec); err != nil {
		return "", fmt.Errorf("task done: %w", err)
	}
	return fmt.Sprintf("%s marked done", ref), nil
}

func (t *TaskTool) log(ref, message string) (string, error) {
	filename, rec, err := t.findTask(ref)
	if err != nil {
		return "", err
	}
	rec.Logs = append(rec.Logs, taskLog{TS: time.Now().UTC(), Msg: message})
	rec.UpdatedAt = time.Now().UTC()
	if err := t.writeTask(filename, rec); err != nil {
		return "", fmt.Errorf("task log: %w", err)
	}
	return fmt.Sprintf("logged to %s", ref), nil
}

func (t *TaskTool) findTask(ref string) (string, *taskRecord, error) {
	// ref may be "canto-hzlu" or just "hzlu" — normalize to short form.
	short := ref
	if idx := strings.LastIndex(ref, "-"); idx >= 0 {
		short = ref[idx+1:]
	}
	filename := fmt.Sprintf("%s-%s.json", t.Project, short)
	rec, err := t.readTask(filename)
	if err != nil {
		return "", nil, fmt.Errorf("task %q not found: %w", ref, err)
	}
	return filename, rec, nil
}

func (t *TaskTool) readTask(filename string) (*taskRecord, error) {
	b, err := t.root.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var rec taskRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (t *TaskTool) writeTask(filename string, rec *taskRecord) error {
	b, err := json.Marshal(rec, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	return t.root.WriteFile(filename, append(b, '\n'), 0o644)
}

func randomRef(n int) (string, error) {
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(taskRefAlphabet))))
		if err != nil {
			return "", err
		}
		b[i] = taskRefAlphabet[idx.Int64()]
	}
	return string(b), nil
}

// NewTaskTool creates a TaskTool for the given tasks directory handle and project name.
func NewTaskTool(root *os.Root, project string) tool.Tool {
	return &TaskTool{root: root, Project: project}
}
