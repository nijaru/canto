# Canto Project Handoff

## Summary
Canto is a Go-native framework for **durable agent backends**. It prioritizes append-only session state, explicit context engineering, and backend-grade execution primitives over "batteries-included" application magic. It is currently in **Phase 4 (Path to Alpha)**.

## Current Project Status (2026-03-24)
- **Build**: `make test` passes. `make build` has a pre-existing failure in `examples/subagents` (the `//go:build ignore` main.go means no `main()` in the `main` package). Not introduced by recent changes.
- **Tests**: all passing. `x/redis` tests gated behind `//go:build redis` (requires Docker).
- **Git**: 8 commits ahead of origin.

## What Just Happened

### Package Review Sweep (P2+P3 COMPLETE, P4 REMAINING)
All P2 and P3 packages reviewed — **no bugs found** in any newly reviewed package. 7 bugs fixed across context/, memory/, llm/, session/ (prior sessions). Key findings:

- **session/** (`canto-gjkj`): Fixed `JSONLStore.LoadUntil` boundary check and `Session.Append` metadata cloning. `EffectiveEntries` and `Subscribe` fan-out audited — no bugs.
- **agent/** (`canto-s984`): Step/Turn loops, handoff extraction, thinking block accumulation — all correct.
- **runtime/** (`canto-o5mf`): Coordinator lease semantics, lane queue, ChildRunner lifecycle, InputGate — all correct.
- **tool/** (`canto-f5ar`): 100% coverage. Clean interfaces.
- **artifact/** (`canto-etkt`): FileStore atomic write pattern verified.
- **skill/** (`canto-3w11`): Loader, registry, CRUD tools — all correct.
- **x/redis/** (`canto-ugn5`): Lua scripts, atomic lease ops — verified.
- **x/eval/** (`canto-jyva`): Parallel eval harness — correct.
- **x/tools/** (`canto-89co`): Bash, file, memory, task, search tools — all correct.

P4 remaining: `hook/`, `x/obs/`, `x/swarm/`, `x/graph/`, `x/pool/`, `x/testing/`

### Redis Distributed Coordinator (DONE)
`x/redis/RedisCoordinator` implements the `runtime.Coordinator` interface with:
- Sorted-set FIFO queues for per-session ordering
- Lua scripts for atomic grant/renew/ack/nack
- TTL-based key cleanup on Nack/crash (att/tok keys get PEXPIRE with 6x safety margin)
- Persistent lease token counter (INCR on `:tok` key)

## Phase 4 Roadmap — Remaining Work
- [x] Explicit alpha package boundary
- [x] Distributed lane coordinator (Redis adapter in `x/redis/`)
- [x] First-class parent/child subagent runtime
- [x] First-class artifact subsystem
- [x] Two-phase context pipeline
- [x] Performance baseline (event memoization, O(1) history)
- [ ] **Package review sweep** (6 P4 packages remaining)
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
- **Context**: `Processor` pipeline. `BuildPreview` (shaping only) vs `BuildCommit` (mutators + shaping).
- **Sessions**: Durable with `Subscribe()`, `LoadUntil`, `Fork`. `WithMetadata` for context propagation.
- **Artifacts**: Durable descriptors with pluggable `Store` (local file-backed default).

## Key Files
- `ai/STATUS.md` — current state, active tasks, known issues
- `ai/DESIGN.md` — architecture, package boundaries, core interfaces
- `ai/DECISIONS.md` — append-only design decision log
- `ai/ROADMAP.md` — phase gates and progress
- `x/redis/coordinator.go` — Redis coordinator implementation
- `runtime/coordinator.go` — Coordinator interface + LocalCoordinator
- `.tasks/` — task tracker (`tk`), local-only

## Developer Context
- **Language**: Go 1.23+, pure Go (no CGo)
- **Format**: `golines --base-formatter gofumpt`
- **Test**: `go test ./...` (Redis tests need `//go:build redis` + Docker)
- **Deps**: `github.com/redis/go-redis/v9`, `github.com/testcontainers/testcontainers-go`, `modernc.org/sqlite`, `github.com/oklog/ulid/v2`
- **Architecture**: Layers depend downward only. Extensions depend on Layer 3, never reverse.
