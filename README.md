# Canto

**Canto** is a composable, minimal-abstraction Go framework for building LLM agents and agent swarms. It prioritizes optimal developer experience, production reliability, and deterministic orchestration.

[![Go Reference](https://pkg.go.dev/badge/github.com/nijaru/canto.svg)](https://pkg.go.dev/github.com/nijaru/canto)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Design Principles

- **Code over Configuration** — Orchestration is deterministic Go code, not brittle prompts.
- **Append-only State** — Every session is a durable event log of ULID-indexed facts.
- **Composable Layers** — Small, well-defined interfaces that compose cleanly.
- **Zero-Dependency Core** — Minimal abstractions that stay close to the metal.

## System Architecture

Canto is organized into functional layers to ensure a clean dependency graph.

### Layer 1: Foundation
- `llm/` — Provider-agnostic interface, streaming normalization, and cost tracking.
- `session/` — Durable event log with JSONL and SQLite backends.
- `hook/` — Lifecycle hooks for subprocess protocols and enforcement.

### Layer 2: Agent Core
- `agent/` — Core agentic loop (perceive → decide → act → observe).
- `context/` — Context engineering pipeline, compaction, and summarization.
- `tool/` — Tool registry, MCP client, and sandboxed execution.
- `memory/` — Core memory (persona) and archival memory (pure Go HNSW).
- `skill/` — Progressive disclosure skill packages (SKILL.md standard).

### Layer 3: Runtime
- `runtime/` — Session execution, lane serialization, and workspace loading.
- `heartbeat/` — Scheduled and autonomous execution via `robfig/cron`.

### Extensions (`x/`)
- `x/graph/` — DAG orchestration with conditional routing.
- `x/swarm/` — Blackboard-based decentralized multi-agent swarms.
- `x/eval/` — Evaluation harness for trajectory scoring.
- `x/channel/` — HTTP adapters with REST and SSE streaming support.

## Quick Start

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

func main() {
	ctx := context.Background()

	// 1. Setup tools and provider
	registry := tool.NewRegistry()
	registry.Register(&tool.BashTool{})

	provider := openai.New(os.Getenv("OPENAI_API_KEY"))

	// 2. Create agent with instructions
	a := agent.New(
		"researcher",
		"You are a helpful research assistant.",
		"gpt-4o",
		provider,
		registry,
	)

	// 3. Setup persistence and runner
	store, _ := session.NewJSONLStore("./data")
	runner := runtime.NewRunner(store, a)

	// 4. Run a session
	sessionID := "session-123"
	if err := runner.Run(ctx, sessionID, "Who are you?"); err != nil {
		log.Fatal(err)
	}
}
```

## Features

- **Multi-Provider Support** — Native support for OpenAI, Anthropic, Gemini, Ollama, and OpenRouter.
- **Context Management** — Automated offloading and LLM-based summarization to prevent context rot.
- **Production Reliability** — Provider fallback, key rotation, and budget guards out of the box.
- **Agentic Skills** — Load and manage agent capabilities using the standard `SKILL.md` format.
- **Durable Memory** — Integrated persona management and vector-backed archival memory.

## Status

Canto is currently in **v0.0.1** (Phase 5: Feature Complete). The core interfaces are stable, and we are currently hardening production features.

## License

MIT
