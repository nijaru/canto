# Canto

**Canto** is a composable, minimal-abstraction Go framework for building LLM agents and agent swarms. It is designed for optimal developer experience, production reliability, and deterministic orchestration.

## Design Principles

1.  **Code over Configuration** — Orchestration is deterministic Go code, not brittle prompts.
2.  **Append-only State** — Every session is a durable event log of ULID-indexed facts.
3.  **Composable Layers** — Small, well-defined interfaces that compose cleanly.
4.  **Minimal Abstraction** — We stay close to the metal; no hidden magic or complex object hierarchies.

## Project Structure

- `llm/`: Layer 1 — Provider-agnostic LLM interface (OpenAI, Anthropic).
- `agent/`: Layer 2 — Core agentic loop (perceive → decide → act → observe).
- `session/`: Layer 3 — Durable append-only event logs and persistence.
- `tool/`: Layer 3 — Executable tool registry and MCP integration.
- `runtime/`: Layer 3 — Orchestration and execution runners.

## Quick Start

```go
package main

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"charm.land/catwalk/pkg/catwalk"
)

func main() {
	ctx := context.Background()

	// 1. Setup Tools
	registry := tool.NewRegistry()
	registry.Register(&tool.BashTool{})

	// 2. Setup Provider (using catwalk metadata)
	p := openai.NewProvider(catwalk.Provider{
		ID: "openai",
		APIKey: "$OPENAI_API_KEY",
	})

	// 3. Create Agent
	a := agent.New("researcher", "You are a helpful research assistant.", "gpt-4o", p, registry)

	// 4. Setup Persistence
	store, _ := session.NewJSONLStore("./data")
	runner := runtime.NewRunner(store, a)

	// 5. Run a Session
	sessionID := "session-123"
	userMsg := llm.Message{Role: llm.RoleUser, Content: "Who are you?"}
	store.Save(ctx, session.NewEvent(sessionID, session.EventTypeMessageAdded, userMsg))

	if err := runner.Run(ctx, sessionID); err != nil {
		panic(err)
	}
}
```

## Status

Canto is currently in **Phase 2: Production Reliability**. 

- [x] Phase 1: Core Loop (Complete)
- [ ] Phase 2: Production Reliability (Active)
- [ ] Phase 3: Runtime Features (Pending)
- [ ] Phase 4: Multi-Agent Swarms (Pending)

## License

MIT
