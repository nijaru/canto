# Canto

> [!WARNING]
> **Status: Pre-release.** Core interfaces are currently unstable and subject to change.

Canto is a layered Go framework for building durable LLM agents and multi-agent systems.

The framework organizes agentic behavior into discrete, swappable layers. At its core, Canto uses an append-only event log to track session history, providing a deterministic foundation for state recovery, observability, and regression testing. It is designed for developers building production agents that require auditability and reliability beyond simple prompt loops.

[![Go Reference](https://pkg.go.dev/badge/github.com/nijaru/canto.svg)](https://pkg.go.dev/github.com/nijaru/canto)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Features

- **Durable Sessions**: Append-only event log (JSONL/SQLite) for state recovery and auditability.
- **Layered Decoupling**: Separate layers for LLM providers, agent loops, context management, and tools.
- **Advanced Orchestration**: Support for graph routing, multi-agent swarms, and parallel tool dispatch.
- **Context Pipeline**: Middleware-style request builder with budget guards and auto-compaction.
- **Observability**: Integrated OpenTelemetry tracing and a transcript evaluation suite (`x/eval`).
- **Memory**: Support for HNSW vector stores and SQLite-backed long-term memory.
- **Resilience**: Provider-level key rotation, fallback chains, and budget caps.

## Installation

```bash
go get github.com/nijaru/canto
```

## Architecture

Canto's architecture depends downward only. Extensions depend on the runtime, which depends on the agent loop and LLM layer.

```
+-------------------------------------------------------------+
|  Extensions  (graph, swarm, eval, obs, tools)               |
+-------------------------------------------------------------+
|  Runtime     (session, context, tool, skill, memory)        |
+-------------------------------------------------------------+
|  Agent Loop  (perceive → decide → act → observe)            |
+-------------------------------------------------------------+
|  LLM Layer   (provider, streaming, cost, tokens)            |
+-------------------------------------------------------------+
```

### Layered Breakdown

- **LLM Layer**: Normalizes interactions across providers (OpenAI, Anthropic, Gemini, etc.) and handles cost/token tracking.
- **Agent Loop**: Orchestrates the atomic turn-based execution cycle.
- **Runtime**: Manages the persistent session log, request construction (context), and long-term memory.
- **Extensions (x/)**: High-level patterns like DAG orchestration (Graph), Blackboard coordination (Swarm), and Judge-based evaluation.

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
	"github.com/nijaru/canto/session"
)

func main() {
	ctx := context.Background()
	provider := openai.NewProvider(catwalk.Provider{
		ID:     "openai",
		APIKey: os.Getenv("OPENAI_API_KEY"),
	})
	
	// 1. Initialize an agent
	a := agent.New("assistant", "You're a helpful assistant.", "gpt-4o", provider, nil)
	
	// 2. Start a durable session
	sess := session.New("user-123")
	msg := llm.Message{Role: llm.RoleUser, Content: "How does Canto handle state?"}
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), msg)); err != nil {
		panic(err)
	}

	// 3. Run a turn
	result, err := a.Turn(ctx, sess)
	if err != nil {
		panic(err)
	}

	// 4. View results
	fmt.Println(result.Content)
}
```

`sess.Messages()` returns the raw append-only transcript. Use `sess.EffectiveMessages()` when you need the model-visible history after compaction.

The executable quickstart lives at [examples/quickstart/main.go](examples/quickstart/main.go) and is compiled by `go test ./...`.

## History Semantics

- `sess.Messages()` is transcript truth: the raw append-only messages exactly as they were emitted.
- `sess.EffectiveMessages()` is prompt truth: the model-visible history after the latest durable compaction snapshot plus any later messages.
- `sess.EffectiveEntries()` returns prompt truth together with originating message-event IDs when known, which is useful for compaction and replay tooling.
- Forks preserve lineage with fresh event IDs plus `fork_origin` metadata, so branches are durable without losing ancestry.

## License

[MIT](LICENSE)
