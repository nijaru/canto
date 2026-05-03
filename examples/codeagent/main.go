// codeagent is a no-credential reference for building Claude Code/Codex/Cursor-
// class agents on Canto primitives.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nijaru/canto"
	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/coding"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/service"
	"github.com/nijaru/canto/session"
	cantotool "github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/workspace"
)

const sessionID = "codeagent-reference"

type searchArgs struct {
	Query string `json:"query" jsonschema:"search query"`
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, w io.Writer) error {
	rootDir, err := os.MkdirTemp("", "canto-codeagent-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(rootDir)

	if err := seedWorkspace(rootDir); err != nil {
		return err
	}

	root, err := workspace.Open(rootDir)
	if err != nil {
		return err
	}
	defer root.Close()

	storePath := filepath.Join(rootDir, ".canto", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(storePath)
	if err != nil {
		return err
	}

	auditLog := &strings.Builder{}
	executor := coding.NewExecutor(5*time.Second, 64*1024)
	executor.SecretInjector = safety.StaticSecretInjector{
		"EXAMPLE_TOKEN": "redacted",
	}

	webSearch, err := service.New(service.Config[searchArgs, searchResult]{
		Name:        "web_search",
		Description: "Search the web for reference information.",
		Metadata: cantotool.Metadata{
			Category:    "service",
			ReadOnly:    true,
			Concurrency: cantotool.Parallel,
		},
		Execute: func(_ context.Context, args searchArgs) (searchResult, error) {
			return searchResult{
				Title:   "Canto coding agents",
				URL:     "https://example.com/canto-codeagent",
				Snippet: "Canto composes durable sessions, workspace tools, approvals, hooks, and service tools.",
			}, nil
		},
		Approval: service.ReadOnly("web.search", func(args searchArgs) string {
			return args.Query
		}),
	})
	if err != nil {
		return err
	}

	hooks := hook.NewRunner()
	hooks.Register(hook.FromFunc(
		"audit-tool-use",
		[]hook.Event{hook.EventPreToolUse, hook.EventPostToolUse, hook.EventPostToolUseFailure},
		func(_ context.Context, payload *hook.Payload) *hook.Result {
			toolName, _ := payload.Data["tool"].(string)
			if toolName != "" {
				fmt.Fprintf(auditLog, "%s %s\n", payload.Event, toolName)
			}
			return &hook.Result{Action: hook.ActionProceed}
		},
	))

	h, err := canto.NewHarness("codeagent").
		Instructions("Use workspace, shell, and service tools to complete coding tasks. Verify changes before answering.").
		Model("faux").
		Provider(scriptedProvider()).
		SessionStore(store).
		Tools(referenceTools(root, rootDir, executor, webSearch)...).
		Approvals(approval.NewGate(safety.NewConfig(safety.ModeAuto))).
		Hooks(hooks).
		AgentOptions(agent.WithMaxSteps(8)).
		RuntimeOptions(runtime.WithExecutionTimeout(15 * time.Second)).
		Build()
	if err != nil {
		return err
	}
	defer h.Close()

	sessionHandle := h.Session(sessionID)
	events, err := sessionHandle.Events(ctx)
	if err != nil {
		return err
	}
	defer events.Close()

	seen := make(chan []session.EventType, 1)
	go collectEvents(events, seen)

	result, err := sessionHandle.Prompt(
		ctx,
		"Inspect the project, update README.md, consult web search, run tests, and summarize.",
	)
	if err != nil {
		return err
	}

	resume, err := sessionHandle.Prompt(ctx, "Resume the session and report the current state.")
	if err != nil {
		return err
	}

	events.Close()
	eventTypes := <-seen

	readme, err := root.ReadFile("README.md")
	if err != nil {
		return err
	}

	fmt.Fprintln(w, result.Content)
	fmt.Fprintln(w, resume.Content)
	fmt.Fprintf(w, "README.md: %s\n", strings.TrimSpace(string(readme)))
	fmt.Fprintf(w, "Events: %s\n", eventSummary(eventTypes))
	_, err = fmt.Fprintf(w, "Audit:\n%s", auditLog.String())
	return err
}

func referenceTools(
	root workspace.WorkspaceFS,
	dir string,
	executor *coding.Executor,
	webSearch cantotool.Tool,
) []cantotool.Tool {
	shell := &coding.ShellTool{Executor: executor, Dir: dir}
	code := coding.NewCodeExecutionTool("python")
	code.Executor = executor

	return []cantotool.Tool{
		coding.NewReadFileTool(root),
		coding.NewWriteFileTool(root),
		coding.NewListDirTool(root),
		coding.NewEditTool(root),
		coding.NewMultiEditTool(root),
		shell,
		code,
		webSearch,
	}
}

func scriptedProvider() llm.Provider {
	return llm.NewFauxProvider(
		"faux",
		llm.FauxStep{
			Calls: []llm.Call{
				toolCall("list", "list_dir", `{"path":"."}`),
				toolCall("read", "read_file", `{"path":"README.md"}`),
				toolCall("search", "web_search", `{"query":"canto coding agent architecture"}`),
			},
		},
		llm.FauxStep{
			Calls: []llm.Call{
				toolCall(
					"edit",
					"edit",
					`{"path":"README.md","before":"status: draft","after":"status: verified"}`,
				),
				toolCall("shell", "shell", `{"command":"test -f README.md"}`),
				toolCall("code", "execute_code", `{"code":"print('unit smoke ok')"}`),
			},
		},
		llm.FauxStep{
			Content: "Updated README.md, checked service context, and verified the workspace smoke test.",
		},
		llm.FauxStep{
			Content: "Session resumed with durable history; README.md remains verified.",
		},
	)
}

func seedWorkspace(root string) error {
	if err := os.WriteFile(
		filepath.Join(root, "README.md"),
		[]byte("project: reference\nstatus: draft\n"),
		0o644,
	); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644)
}

func collectEvents(sub *session.Subscription, done chan<- []session.EventType) {
	var events []session.EventType
	for event := range sub.Events() {
		events = append(events, event.Type)
	}
	done <- events
}

func eventSummary(events []session.EventType) string {
	counts := make(map[session.EventType]int)
	for _, event := range events {
		counts[event]++
	}
	names := []session.EventType{
		session.MessageAdded,
		session.ToolStarted,
		session.ToolCompleted,
		session.ApprovalRequested,
		session.ApprovalResolved,
		session.TurnCompleted,
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		if counts[name] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", name, counts[name]))
		}
	}
	return strings.Join(parts, ", ")
}

func toolCall(id, name, args string) llm.Call {
	call := llm.Call{
		ID:   id,
		Type: "function",
	}
	call.Function.Name = name
	call.Function.Arguments = args
	return call
}
