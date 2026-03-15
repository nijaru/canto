# Canto

Composable Go framework for building LLM agents and agent swarms.

[![Go Reference](https://pkg.go.dev/badge/github.com/nijaru/canto.svg)](https://pkg.go.dev/github.com/nijaru/canto)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Design

- Orchestration is deterministic Go code, not prompts
- Session state is an append-only event log — never mutated
- Five small interfaces that compose into larger behaviors

## Quick Start

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

func main() {
	ctx := context.Background()

	registry := tool.NewRegistry()
	registry.Register(&tool.BashTool{})

	provider := openai.New(os.Getenv("OPENAI_API_KEY"))

	a := agent.New(
		"researcher",
		"You are a helpful research assistant.",
		"gpt-4o",
		provider,
		registry,
	)

	store, _ := session.NewJSONLStore("./data")
	runner := runtime.NewRunner(store, a)

	if err := runner.Run(ctx, "session-123", "Who are you?"); err != nil {
		log.Fatal(err)
	}
}
```

## Architecture

```
+-------------------------------------------------------------+
|  Extensions  (graph, swarm, eval, channel, rl, obs...)      |
+-------------------------------------------------------------+
|  Runtime     (session, context, tool, skill, memory)        |
+-------------------------------------------------------------+
|  Agent Loop  (perceive → decide → act → observe)            |
+-------------------------------------------------------------+
|  LLM         (provider, resolver, streaming, cost)          |
+-------------------------------------------------------------+
```

**Layer 1 — LLM**

- `llm/` — Provider interface, streaming normalization, cost tracking
- `llm/providers/` — OpenAI, Anthropic, Gemini, Ollama, OpenRouter

**Layer 2 — Agent**

- `agent/` — Core loop: perceive → decide → act → observe; parallel tool dispatch; handoffs

**Layer 3 — Runtime**

- `session/` — Append-only event log, JSONL and SQLite backends, trajectory recording
- `context/` — Context engineering pipeline: token guards, compaction, KV-cache preservation
- `tool/` — Registry, sandboxed executor, MCP client/server
- `skill/` — Progressive disclosure skill packages (SKILL.md standard)
- `runtime/` — Session runner, per-session lane queue, heartbeat scheduler
- `memory/` — In-context working memory, SQLite long-term store, vector search

**Extensions (`x/`)**

- `x/graph/` — DAG orchestration with conditional routing
- `x/swarm/` — Blackboard-based decentralized multi-agent swarms
- `x/eval/` — Evaluation harness for trajectory scoring
- `x/channel/` — HTTP, CLI, and webhook adapters

## Core Interfaces

```go
type Provider interface {
	Complete(ctx context.Context, req Request) (*Response, error)
	Stream(ctx context.Context, req Request) (<-chan Delta, <-chan error)
	CountTokens(messages []Message) (int, error)
	ModelInfo() ModelInfo
}

type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

type Agent interface {
	ID() string
	Instructions(ctx context.Context, session *Session) string
	Tools() []Tool
	Step(ctx context.Context, session *Session) (StepResult, error)
}

type ContextProcessor interface {
	Name() string
	Process(ctx context.Context, sess *Session, req *LLMRequest) error
}

type Store interface {
	Get(ctx context.Context, sessionID string) (*Session, error)
	Create(ctx context.Context, session *Session) error
	AppendEvent(ctx context.Context, sessionID string, event Event) error
	Search(ctx context.Context, query string, limit int) ([]Session, error)
}
```

## Context Pipeline

Processors build each LLM request from the session log. They are pure functions — read session state, never write it.

```go
var DefaultProcessors = []ContextProcessor{
	&WorkspaceProcessor{},                       // load AGENTS.md / SOUL.md
	&HistoryProcessor{},                         // select and format events
	&SkillListProcessor{},                       // inject skill names only, not full content
	&ToolListProcessor{},                        // lazy-load schemas above 20 tools
	&TokenGuardProcessor{RotThresholdPct: 0.60}, // compact at 60%, not 95%
}
```

Compaction runs offload before summarize. Offload writes tool results to disk and keeps a path reference in context — reversible. Summarization is lossy and final.

## Features

- **Provider resilience** — key rotation on rate limits, fallback chains, budget enforcement
- **Lane queue** — concurrent across sessions, serialized within each session
- **Heartbeat** — cron-based scheduling (`@every 5m`, `0 9 * * 1-5`), missed-run recovery
- **Workspace config** — agents load `SOUL.md` and `AGENTS.md` from the directory tree
- **MCP** — connect to MCP servers as tool sources, or expose tools over MCP

## Status

v0.0.1 — feature complete, production hardening in progress. Core interfaces are stable.

## License

MIT
