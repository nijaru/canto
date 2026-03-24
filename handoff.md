# Canto Project Handoff

## Summary
Canto is a Go-native framework for **durable agent backends**. It prioritizes append-only session state, explicit context engineering, and backend-grade execution primitives over "batteries-included" application magic. It is currently in **Phase 4 (Path to Alpha)**.

## Current Project Status (2026-03-23)
- **Build**: `make test` passes. `make build` has a pre-existing failure in `examples/subagents` (the `//go:build ignore` main.go means no `main()` in the `main` package). Not introduced by recent changes.
- **Tests**: all passing. `x/redis` tests gated behind `//go:build redis` (requires Docker).
- **Git**: 4 commits ahead of origin.

## What Just Happened

### Redis Distributed Coordinator (DONE)
`x/redis/RedisCoordinator` implements the `runtime.Coordinator` interface with:
- Sorted-set FIFO queues for per-session ordering
- Lua scripts for atomic grant/renew/ack/nack (single-script Enqueue eliminates ZCard+ZAddNX race)
- TTL-based key cleanup on Nack/crash (att/tok keys get PEXPIRE with 6x safety margin)
- Persistent lease token counter (INCR on `:tok` key, separate from lease hash)
- `//go:build redis` + testcontainers-go for containerized integration tests (11 tests)
- Commits: `b5523d9` (redis), `824334a` (examples fix)

### Correctness Fixes (DONE)
Five bugs found and fixed during code review:
1. **Enqueue race** — two-command gap allowed stale sequence numbers. Fixed with single Lua script.
2. **att/tok key leak** — keys persisted forever after Nack/crash. Fixed with PEXPIRE in grant script.
3. **`parseLease` zeroed RequestID** — Ack/Renew/Nack always compared against empty string. Fixed by passing original Ticket.
4. **Lease token counter reset** — hash deletion on expiry caused token collisions. Fixed with persistent `tok` key.
5. **`examples/subagents` build** — `runExample` undefined when compiling with `//go:build ignore`. Fixed by extracting to `example.go`.

### Package Review Sweep (IN PROGRESS)
18 review tasks in `tk`. Priority order:
- **P2**: `session/` (51.6%), `llm/` (30.5%), `memory/` (59.4%), `context/` (74.6%)
- **P3**: `agent/`, `runtime/`, `artifact/`, `tool/`, `skill/`, `x/redis/`, `x/eval/`, `x/tools/`
- **P4**: `hook/`, `x/obs/`, `x/swarm/`, `x/graph/`, `x/pool/`, `x/testing/`

Run `tk ready` to find the next package to review.

## Phase 4 Roadmap — Remaining Work
- [x] Explicit alpha package boundary
- [x] Distributed lane coordinator (Redis adapter in `x/redis/`)
- [x] First-class parent/child subagent runtime
- [x] First-class artifact subsystem
- [x] Two-phase context pipeline
- [x] Performance baseline (event memoization, O(1) history)
- [ ] **Package review sweep** (18 packages remaining)
- [ ] Artifact storage refinement (`canto-n22u`)
- [ ] Ion dogfood friction capture (`canto-h9da`)
- [ ] Alpha release gate checklist (`canto-dtnr`)
- [ ] Comprehensive documentation site

## Architecture Overview

```
x/graph, x/swarm, x/eval, x/redis
        ↓
    runtime/
        ↓
  agent/, context/, tool/, hook/, skill/, memory/
        ↓
  session/, llm/
```

- **State**: Append-only event log (JSONL/SQLite). Never mutate events.
- **Coordination**: `runtime.Coordinator` interface. `LocalCoordinator` (built-in) or `RedisCoordinator` (distributed).
- **Context**: `Processor` pipeline. Preview-safe (`BuildPreview`) vs commit-time (`BuildCommit`).
- **Sessions**: Durable with `Subscribe()`, `LoadUntil`, `Fork`. `WithMetadata` for context propagation.
- **Artifacts**: Durable descriptors with pluggable `Store` (local file-backed default).

## Key Files
- `ai/STATUS.md` — current state, active tasks, known issues
- `ai/DESIGN.md` — architecture, package boundaries, core interfaces
- `ai/DECISIONS.md` — append-only design decision log
- `ai/ROADMAP.md` — phase gates and progress
- `x/redis/coordinator.go` — Redis coordinator implementation
- `x/redis/coordinator_test.go` — integration tests
- `runtime/coordinator.go` — Coordinator interface + LocalCoordinator
- `runtime/coordinator_exec.go` — Runner integration (lease renewal, ack)
- `.tasks/` — task tracker (`tk`), local-only

## Developer Context
- **Language**: Go 1.23+, pure Go (no CGo)
- **Format**: `golines --base-formatter gofumpt`
- **Test**: `go test ./...` (Redis tests need `//go:build redis` + Docker)
- **Deps**: `github.com/redis/go-redis/v9`, `github.com/testcontainers/testcontainers-go`, `modernc.org/sqlite`, `github.com/oklog/ulid/v2`
- **Architecture**: Layers depend downward only. Extensions depend on Layer 3, never reverse.
