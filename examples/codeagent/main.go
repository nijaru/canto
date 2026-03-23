//go:build ignore

// codeagent demonstrates a persistent CLI coding assistant.
// It features:
// - A durable JSONL session store so conversations persist across runs
// - Tool registry with Bash and Python execution
// - Go-native hooks for intercepting lifecycle events (like tool calls)
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

const sessionID = "codeagent-session-1"

func main() {
	ctx := context.Background()

	// 1. Setup tools: bash for file ops, Python for execution
	reg := tool.NewRegistry()
	reg.Register(&tools.BashTool{})
	reg.Register(tools.NewCodeExecutionTool("python"))

	provider := openai.New(os.Getenv("OPENAI_API_KEY"))

	instructions := `You are a coding assistant with access to bash and a Python REPL.
You help users write, debug, and run code. Use bash to read files, run tests,
and explore the codebase. Use execute_code for quick Python experiments.
Always verify your changes work before reporting success.`

	// 2. Initialize the agent
	a := agent.New("codeagent", instructions, "gpt-4o", provider, reg)
	a.MaxSteps = 30

	// 3. Register a native Go hook to log tool executions to stderr
	a.Hooks.Register(hook.NewFunc(
		"log-tool-use",
		[]hook.Event{hook.EventPreToolUse},
		func(ctx context.Context, p *hook.Payload) *hook.Result {
			toolName, _ := p.Data["tool_name"].(string)
			args, _ := p.Data["tool_args"].(map[string]any)
			fmt.Fprintf(os.Stderr, "🔧 [Tool] %s(%v)\n", toolName, args)
			return &hook.Result{Action: hook.ActionProceed}
		},
	))

	// 4. Initialize persistent storage
	store, err := session.NewJSONLStore("./data/codeagent")
	if err != nil {
		log.Fatalf("failed to create store: %v", err)
	}

	runner := runtime.NewRunner(store, a)

	// 5. Get input from user (args or prompt)
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
		fmt.Fprintln(os.Stderr, "no input provided")
		os.Exit(1)
	}

	// 6. Append user message to the durable session
	err = store.Save(ctx, session.NewEvent(sessionID, session.MessageAdded,
		llm.Message{Role: llm.RoleUser, Content: input},
	))
	if err != nil {
		log.Fatalf("failed to save message: %v", err)
	}

	// 7. Run the agent
	if _, err := runner.Run(ctx, sessionID); err != nil {
		log.Fatalf("run failed: %v", err)
	}

	// 8. Output the final assistant response
	sess, _ := store.Load(ctx, sessionID)
	msgs := sess.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		// Find the last assistant message that wasn't just a tool call
		if m.Role == llm.RoleAssistant && len(m.Calls) == 0 {
			fmt.Println("\n" + m.Content)
			break
		}
	}
}
