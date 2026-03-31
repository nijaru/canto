# Canto

> [!WARNING]
> **Status: Pre-alpha.** Canto is under active development. APIs are unstable and subject to breaking changes.

Canto is a Go framework for durable agent backends. It provides the state and runtime primitives needed to build reliable, observable, and multi-turn agent systems.

It is built around an append-only session log, providing a stable foundation for context compaction, tool use, child runs, and artifact management without collapsing everything into a single prompt loop.

[![Go Reference](https://pkg.go.dev/badge/github.com/nijaru/canto.svg)](https://pkg.go.dev/badge/github.com/nijaru/canto)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Component Status

| Package | Status | Description |
| :--- | :--- | :--- |
| `llm` | **Stable-ish** | Provider normalization, cost, and token tracking. |
| `agent` | **Stable-ish** | Core atomic turn-based loop. |
| `session` | **Stable-ish** | Append-only event log (JSONL/SQLite). |
| `artifact` | **Stable-ish** | Durable blob storage and descriptor registry. |
| `tool` | **Stable-ish** | Tool registry and execution. |
| `safety` | **New** | Execution modes (Auto, Read, Edit) and tool gating. |
| `governor` | **New** | Context management (Offloading, Summarization, Guards). |
| `context` | **Active** | Request construction and preview-safe pipeline. |
| `runtime` | **Active** | Runner, child sessions, and coordination. |
| `tool/mcp` | **Experimental** | Official MCP SDK integration. |
| `memory` | **Experimental** | Long-term memory and vector search contract. |
| `skill` | **Experimental** | Progressive discovery and runtime authoring. |
| `x/` | **Experimental** | High-level patterns (Graph, Swarm, Eval). |

## Getting Started

### 1. Installation
```bash
go get github.com/nijaru/canto
```

### 2. Run the Quickstart
The best way to understand the core primitives is to run and read the executable quickstart:
```bash
go run examples/quickstart/main.go
```

### 3. Choose Your Entry Point
*   **Host Applications**: Use `runtime.Runner` for a full-featured integration with coordination, hooks, and streaming.
*   **Simple Agents**: Use `agent.Agent` and `session.Session` for direct control over turns and storage.
*   **Multi-Agent Systems**: Explore `runtime.ChildRunner` and the orchestrator examples in `examples/subagents`.

## Core Concepts

*   **Durable Session Log**: Every event (message, tool call, compaction) is appended to a permanent log.
*   **Transcript vs. Prompt Truth**: `sess.Messages()` is the raw history; `sess.EffectiveMessages()` is what the model sees after compaction.
*   **Phased Context Building**: `context.Builder` separates previewing a request from committing durable state changes (like compaction).
*   **Safety Modes**: Use `safety.Policy` to enforce `ModeRead`, `ModeEdit`, or `ModeAuto` across your agent's tools.
*   **Resource Governance**: `governor` manages token budgets, costs, and automatic context offloading.

## Why Canto?

Canto fits best when you want to own the agent's policy and product UX, but don't want to rebuild the hard backend pieces:
- Multi-node execution safety via distributed coordination.
- Replayable and exportable nested agent trajectories.
- Standardized handling of large tool outputs (artifacts).
- Native MCP support for tool discovery.

## Architecture

Canto's packages depend downward only. Extensions depend on the runtime, which depends on the agent loop and LLM layer.

```
+-------------------------------------------------------------+
|  Extensions  (graph, swarm, eval, obs, tools)               |
+-------------------------------------------------------------+
|  Governance  (governor, safety)                             |
+-------------------------------------------------------------+
|  Runtime     (session, context, tool, skill, memory)        |
+-------------------------------------------------------------+
|  Agent Loop  (perceive → decide → act → observe)            |
+-------------------------------------------------------------+
|  LLM Layer   (provider, streaming, cost, tokens)            |
+-------------------------------------------------------------+
```

## License

[MIT](LICENSE)
