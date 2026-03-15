# Canto

**Canto** is a composable, minimal-abstraction Go framework for building LLM agents and agent swarms. Designed for optimal developer experience, production reliability, and deterministic orchestration.

## Design Principles

1. **Code over Configuration** — Orchestration is deterministic Go code, not brittle prompts.
2. **Append-only State** — Every session is a durable event log of ULID-indexed facts.
3. **Composable Layers** — Small, well-defined interfaces that compose cleanly.
4. **Minimal Abstraction** — Stay close to the metal; no hidden magic or complex hierarchies.

## Project Structure

| Package      | Layer | Purpose                                                     |
| ------------ | ----- | ----------------------------------------------------------- |
| `llm/`       | 1     | Provider-agnostic LLM interface, streaming, cost tracking   |
| `agent/`     | 2     | Core agentic loop (perceive → decide → act → observe)       |
| `session/`   | 3     | Durable append-only event log, JSONL and SQLite stores      |
| `context/`   | 3     | Context engineering pipeline, compaction, KV-cache helpers  |
| `tool/`      | 3     | Tool execution, registry, MCP client                        |
| `skill/`     | 3     | Progressive disclosure skill packages (SKILL.md standard)   |
| `runtime/`   | 3     | Session execution, lane queue, heartbeat, workspace config  |
| `memory/`    | 3     | Core memory (persona) + archival memory (HNSW vector store) |
| `x/graph/`   | ext   | DAG orchestration with conditional routing                  |
| `x/swarm/`   | ext   | Blackboard-based decentralized multi-agent swarm            |
| `x/eval/`    | ext   | Evaluation harness over trajectory store                    |
| `x/channel/` | ext   | HTTP channel adapter (REST + SSE streaming)                 |

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"os"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

func main() {
	ctx := context.Background()

	// 1. Setup tools
	registry := tool.NewRegistry()
	registry.Register(&tool.BashTool{})

	// 2. Setup provider
	p := openai.NewProvider(catwalk.Provider{
		ID:     "openai",
		APIKey: os.Getenv("OPENAI_API_KEY"),
	})

	// 3. Create agent
	a := agent.New("researcher", "You are a helpful research assistant.", "gpt-4o", p, registry)

	// 4. Setup persistence
	store, _ := session.NewJSONLStore("./data")
	runner := runtime.NewRunner(store, a)

	// 5. Run a session
	sessionID := "session-123"
	userMsg := llm.Message{Role: llm.RoleUser, Content: "Who are you?"}
	store.Save(ctx, session.NewEvent(sessionID, session.EventTypeMessageAdded, userMsg))

	if err := runner.Run(ctx, sessionID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

## Status

All four implementation phases complete. Currently in v0.0.1 polish.

- [x] Phase 1: Core Loop
- [x] Phase 2: Production Reliability
- [x] Phase 3: Runtime Features
- [x] Phase 4: Multi-Agent Swarms

## License

MIT
