# Canto

[![Go Reference](https://pkg.go.dev/badge/github.com/nijaru/canto.svg)](https://pkg.go.dev/badge/github.com/nijaru/canto)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Canto is a Go framework for durable agents. It provides primitives for append-only sessions, context construction, tool execution, workspace-safe tools, service/API tools, and multi-agent coordination.

> [!WARNING]
> **Status: Pre-alpha.** Canto is under active development. APIs are unstable and subject to breaking changes.

## Features

- **Append-only log**: Every interaction, tool call, and compaction is recorded as a permanent fact.
- **Durable Sessions**: JSONL or SQLite (FTS5) stores for session persistence and search.
- **Phased Context**: Separate request building (preview) from state mutation (commit).
- **Coding Tools**: Stable workspace, edit, shell, and code-execution tools in `coding/`.
- **Service Tools**: Typed service/API helpers with schemas, approval requirements, metadata, and retries.
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
	"log"

	"github.com/nijaru/canto"
	"github.com/nijaru/canto/llm"
)

func main() {
	app, err := canto.NewAgent("assistant").
		Instructions("You are a concise assistant.").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "Hello from Canto."})).
		Ephemeral().
		Build()
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	res, err := app.Send(context.Background(), "session-1", "Say hello.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Content)
}
```

Use `SessionStore(store)` instead of `Ephemeral()` for durable applications.

## Packages

| Package | Description | Status |
| :--- | :--- | :--- |
| `llm` | Provider normalization and token tracking | Semi-stable |
| `session` | Append-only event log and storage | Semi-stable |
| `agent` | Core turn-based loops | Active |
| `runtime` | Coordinator and runner execution | Active |
| `coding` | Stable workspace, edit, shell, and code execution tools | Active |
| `service` | Typed service/API tool helpers | Active |
| `governor` | Context offloading and guards | New |
| `safety` | Execution modes and tool gating | New |
| `artifact` | Blob storage and registry | Semi-stable |
| `x/` | Swarm, Graph, and evaluation patterns | Experimental |

## Provider Support

Canto's M1 provider contract is documented in
[docs/providers.md](docs/providers.md). In short: `llm.NewFauxProvider`,
OpenAI, and Anthropic are the supported alpha paths; OpenRouter, Gemini, and
Ollama are provisional OpenAI-compatible adapters; custom OpenAI-compatible
endpoints are bring-your-own validation.

## Defaults

Canto's default prompt and tool contract is documented in
[docs/defaults.md](docs/defaults.md). Canto provides the prompt pipeline,
session history, lazy tool loading, and optional feature blocks; hosts provide
agent instructions and choose which tools are registered.

## Examples

- [Hello](examples/hello/main.go): Minimal no-credential root-builder agent.
- [Code Agent](examples/codeagent/main.go): No-credential Claude Code/Codex/Cursor-class reference using durable sessions, workspace tools, approvals, hooks, service tools, and resume.
- [Service Agent](examples/service-agent/main.go): Typed service/API tool example.
- [Quickstart](examples/quickstart/main.go): Lower-level durable agent loop.
- [Subagents](examples/subagents/main.go): Parallel child runs and run-tree export.
- [Long Horizon](examples/long-horizon/main.go): Context offloading using the `governor`.

## License

[MIT](LICENSE)
