// codeagent is a no-credential reference for building Claude Code/Codex/Cursor-
// class agents on Canto primitives.
package main

import (
	"context"
	"fmt"
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
	ctx := context.Background()
	rootDir, err := os.MkdirTemp("", "canto-codeagent-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(rootDir)

	if err := seedWorkspace(rootDir); err != nil {
		log.Fatal(err)
	}

	root, err := workspace.Open(rootDir)
	if err != nil {
		log.Fatal(err)
	}
	defer root.Close()

	storePath := filepath.Join(rootDir, ".canto", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		log.Fatal(err)
	}
	store, err := session.NewSQLiteStore(storePath)
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
	}

	hooks := hook.NewRunner()
	hooks.Register(hook.NewFunc(
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

	app, err := canto.NewAgent("codeagent").
		Instructions("Use workspace, shell, and service tools to complete coding tasks. Verify changes before answering.").
		Model("faux").
		Provider(scriptedProvider()).
		SessionStore(store).
		Tools(referenceTools(root, rootDir, executor, webSearch)...).
		Approvals(approval.NewManager(safety.NewPolicy(safety.ModeAuto))).
		Hooks(hooks).
		AgentOptions(agent.WithMaxSteps(8)).
		RuntimeOptions(runtime.WithExecutionTimeout(15 * time.Second)).
		Build()
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	events, err := app.Runner.Watch(ctx, sessionID)
	if err != nil {
		log.Fatal(err)
	}
	defer events.Close()

	seen := make(chan []session.EventType, 1)
	go collectEvents(events, seen)

	result, err := app.Send(
		ctx,
		sessionID,
		"Inspect the project, update README.md, consult web search, run tests, and summarize.",
	)
	if err != nil {
		log.Fatal(err)
	}

	resume, err := app.Send(ctx, sessionID, "Resume the session and report the current state.")
	if err != nil {
		log.Fatal(err)
	}

	events.Close()
	eventTypes := <-seen

	readme, err := root.ReadFile("README.md")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Content)
	fmt.Println(resume.Content)
	fmt.Printf("README.md: %s\n", strings.TrimSpace(string(readme)))
	fmt.Printf("Events: %s\n", eventSummary(eventTypes))
	fmt.Printf("Audit:\n%s", auditLog.String())
}

func referenceTools(
	root workspace.WorkspaceFS,
	dir string,
	executor *coding.Executor,
	webSearch cantotool.Tool,
) []cantotool.Tool {
	bash := &coding.BashTool{Executor: executor, Dir: dir}
	code := coding.NewCodeExecutionTool("python")
	code.Executor = executor

	out := coding.WorkspaceTools(root)
	out = append(out, bash, code, webSearch)
	return out
}

func scriptedProvider() llm.Provider {
	return llm.NewFauxProvider(
		"faux",
		llm.FauxStep{
			Calls: []llm.Call{
				toolCall("list", "list_dir", `{"path":"."}`),
				toolCall("glob", "glob", `{"pattern":"*.go"}`),
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
				toolCall("bash", "bash", `{"command":"test -f README.md"}`),
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
