//go:build ignore

// memory-agent demonstrates Canto's multi-layer memory system:
//
//   - memory.Manager with scoped core + semantic memory
//   - MemoryPrompt request processor for manager-driven retrieval
//   - remember_memory / recall_memory tools for explicit durable memory I/O
//
// Usage: ANTHROPIC_API_KEY=... go run examples/memory-agent/main.go [message]
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/nijaru/canto/agent"
	cantoctx "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm/providers/anthropic"
	"github.com/nijaru/canto/memory"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/tools"
)

const (
	sessionID = "memory-agent-session-1"
	dbPath    = "./data/memory-agent.db"
	model     = "claude-3-5-sonnet-20241022"
)

func main() {
	ctx := context.Background()

	os.MkdirAll("./data", 0o755)

	// 1. Initialize persistent storage
	// Both the session event log and the CoreStore use the same SQLite file.
	// SQLiteStore and CoreStore each open their own connection with WAL mode,
	// so concurrent access is safe.
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("failed to open session store: %v", err)
	}
	defer store.Close()

	coreStore, err := memory.NewCoreStore(dbPath)
	if err != nil {
		log.Fatalf("failed to open memory store: %v", err)
	}
	defer coreStore.Close()

	namespace := memory.Namespace{Scope: memory.ScopeUser, ID: "memory-agent-user"}
	manager := memory.NewManager(coreStore, nil, nil, memory.WritePolicy{
		ConflictMode: memory.ConflictMerge,
	})
	defer manager.Close()

	// 2. Configure durable core memory blocks
	err = manager.UpsertBlock(ctx, namespace, "persona", `Agent Name: Archivist
Persona Context: A helpful research assistant with persistent long-term memory.
Directives: Always search memory before answering. Memorize important new facts.`, nil)
	if err != nil {
		log.Fatalf("failed to seed core memory: %v", err)
	}

	// 3. Register manager-based memory tools
	reg := tool.NewRegistry()
	reg.Register(&tools.RememberTool{
		Manager:   manager,
		Namespace: namespace,
		Role:      memory.RoleSemantic,
	})
	reg.Register(&tools.RecallTool{
		Manager:    manager,
		Namespaces: []memory.Namespace{namespace},
		Roles:      []memory.Role{memory.RoleCore, memory.RoleSemantic, memory.RoleEpisodic},
		Limit:      5,
	})

	// 4. Build the Context Pipeline
	// Pipeline ordering is critical:
	//  1. MemoryPrompt       — manager-driven retrieval (core + long-term memory)
	//  2. History            — model-visible conversation transcript
	//  3. Tools              — tool specs
	//  4. Capabilities       — MUST be last; adapts system/temp for reasoning models
	builder := cantoctx.NewBuilder(
		cantoctx.MemoryPrompt(manager, cantoctx.MemoryPromptOptions{
			Namespaces: []memory.Namespace{namespace},
			Roles:      []memory.Role{memory.RoleCore, memory.RoleSemantic, memory.RoleEpisodic},
			Limit:      5,
		}),
		cantoctx.History(),
		cantoctx.Tools(reg),
		cantoctx.Capabilities(),
	)

	const instructions = `You are a research assistant with persistent memory across sessions.
Before answering any question, use recall_memory to search what you know.
When the user shares an important fact, use remember_memory to store it.`

	// 5. Initialize the Agent
	provider := anthropic.New(os.Getenv("ANTHROPIC_API_KEY"))
	a := agent.New("memory-agent", instructions, model, provider, reg,
		agent.WithBuilder(builder),
		agent.WithMaxSteps(20),
	)

	runner := runtime.NewRunner(store, a)

	// 6. Get user input
	input := strings.Join(os.Args[1:], " ")
	if input == "" {
		fmt.Print(">>> ")
		sc := bufio.NewScanner(os.Stdin)
		sc.Scan()
		input = sc.Text()
	}
	if input == "" {
		fmt.Fprintln(os.Stderr, "no input")
		os.Exit(1)
	}

	// 7. Append input and run through the runner's canonical host API
	result, err := runner.Send(ctx, sessionID, input)
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	// 8. Output final response
	fmt.Println("\n" + result.Content)
}
