# canto

Go primitives for durable agent backends.
Focus on session durability, context construction, tools, and orchestration. Not a hosted platform or full-stack agent product.

## Ion Product Pressure

Ion is the first-class Pi -> Pi+ terminal coding-agent product built on Canto.
Canto is the general-purpose framework underneath it. Ion is the primary real
consumer pressure for Canto's native agent-loop contracts, but it is not merely
a test harness and it does not define the whole framework scope.

Canto work should stay focused on framework-owned defects exposed by Ion,
explicitly selected optimal-core redesign work, or explicitly selected M1
framework-readiness work:

- durable session log and projection validity
- runner/session coordination
- agent terminal states
- tool call/result ordering and durability
- prompt/provider-visible history construction
- retry/compaction behavior that protects real agent reliability

For the optimal-core lane, keep P1 Pi-level. Use Pi as the primary core
control; use Codex app/CLI and Claude Code for P1 ergonomics/performance
lessons; treat AX, DSPy, GEPA, Slate, Droid, and richer multi-agent/workflow
systems as Phase 2/Pi+ references unless they expose a primitive required for
P1 correctness.

Do not expand Canto's public-framework surface or SOTA primitives just because
Ion's current phase-1 bar is green. When Ion's long-term foundation requires a
stronger core, proactively rewrite or replace flawed Canto session/turn
surfaces instead of waiting for each flaw to appear as dogfood failure. Keep
Canto as the source of truth for framework-owned mechanisms, make those fixes
prove themselves through Ion, and resume docs/release posture only when the M1
lane is explicitly selected. Prefer targeted rewrites of load-bearing flawed
modules over isolated symptom patches or whole-repo rewrites.

## Project Structure

| Directory    | Purpose                                                              |
| ------------ | -------------------------------------------------------------------- |
| `llm/`       | Layer 1: Provider-agnostic LLM interface, streaming, cost            |
| `agent/`     | Layer 2: Agent loop (perceive → decide → act → observe)              |
| `session/`   | Layer 3a: Durable append-only event log, JSONL/SQLite stores         |
| `prompt/`   | Layer 3b: Context engineering pipeline, compaction, KV-cache helpers |
| `tool/`      | Layer 3c: Tool execution, registry, MCP client/server                |
| `skill/`     | Layer 3d: Progressive disclosure skill packages (SKILL.md standard)  |
| `runtime/`   | Layer 3e: Session execution, lane queue, heartbeat, workspace config |
| `memory/`    | Layer 3f: In-context + external memory, SQLite-backed, vector store  |
| `workspace/` | Rooted, symlink-safe workspace filesystem capability                 |
| `x/`         | Extension packages: graph, swarm, eval, channel, rl, obs, guardrail  |
| `ai/`        | Tracked AI project context and design memory                         |
| `.tasks/`    | Local-only task tracker state — excluded via `.git/info/exclude`     |

### AI Context Organization

**Purpose:** Keep project state, architecture, and decisions aligned between
sessions.

**Session files** (read every session):

- `ai/STATUS.md` — current state, blockers, active work (read FIRST)
- `ai/DESIGN.md` — architecture, layer breakdown, interface decisions
- `ai/DECISIONS.md` — append-only design decisions with rationale
- `ai/PLAN.md` — current phase frontier plus completed sprint archive

**Reference files** (loaded on demand):

- `ai/research/` — external research, prior art notes
- `ai/design/` — per-package deep specs
- `ai/tmp/` — scratch artifacts (gitignored)

**Task tracking:** `tk` CLI with `.tasks/` kept local-only. Use `tk ready` to find pending work.

## Technology Stack

| Component     | Technology                                        |
| ------------- | ------------------------------------------------- |
| Language      | Go 1.23+                                          |
| Module path   | `github.com/nijaru/canto`                         |
| Scheduling    | `github.com/robfig/cron/v3`                       |
| SQLite        | `modernc.org/sqlite` (pure Go, FTS5, no CGo)      |
| IDs           | `github.com/oklog/ulid/v2` (sortable event ULIDs) |
| Observability | `go.opentelemetry.io/otel`                        |
| JSON Schema   | `github.com/invopop/jsonschema`                   |
| Testing       | `go test`                                         |
| Formatting    | `golines --base-formatter gofumpt`                |

## Commands

```bash
# Format (only tracked files)
make fmt

# Test
make test

# Build
make build

# Format + test + build
make check

# Tidy
go mod tidy
```

## Verification Steps

Commands that must pass before shipping:

- Build: `make build`
- Tests: `make test`
- Format: `make fmt`

## Code Standards

| Aspect         | Standard                                                                                                                                    |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| Architecture   | Layers depend downward only; extensions depend on Layer 3, never reverse                                                                    |
| State          | Session event log is append-only — never mutate or delete events                                                                            |
| Interfaces     | Keep the 5 core interfaces small; compose from them                                                                                         |
| Context        | `RequestProcessor` shapes the in-flight request; `ContextMutator` records durable session or external changes and may declare `SideEffects` |
| Orchestration  | Graph routing and coordination are Go functions; agent behavior within a turn is LLM-decided                                                |
| Compaction     | Offload (reversible) before summarize (lossy); never skip to summarize                                                                      |
| Tool loading   | Lazy when > 20 tools; present `search_tools` meta-tool first                                                                                |
| KV cache       | System prompt always first message; never reorder or modify message prefix                                                                  |
| Error handling | Let errors propagate; catch only to recover                                                                                                 |
| Naming         | Proportional to scope; no V2/legacy/new markers                                                                                             |

## Go Idioms

Use the `go-expert` skill for full guidance. Key modern idioms:

- `slices` / `maps` packages — not manual loops or `sort.Slice`
- `iter.Seq` / `iter.Seq2` — range-over-function iterators (Go 1.23+)
- `sync.WaitGroup.Go` — replaces `Add(1); go func() { defer Done() }()`
- `errors.AsType[T](err)` — type-safe error unwrapping (Go 1.26)
- `t.Context()` in tests — not `context.TODO()`

## Design Principles

1. **Explicit coordination** — graph routing and task assignment are Go code; agents decide their own behavior within a turn
2. **Composable over complete** — small well-designed interfaces that compose cleanly
3. **Append-only state** — session event log is never mutated, ever

## Development Workflow

1. Research prior art → `ai/research/{topic}.md`
2. Synthesize architecture → `ai/DESIGN.md` or `ai/design/{package}.md`
3. Record decision → `ai/DECISIONS.md`
4. Check phase frontier → `ai/PLAN.md`
5. Implement with TDD — test gates in spec must pass before moving phases
6. Run `go test ./... && go build ./...`
7. Update `ai/STATUS.md` with findings

During Ion stabilization:

- Check Ion's active core-loop audit docs before broad new Canto planning.
- Add Canto tasks for concrete framework issues and for explicitly selected
  optimal-core redesign slices, not Ion product work.
- After a Canto fix, run the focused package test and `go test ./...`; then import the commit into Ion and verify Ion.

## Current Focus

See local `ai/STATUS.md` for active work and `ai/PLAN.md` for phase status.
