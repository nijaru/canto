# Canto

[![Go Reference](https://pkg.go.dev/badge/github.com/nijaru/canto.svg)](https://pkg.go.dev/badge/github.com/nijaru/canto)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Canto is a Go framework for building durable agent backends. It provides primitives for append-only session history, phased context construction, and multi-agent coordination.

> [!WARNING]
> **Status: Pre-alpha.** Canto is under active development. APIs are unstable and subject to breaking changes.

## Features

- **Append-only log**: Every interaction, tool call, and compaction is recorded as a permanent fact.
- **Durable Sessions**: JSONL or SQLite (FTS5) backends for session persistence and search.
- **Phased Context**: Separate request building (preview) from state mutation (commit).
- **Subagent Primitives**: Spawn, monitor, and export child agent runs with linked history.
- **MCP Support**: Integration with the Model Context Protocol for tool discovery.
- **Context Governance**: Automated offloading and summarization via the `governor` package.

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
	p := anthropic.NewProvider(...) // requires API key

	// 1. Define an agent
	a := agent.New("assistant", "You are a helpful gopher.", "claude-3-5-sonnet", p, nil)

	// 2. Start a session
	sess := session.New("user-session-id")

	// 3. Execute a turn
	res, _ := a.Turn(ctx, sess)
	fmt.Println(res.Content)
}
```

## Packages

| Package | Description | Status |
| :--- | :--- | :--- |
| `llm` | Provider normalization and token tracking | Semi-stable |
| `session` | Append-only event log and storage | Semi-stable |
| `agent` | Core turn-based loops | Active |
| `runtime` | Coordinator and runner execution | Active |
| `governor` | Context offloading and guards | New |
| `safety` | Execution modes and tool gating | New |
| `artifact` | Blob storage and registry | Semi-stable |
| `x/` | Swarm, Graph, and evaluation patterns | Experimental |

## Examples

- [Quickstart](examples/quickstart/main.go): Basic durable agent loop.
- [Subagents](examples/subagents/main.go): Parallel child runs and run-tree export.
- [Long Horizon](examples/long-horizon/main.go): Context offloading using the `governor`.

## License

[MIT](LICENSE)
