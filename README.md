# Canto

> [!WARNING]
> **Status: Pre-release.** `v0.0.x` is not a compatibility guarantee. The intended first-alpha base is a semi-stable core for Ion and similar agent backends: `llm`, `agent`, `artifact`, `session`, `tool`, and `hook`, with `context`, `runtime`, `tool/mcp`, `memory`, and `skill` included but newer. `x/` packages and `examples/` remain experimental.

Canto is a Go framework for durable agent backends.

It is built around an append-only session log. That gives you a stable place to put context compaction, tool use, child runs, artifact refs, replay, and export without collapsing everything into one prompt loop.

It does not try to be a hosted platform, frontend SDK, or batteries-included product framework. The point is to give you durable state and runtime primitives so you can build your own agent behavior on top.

[![Go Reference](https://pkg.go.dev/badge/github.com/nijaru/canto.svg)](https://pkg.go.dev/github.com/nijaru/canto)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Start Here

If you are new to the framework, use this order:

1. Read the executable quickstart in [examples/quickstart/main.go](examples/quickstart/main.go).
2. Learn the session model in [`session`](session/): raw transcript vs effective prompt history.
3. Learn the context split in [`context`](context/): preview-safe request shaping vs commit-time mutation.
4. Learn orchestration in [`runtime`](runtime/) and [examples/subagents/main.go](examples/subagents/main.go): attached vs detached child runs, default local coordination vs custom coordinator-backed execution.
5. Learn tool integration in [`tool`](tool/) and [`tool/mcp`](tool/mcp/): local tools plus MCP-backed tools and servers.

## What Canto Is

Canto fits best when you want to own agent policy and product UX, but you do not want to rebuild the backend runtime from scratch.

- durable session state
- context construction and compaction hooks
- tool registry and MCP transport integration
- child-session lifecycle and nested run export
- local and distributed execution coordination

## What Canto Is Not

- not a hosted agent platform
- not a frontend chat SDK
- not a batteries-included app framework with every workflow already modeled

## What Canto Decides

Canto provides the backend pieces that are hard to rebuild correctly:

- durable session history
- context construction and compaction hooks
- tool registry and MCP transport integration
- child-session lifecycle and nested run export
- local and distributed execution coordination

Your application still decides agent-level policy:

- when to spawn child runs
- how much context to hand off
- which model or tool to choose
- how to merge results back into the user-facing session
- what approval, safety, or UX rules to enforce

## Core Ideas

- **Durable sessions**: Append-only event log in JSONL or SQLite.
- **Prompt truth vs transcript truth**: Raw messages stay immutable; effective history stays replayable after compaction.
- **Two-phase context building**: Preview-safe request shaping is separate from commit-time mutation.
- **Framework-level orchestration**: Child sessions, lifecycle events, artifact refs, and nested run export are built in.
- **MCP transport integration**: MCP uses the official Go SDK at the transport boundary.

## Installation

```bash
go get github.com/nijaru/canto
```

## Architecture

Canto's packages depend downward only. Extensions depend on the runtime, which depends on the agent loop and LLM layer.

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
- **Runtime**: Manages the persistent session log, request construction (context), and execution helpers.
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

## Common Paths

### Single Durable Agent

Use this path when you want one agent with durable history:

- create a `session.Session`
- append user messages to the session
- run an `agent.Agent`
- read `Messages()` for transcript truth or `EffectiveMessages()` for model-visible history

The quickest reference is [examples/quickstart/main.go](examples/quickstart/main.go).

### Context Preview vs Commit

Use [`context.Builder.BuildPreview`](context/) when you need to inspect or shape the next request without mutating durable state. Use `Build` or `BuildCommit` when processors may compact, offload, or otherwise record new durable facts before the request is sent.

This split exists so the framework can support dry-run inspection, tracing, and request planning without hiding stateful behavior behind one middleware hook.

If your application wants to manually trigger Canto's built-in durable
compaction path, use `context.CompactSession(...)`. It runs the standard
offload-then-summarize pipeline and returns whether the session actually
appended new compaction snapshots.

### Child Runs

The parent/child subagent pattern in Canto is a framework capability, not a prescribed agent policy:

- the framework provides isolated child sessions, durable lifecycle events, and nested trajectory export
- the application decides when to spawn children, how to hand off context, and how to merge results

See [examples/subagents/main.go](examples/subagents/main.go) for a bounded orchestrator-worker example that keeps those concerns separate.

By default, child runs are attached to the spawn context and are canceled with it. Set `runtime.ChildSpec.Detached` only for background work that should outlive the caller.

If your host wants one obvious way to share runtime settings across foreground
and background execution, use `runtime.ExecutionConfig`,
`runtime.NewRunnerWithConfig(...)`, `runtime.NewChildRunnerWithConfig(...)`, or
`runner.ChildRunner()` to carry the same coordinator, hook, and timeout posture
through both paths.

### MCP Tools

Use [`tool.Registry`](tool/) for local tools. Use [`tool/mcp`](tool/mcp/) when you need to discover or serve tools over MCP.

The framework uses the official Go MCP SDK for transport and session handling, while Canto still owns tool validation and registry adaptation.

## History Semantics

- `sess.Messages()` is transcript truth: the raw append-only messages exactly as they were emitted.
- `sess.EffectiveMessages()` is prompt truth: the model-visible history after the latest durable compaction snapshot plus any later messages.
- `sess.EffectiveEntries()` returns prompt truth together with originating message-event IDs when known, which is useful for compaction and replay tooling.
- Forks preserve lineage with fresh event IDs plus `fork_origin` metadata, so branches are durable without losing ancestry.
- SQLite and JSONL stores now also expose first-class tree queries through `session.SessionTreeStore`, so parent/children/lineage navigation does not require scanning copied fork events.
- Use `session.StoreArtifact(...)` when you want the framework to persist an
  artifact body and emit the corresponding durable `artifact_recorded` event in
  one step. Use `session.RecordArtifact(...)` for existing external descriptors
  or refs.

## License

[MIT](LICENSE)
