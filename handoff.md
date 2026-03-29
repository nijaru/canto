# Canto Project Handoff

## Summary
Canto is a Go-native framework for **durable agent backends**. It prioritizes append-only session state, explicit context engineering, and backend-grade execution primitives over "batteries-included" application magic. It is currently in **Phase 4 (Path to Alpha)**.

## Current Project Status (2026-03-28)
- **Build**: `make build` and `make test` are passing across all core packages.
- **Tests**: 100% pass rate. Workspace verified with `go test ./...`. `x/redis` tests remain gated behind `//go:build redis`.
- **Deep Audit (COMPLETE)**: A comprehensive architectural and security audit was completed on 2026-03-28, resulting in 12 safety and concurrency fixes.

## What Just Happened

### Deep Architecture & Safety Audit (DONE)
Resolved 12 critical and architectural issues to harden the framework for the Alpha release. Key fixes:

- **Logic Fixes**: Corrected thinking block assembly in `StreamStep` (was dropping reasoning content without signatures).
- **Goroutine Leak Prevention**: Updated live session watching to support explicit cleanup. New code should prefer `Watch()` returning a `Subscription` handle with `Close()`, while `Subscribe()` remains as a compatibility wrapper.
- **Data Durability**: Fixed a race/data-loss risk in `AttachWriteThrough` by introducing a dedicated, non-lossy persistent path (`SetWriterChannel`).
- **Panic Safety**: Added `recover()` boundaries to `RunTools`, `x/swarm`, and `executeUnderLease` to prevent faulty tools or agents from crashing the entire orchestrator.
- **Context Handling**: Fixed missing context cancellation propagation in `x/pool` workers.
- **Performance**:
    - Implemented sharded locking in `JSONLStore` to eliminate global mutex contention across sessions.
    - Refactored `extractHandoff` to use reverse iterators, avoiding O(N) memory allocation on every agent step.
- **Stability**: Enforced SQLite connection pool limits (`MaxOpenConns=16`) for file-backed stores to prevent `SQLITE_BUSY` contention.

### API Migration Guide (NEW)
Documented breaking changes for consumers in `ai/review/API_CHANGES_2026_03_28.md`. Primary changes:
- `Subscribe` now returns `(<-chan Event, CancelFunc)`, and new code should prefer `Watch()` returning `Subscription`.
- `agent.StepConfig` and `agent.RunTools` now require concurrency limits.

## Phase 4 Roadmap — Remaining Work
- [x] Deep Architecture & Safety Audit (12 fixes landed)
- [x] Redis Distributed Coordinator
- [x] First-class parent/child subagent runtime
- [x] Performance baseline (event memoization, O(1) history)
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
- **Sessions**: Durable with `Watch()`/`Subscription`, `LoadUntil`, `Fork`. `WithMetadata` for context propagation.
- **Safety**: Built-in panic recovery, concurrency limits, and deterministic subscription cleanup.

## Key Files
- `ai/STATUS.md` — current state and audit results
- `ai/review/canto-framework-review-2026-03-28.md` — full audit findings
- `ai/review/API_CHANGES_2026_03_28.md` — migration guide for consumers
- `ai/DESIGN.md` — architecture, package boundaries, core interfaces
- `ai/DECISIONS.md` — append-only design decision log
- `ai/ROADMAP.md` — phase gates and progress
- `.tasks/` — task tracker (`tk`), local-only

## Developer Context
- **Language**: Go 1.26+, pure Go (no CGo)
- **Format**: `golines --base-formatter gofumpt`
- **Verification**: `go test ./...` before any commit.
- **Concurrency**: Always use semaphores for parallel I/O; never hold session locks during disk/network calls.
