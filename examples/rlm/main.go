//go:build ignore

// rlm demonstrates a Recursive Language Model agent: an agent that can
// write and execute code, observe results, and iteratively refine its
// solution within a single Turn.
//
// The REPL tool wraps CodeExecutionTool and passes its output back so the
// agent can react to runtime results — the same feedback loop as Claude Code.
//
// Run: OPENAI_API_KEY=... go run examples/rlm/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/tools"
)

func main() {
	ctx := context.Background()

	// Tools: Python REPL + bash for file ops.
	reg := tool.NewRegistry()
	reg.Register(tools.NewCodeExecutionTool("python"))
	reg.Register(&tools.BashTool{})

	provider := openai.New(os.Getenv("OPENAI_API_KEY"))

	instructions := `You are a coding assistant with a Python REPL.
Write code, execute it with execute_code, observe results, and iterate.
Fix errors by reading the output and adjusting your code.
When your solution is correct, explain the result clearly.`

	a := agent.New("rlm", instructions, "gpt-4o", provider, reg)
	// Allow more steps so the agent can iterate through multiple REPL calls.
	a.MaxSteps = 20

	store, err := session.NewJSONLStore("./data/rlm")
	if err != nil {
		log.Fatal(err)
	}
	runner := runtime.NewRunner(store, a)

	sessionID := "rlm-session-1"

	// Seed user message.
	store.Save(ctx, session.NewEvent(
		sessionID,
		session.EventTypeMessageAdded,
		llm.Message{
			Role:    llm.RoleUser,
			Content: "Write a Python function to find all prime numbers up to N using the Sieve of Eratosthenes. Test it for N=50.",
		},
	))

	if err := runner.Run(ctx, sessionID); err != nil {
		log.Fatalf("run failed: %v", err)
	}

	sess, _ := store.Load(ctx, sessionID)
	fmt.Println("=== Final response ===")
	for _, m := range sess.Messages() {
		if m.Role == llm.RoleAssistant && len(m.Calls) == 0 {
			fmt.Println(m.Content)
		}
	}
}
