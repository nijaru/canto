# Canto 🎤

[![Go Reference](https://pkg.go.dev/badge/github.com/nijaru/canto.svg)](https://pkg.go.dev/badge/github.com/nijaru/canto)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Canto** is a Go framework for building durable agent backends. It provides the low-level primitives for append-only session history, phased context construction, and multi-agent coordination.

> [!WARNING]
> **Pre-alpha:** APIs are unstable and subject to breaking changes.

## Why Canto?

Building "batteries-included" agent loops is easy; building durable, observable backends is hard. Canto focuses on the state and runtime pieces that are difficult to rebuild correctly:

- **Append-only log**: Every interaction, tool call, and compaction is a permanent fact.
- **Transcript vs. Prompt Truth**: `EffectiveMessages()` gives you the current prompt state after lossy or reversible compaction.
- **Phased Context**: Separate request building (preview) from durable state mutation (commit).
- **Subagent Primitives**: Native support for spawning, monitoring, and exporting child agent runs.
- **MCP Native**: First-class support for tool discovery via the Model Context Protocol.

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
	p := anthropic.NewProvider(...)

	// 1. Define an agent
	a := agent.New("assistant", "You are a helpful gopher.", "claude-3-5-sonnet", p, nil)

	// 2. Initialize a durable session
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
| `x/` | Swarm, Graph, and evaluation patterns | Experimental |

## Examples

- [Quickstart](examples/quickstart/main.go): The simplest durable agent loop.
- [Subagents](examples/subagents/main.go): Parent agents spawning child workers.
- [Long Horizon](examples/long-horizon/main.go): Using `governor` for context offloading.

## License

[MIT](LICENSE)
