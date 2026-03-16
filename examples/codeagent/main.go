//go:build ignore

// codeagent demonstrates a Claude Code-style coding assistant: persistent
// sessions, bash execution, skill progressive disclosure, and hook-based
// lifecycle logging.
//
// Run: OPENAI_API_KEY=... go run examples/codeagent/main.go <message>
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/tools"
)

const sessionID = "codeagent-main"

func main() {
	ctx := context.Background()

	// Tool registry: bash for file ops, shell commands, test runners.
	reg := tool.NewRegistry()
	reg.Register(&tools.BashTool{})
	reg.Register(tools.NewCodeExecutionTool("python"))

	provider := openai.New(os.Getenv("OPENAI_API_KEY"))

	instructions := `You are a coding assistant with access to bash and a Python REPL.
You help users write, debug, and run code. Use bash to read files, run tests,
and explore the codebase. Use execute_code for quick Python experiments.
Always verify your changes work before reporting success.`

	a := agent.New("codeagent", instructions, "gpt-4o", provider, reg)
	a.MaxSteps = 30

	// Hooks: log tool calls and session lifecycle to stderr.
	a.Hooks.Register(hook.CommandHook{
		Event: hook.EventPreToolUse,
		Args:  []string{"bash", "-c", `echo "[tool] $CANTO_TOOL_NAME: $CANTO_TOOL_ARGS" >&2`},
	})

	store, err := session.NewJSONLStore("./data/codeagent")
	if err != nil {
		log.Fatal(err)
	}

	runner := runtime.NewRunner(store, a)

	// Read user input: from args or interactive stdin.
	var input string
	if len(os.Args) > 1 {
		input = strings.Join(os.Args[1:], " ")
	} else {
		fmt.Print(">>> ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		input = scanner.Text()
	}

	if input == "" {
		fmt.Fprintln(os.Stderr, "no input")
		os.Exit(1)
	}

	// Append the user message and run.
	store.Save(ctx, session.NewEvent(sessionID, session.EventTypeMessageAdded,
		llm.Message{Role: llm.RoleUser, Content: input},
	))

	if err := runner.Run(ctx, sessionID); err != nil {
		log.Fatalf("run failed: %v", err)
	}

	// Print the assistant's final response.
	sess, _ := store.Load(ctx, sessionID)
	msgs := sess.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == llm.RoleAssistant && len(m.Calls) == 0 {
			fmt.Println(m.Content)
			break
		}
	}
}
