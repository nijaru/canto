# Canto 🎤

[![Go Reference](https://pkg.go.dev/badge/github.com/nijaru/canto.svg)](https://pkg.go.dev/badge/github.com/nijaru/canto)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Canto** is a Go framework for building durable agent backends. It provides the low-level primitives for append-only session history, phased context construction, and multi-agent coordination.

> [!WARNING]
> **Pre-alpha:** APIs are unstable and subject to breaking changes.

## Why Canto?

While many SDKs focus on the "prompt-to-output" wire, Canto focuses on the *durable state* and *runtime policy* required for reliable, production-grade agents.

### Canto vs. Others

| Feature | Model SDKs (OpenAI/Anthropic) | LangGraph / LangChain | Vercel AI SDK | **Canto** |
| :--- | :--- | :--- | :--- | :--- |
| **Focus** | Low-level API wire | Graph-based state machines | Frontend/Web integration | **Durable backend logs** |
| **History** | In-memory only | Checkpoint-based | UI-centric (React/TS) | **Append-only event log** |
| **Coordination** | Manual | Node-to-node edges | Functional | **Parent/Child sessions** |
| **Language** | Many | Python/JS (primary) | TypeScript | **Go (native)** |
| **Governance** | None | Limited | None | **Automated offloading** |

## Core Primitives

### 1. Durable Session Log (`session`)
Canto treats every interaction, tool call, and internal thought as a permanent fact. Sessions can be backed by **JSONL** (for simple local debugging) or **SQLite** (for production persistence with FTS5 search).
- **Transcript Truth**: `sess.Messages()` is the exact raw log of what happened.
- **Prompt Truth**: `sess.EffectiveMessages()` is the current context window after compaction.

### 2. Phased Context Building (`context`)
Canto separates request construction into two distinct phases to support advanced UI and tracing workflows:
- **Preview**: Generate the next request payload without changing any durable state.
- **Commit**: Run stateful mutators (like compaction or offloading) before finalizing the request.

### 3. Resource Governance (`governor`)
Stop agents from blowing their context window or budget. The governor monitors every turn and can:
- **Offload**: Move large tool outputs to external storage (S3/local) and replace them with small pointers.
- **Summarize**: Distill older conversation history into factual snapshots when token limits are approached.
- **Guard**: Enforce hard token or USD budget limits per session.

### 4. Native Orchestration (`runtime`)
Spawn workers that are real Canto agents, not just function calls.
- **Isolated State**: Every child worker has its own dedicated durable session.
- **Durable Lifecycle**: Parent sessions record exactly when children were spawned, blocked, or merged.
- **Trajectory Export**: Export the full tree of parent/child interactions for offline evaluation or debugging.

## Installation

```bash
go get github.com/nijaru/canto
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm/providers/anthropic"
	"github.com/nijaru/canto/session"
)

func main() {
	ctx := context.Background()
	p := anthropic.NewProvider(...) // Setup your provider

	// 1. Define an agent with system instructions
	a := agent.New("assistant", "You are a helpful gopher.", "claude-3-5-sonnet", p, nil)

	// 2. Initialize a durable session (SQLite or JSONL)
	sess := session.New("user-session-id")

	// 3. Execute a turn
	res, _ := a.Turn(ctx, sess)
	fmt.Println(res.Content)
}
```

## Package Status

| Package | Purpose | Stability |
| :--- | :--- | :--- |
| `llm` | Provider normalization & token tracking | Semi-stable |
| `session` | Append-only event log (JSONL/SQLite) | Semi-stable |
| `agent` | Atomic turn-based loops | Active |
| `runtime` | Coordinator & runner execution | Active |
| `governor` | Token guards & compaction logic | New |
| `safety` | Execution modes & tool gating | New |
| `artifact` | Large blob storage & registry | Semi-stable |
| `x/` | Swarm, Graph, and evaluation patterns | Experimental |

## Examples

- [Quickstart](examples/quickstart/main.go): The simplest durable agent loop.
- [Subagents](examples/subagents/main.go): Orchestrator-worker patterns with parallel child runs.
- [Long Horizon](examples/long-horizon/main.go): Automated context governance and offloading.
- [MCP Tools](tool/mcp/): Using the Model Context Protocol for tool discovery.

## License

[MIT](LICENSE)
