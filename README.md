# Canto

Composable Go framework for building LLM agents and agent swarms.

[![Go Reference](https://pkg.go.dev/badge/github.com/nijaru/canto.svg)](https://pkg.go.dev/github.com/nijaru/canto)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Design

- Graph routing and coordination are Go code. What agents do within a turn is LLM-decided.
- Session state is an append-only event log — never mutated
- Five small interfaces that compose

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/tools"
)

func main() {
	ctx := context.Background()

	registry := tool.NewRegistry()
	registry.Register(&tools.BashTool{})

	provider := openai.NewProvider(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY")})

	a := agent.New("assistant", "You are a helpful assistant.", "gpt-4o", provider, registry)

	sess := session.New("session-1")
	sess.Append(session.NewEvent(sess.ID(), session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "What Go version introduced range over functions?",
	}))

	if _, err := a.Turn(ctx, sess); err != nil {
		log.Fatal(err)
	}

	msgs := sess.Messages()
	fmt.Println(msgs[len(msgs)-1].Content)
}
```

## Architecture

```
+-------------------------------------------------------------+
|  Extensions  (graph, swarm, eval, rl, obs...)               |
+-------------------------------------------------------------+
|  Runtime     (session, context, tool, skill, memory)        |
+-------------------------------------------------------------+
|  Agent Loop  (perceive → decide → act → observe)            |
+-------------------------------------------------------------+
|  LLM         (provider, resolver, streaming, cost)          |
+-------------------------------------------------------------+
```

**Layer 1 — LLM**

- `llm/` — Provider interface, streaming normalization, cost tracking, token estimation
- `llm/providers/` — OpenAI, Anthropic, Gemini, Ollama, OpenRouter

**Layer 2 — Agent**

- `agent/` — Core loop: perceive → decide → act → observe; parallel tool dispatch; handoffs

**Layer 3 — Runtime**

- `session/` — Append-only event log, JSONL and SQLite backends, trajectory recording
- `context/` — Context pipeline: token guards, compaction, KV-cache preservation
- `tool/` — Registry, MCP client/server
- `skill/` — Progressive disclosure skill packages (SKILL.md standard)
- `runtime/` — Session runner with per-session lane queue
- `memory/` — Episode store, SQLite long-term memory, HNSW vector search

**Extensions (`x/`)**

- `x/graph/` — DAG orchestration with conditional Go routing functions
- `x/swarm/` — Blackboard-based multi-agent coordination
- `x/eval/` — Trajectory scoring harness
- `x/tools/` — Bash, code execution, sandboxed executor, memory search, lazy tool discovery

## Core Interfaces

```go
type Provider interface {
	ID() string
	Generate(ctx context.Context, req *LLMRequest) (*LLMResponse, error)
	Stream(ctx context.Context, req *LLMRequest) (Stream, error)
	CountTokens(ctx context.Context, model string, messages []Message) (int, error)
	Cost(ctx context.Context, model string, usage Usage) float64
	Capabilities(model string) Capabilities
}

type Tool interface {
	Spec() llm.ToolSpec
	Execute(ctx context.Context, args string) (string, error)
}

type Agent interface {
	ID() string
	Step(ctx context.Context, sess *session.Session) (StepResult, error)
	Turn(ctx context.Context, sess *session.Session) (StepResult, error)
}

type ContextProcessor interface {
	Process(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.LLMRequest) error
}

type Store interface {
	Load(ctx context.Context, sessionID string) (*Session, error)
	Save(ctx context.Context, e Event) error
}
```

## Context Pipeline

Processors build each LLM request from the session log. Each is a pure function — reads session state, never writes it.

```go
builder := context.NewBuilder(
	context.InstructionProcessor(instructions),
	context.ToolProcessor(registry),
	context.HistoryProcessor(),
)
```

Compaction runs offload before summarize. Offload writes tool results to external storage and keeps a reference in context — reversible. Summarization is lossy and runs only when offload isn't enough.

## Features

- **Provider resilience** — key rotation on rate limits, fallback chains, budget caps
- **Lane queue** — concurrent across sessions, serialized within each session
- **MCP** — connect to external MCP servers as tool sources, or expose a tool registry over MCP
- **Reasoning model support** — `Capabilities()` detects o1/o3 and adapts requests automatically
- **Structured outputs** — `ResponseFormat` in `LLMRequest` for JSON schema enforcement

## Status

Pre-release. Core interfaces are unstable.

## License

MIT
