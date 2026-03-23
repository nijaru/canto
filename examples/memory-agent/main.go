//go:build ignore

// memory-agent demonstrates Canto's multi-layer memory system:
//
//   - CoreStore persona: the agent's identity injected into every turn
//   - KnowledgeMemory processor: FTS5 RAG auto-injected from the last user message
//   - memorize_knowledge / recall_knowledge tools: explicit FTS5 read/write
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
	"github.com/nijaru/canto/llm"
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

	// 2. Configure the Persona
	// Overwrites on each run; remove this block to persist across runs
	// once you have a management interface.
	err = coreStore.SetPersona(ctx, sessionID, &memory.Persona{
		Name:        "Archivist",
		Description: "A helpful research assistant with persistent long-term memory.",
		Directives:  "Always search memory before answering. Memorize important new facts.",
	})
	if err != nil {
		log.Fatalf("failed to set persona: %v", err)
	}

	// 3. Register FTS5 semantic memory tools
	reg := tool.NewRegistry()
	reg.Register(&tools.MemorizeKnowledgeTool{Store: coreStore, SessionID: sessionID})
	reg.Register(&tools.RecallKnowledgeTool{Store: coreStore, Limit: 5})

	// 4. Build the Context Pipeline
	// Pipeline ordering is critical:
	//  1. CoreMemoryProcessor — <core_memory> persona block
	//  2. KnowledgeMemory     — <knowledge_memory> FTS5 RAG (query from last user msg)
	//  3. History             — model-visible conversation transcript
	//  4. Tools               — tool specs
	//  5. Capabilities        — MUST be last; adapts system/temp for reasoning models
	builder := cantoctx.NewBuilder(
		cantoctx.CoreMemoryProcessor(coreStore),
		cantoctx.KnowledgeMemory(coreStore, "", 5),
		cantoctx.History(),
		cantoctx.Tools(reg),
		cantoctx.Capabilities(),
	)

	const instructions = `You are a research assistant with persistent memory across sessions.
Before answering any question, use recall_knowledge to search what you know.
When the user shares an important fact, use memorize_knowledge to store it.`

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

	// 7. Append message and run
	if err := store.Save(ctx, session.NewEvent(sessionID, session.MessageAdded,
		llm.Message{Role: llm.RoleUser, Content: input},
	)); err != nil {
		log.Fatalf("failed to save message: %v", err)
	}

	if _, err := runner.Run(ctx, sessionID); err != nil {
		log.Fatalf("run failed: %v", err)
	}

	// 8. Output final response
	sess, err := store.Load(ctx, sessionID)
	if err != nil {
		log.Fatalf("failed to load session: %v", err)
	}

	msgs := sess.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == llm.RoleAssistant && len(m.Calls) == 0 {
			fmt.Println("\n" + m.Content)
			break
		}
	}
}
