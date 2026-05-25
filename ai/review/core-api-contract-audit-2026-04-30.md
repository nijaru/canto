---
date: 2026-04-30
summary: Focused Canto core API contract audit under Ion validation.
status: active
---

# Canto Core API Contract Audit

## Purpose

Audit Canto from the framework/API-contract side, not from isolated Ion symptoms. Ion is the first-class downstream product and primary real consumer pressure, but Canto must stay a general-purpose agent framework: move mechanisms every serious host needs into Canto, and leave product policy, terminal UX, and coding-agent defaults in Ion.

No whole-repo rewrite is planned. Targeted redesign or module rewrite is allowed when an API boundary is structurally wrong or makes the core-loop invariant hard to prove.

## Scope

Core first:

- `session/` — append-only event log, replay, snapshots, effective projection, host-facing history.
- `runtime/` — run queue, runner/session coordination, retry/cancel/terminal event flow.
- `agent/` — model/tool loop, stream event ordering, turn stop states.
- `tool/` — execution contracts, tool result ordering, error/cancel durability.
- `prompt/` and `llm/` — neutral request construction and provider-visible validity.
- `governor/` — compaction, budget/overflow recovery, retry integration.

Deferred unless a core finding pulls it in:

- `memory/`, `skill/`, `workspace/`, `safety/`, `coding/`, `x/*`, examples, release/docs polish.

## Invariants

- Canto never writes provider-visible invalid history.
- `EffectiveMessages` is provider-visible only and always valid.
- `EffectiveEntries` is the canonical host-facing projection.
- Hosts should not reconstruct framework lifecycle semantics from raw events.
- Slash/local/UI-only behavior is host-owned and must not leak into Canto contracts.
- Every accepted run reaches an explicit terminal state that can be replayed safely.
- Tool lifecycle metadata is durable enough for host replay without Ion-specific raw-event scans.
- Retry, cancellation, provider error, compaction, and tool error states remain recoverable on the next turn.

## Audit Checklist

| Area | Status | Files | Contract Questions |
| ---- | ------ | ----- | ------------------ |
| C0 Baseline and task/doc alignment | complete | `ai/STATUS.md`, `ai/PLAN.md`, `tk` | Is the active Canto work tied to Ion validation and a Canto task? Are stale "no issue" notes corrected? |
| C1 Session event/projection contract | fixed, monitoring through Ion | `session/*.go` | Are append validation, snapshots, `EffectiveMessages`, and `EffectiveEntries` sufficient for provider history and host replay? |
| C2 Runtime runner/session coordination | fixed, monitoring through Ion | `runtime/*.go` | Can queue wait, cancel, retry, and terminal events be proven from durable state? |
| C3 Agent loop/tool lifecycle | fixed, monitoring through Ion | `agent/*.go`, `tool/*.go` | Are message/tool events ordered once, persisted once, and recoverable after errors/cancel? |
| C4 Prompt/provider-visible request construction | fixed, monitoring through Ion | `prompt/*.go`, `llm/*.go` | Are system/developer/context/cache boundaries valid across providers? |
| C5 Retry/compaction/budget | fixed, monitoring through Ion | `governor/*.go`, runtime integration | Does overflow/retry rebuild from session state and leave durable resumable traces? |
| C6 Non-core quarantine | fixed, monitoring through Ion | `memory/`, `skill/`, `workspace/`, `safety/`, `coding/`, `x/*` | Which packages are deferred vs load-bearing for the native loop? |

## Recent Context

- `09140f7 feat(session): expose tool lifecycle metadata` added host-facing `HistoryEntry.Tool` projection metadata so Ion no longer has to scan raw Canto events for tool replay.
- Ion imported that revision in `ec5a548 refactor(storage): use canto tool projection` and passed deterministic storage/backend/app tests plus focused race tests.
- This improves the Canto/Ion boundary, but it does not prove all core Canto contracts are sound.

## Findings

### C1 Session Event/Projection Contract

- Fixed `AttachWriteThrough` closed-channel handling so detach cannot persist zero-value events.
- Fixed snapshot `ToolHistory` hygiene: non-tool entries and mismatched tool IDs now drop tool metadata; valid tool snapshot metadata is normalized from the tool message.
- Fixed projection snapshot construction so entries and cutoff event are read under one session lock. A concurrent append can no longer make a snapshot cutoff cover an event missing from snapshot entries.
- Tightened write-side `MessageAdded` validation: zero/unknown message roles are rejected at append/store boundaries and legacy bad rows are filtered from effective history.
- Verification so far:

```sh
go test ./session -count=1
go test -race ./session -run 'TestProjectionSnapshotter|TestAttachWriteThrough' -count=1
go test ./runtime ./agent ./tool ./prompt ./llm ./governor -count=1
go test ./... -count=1
```

### C2 Runtime Runner/Session Coordination

- Fixed `Runner.Send`/`SendStream` serialization: user-message append now happens inside the per-session queue/coordinator lane, not before enqueue. Concurrent sends can no longer leak a later user message into the active turn's provider-visible history.
- Added regression coverage for both the local serial queue and `LocalCoordinator` lease path.
- Fixed `LocalScheduler.Schedule` timer publication: the scheduled task timer is assigned under the task mutex before the callback can enter `start`/`finish`, closing the race found by the runtime race gate.
- Verification so far:

```sh
go test ./runtime -run TestRunnerSendAppendsUserInsideSerializedLane -count=1 -v
go test ./runtime ./agent ./tool ./prompt ./llm ./governor -count=1
go test -race ./runtime -run 'TestRunnerSendAppendsUserInsideSerializedLane|TestRunnerLocalQueueWaitTimeoutDoesNotCancelActiveTurn|TestRunnerQueuedTurnWaitTimeoutRecordsTerminalEvent' -count=1
go test -race ./agent ./runtime ./tool -count=1
go test ./... -count=1
```

### C3 Agent Loop/Tool Lifecycle

- Tool-boundary failures are now model-visible tool observations instead of recoverable step errors when possible. Hook blocks, approval requirement failures, approval denials, and ACRFence ambiguous replay all append one role=`tool` message for the assistant call, so public `Step` cannot leave a dangling provider-visible tool call that only `Turn` escalation or projection recovery can repair.
- Tool panics now record a durable `ToolCompleted` error and a model-visible tool result, matching ordinary tool execution errors. A panic is treated as a failed tool observation, not as framework control-flow.
- Verification so far:

```sh
go test ./agent -run 'TestRunTools_ACRFenceRejectsStartedOnlyExecution|TestTurnRecordsPanicToolFailureAsToolResult|TestStepPanicToolFailureLeavesProviderHistoryComplete|TestRunTools_PanicRecovery|TestRunTools_ApprovalDeny|TestRunToolsRecordsToolCompletedError|TestRunToolsRecordsCanceledToolResult' -count=1 -v
go test ./runtime ./agent ./tool ./prompt ./llm ./governor -count=1
go test -race ./agent ./runtime ./tool -count=1
go test ./... -count=1
```

### C4 Prompt/Provider-Visible Request Construction

- Fixed provider preparation validation order: `llm.PrepareRequestForCapabilities` now validates the neutral request before capability rewriting. A late privileged `system`/`developer` row can no longer be hidden by rewriting it to `user` for providers without a system role.
- Expanded `llm.ValidateRequest` to reject invalid/empty roles, empty assistant messages, and orphan/duplicate tool results while still allowing missing tool results that `TransformRequestForCapabilities` can synthesize before final validation.
- Fixed `llm.Request.Clone` to copy `ResponseFormat` and its schema map so provider-prepared request copies do not share mutable structured-output state with the neutral request.
- Fixed retry test probes to use atomic counters so the retry/race gate can be trusted.
- Verification so far:

```sh
go test ./llm ./prompt -count=1
go test ./runtime ./agent ./tool ./prompt ./llm ./governor -count=1
go test -race ./llm ./prompt ./agent ./runtime -count=1
go test ./... -count=1
```

### C5 Retry/Compaction/Budget

- Confirmed the native overflow-recovery contract belongs at `runtime.Runner`, not only at provider-wrapper level. `runtime.WithOverflowRecovery` retries the whole agent turn after durable compaction, so the second provider request is rebuilt from `session.EffectiveMessages`.
- Added runtime regression coverage for both the minimal runner contract and the real `agent.New`/provider request path. The tests prove compaction runs once for the overflow retry, the original user message is not duplicated, and the retry request contains the compacted session context.
- Clarified `governor.RecoveryProvider` documentation: it retries the same already-built request and is only safe when the compact callback can make that request succeed without rebuild. Session-backed agents should use runtime-level overflow recovery.
- Verification so far:

```sh
go test ./runtime -run 'TestRunnerOverflowRecovery' -count=1 -v
go test ./runtime ./governor ./prompt ./llm ./agent -count=1
go test -race ./runtime ./governor ./llm ./prompt -count=1
go test ./... -count=1
```

### C6 Non-Core Quarantine

- Dependency audit finding: the core `agent` package imported `x/tracing`, so an extension package was load-bearing in every native turn. This violated the `x/` boundary: anything required by the hello-agent/native loop belongs in core, not `x/`.
- Promoted `x/tracing` to `tracing/` and updated core/importing packages. This is a clean pre-alpha rename with no compatibility shim.
- Remaining reviewed boundaries so far:
  - `runtime.Bootstrap` depends on `workspace/` for explicit workspace snapshots; that is a core mechanism.
  - `runtime.ChildRunner` now accepts generic `agent.RuntimeConfig` for scoped child execution instead of owning skill policy directly; child skill validation, tool scoping, and preload composition moved to `skill.RuntimeConfig`.
  - `session` owns artifact event descriptors directly; artifact body storage
    helpers moved to `artifact.StoreSessionArtifact`, so durable sessions no
    longer depend on the artifact store package.
  - `governor` owns token/budget/compaction prompt machinery; approval
    circuit-breaker prompt injection moved to `approval.CircuitBreakerGuard`.
    Artifact-backed offload remains in `governor` because it is part of the
    Pi-like context governance path Ion uses for manual compaction, proactive
    compaction, and overflow recovery. `MinKeepTurns` now keeps complete
    recent user turns for offload and summarize instead of keeping a raw
    message suffix.
  - `agent.WithBudgetGuard` now uses an agent-local request processor instead
    of importing `governor`. `turnState` maps any error implementing the small
    budget-exceeded marker to `TurnStopBudgetExhausted`, and
    `governor.BudgetExceededError` implements that marker for hosts that still
    install `governor.NewBudgetGuard` explicitly.
  - The root harness facade no longer retains concrete workspace/executor/
    safety capability fields or builds workspace/executor tools directly.
    Capability-tool construction moved to the opt-in `environmenttool`
    package, while root hosts keep registering explicit tools through
    `HarnessBuilder.Tools`.
  - Root `HarnessBuilder.Compaction` and `Session.Compact` were removed. Hosts
    that want proactive compaction or overflow recovery now explicitly compose
    `governor.CompactSession` through `runtime.WithBeforeRun` and
    `runtime.WithOverflowRecovery`, which is the path Ion already uses. The
    base root package no longer imports `governor` or `artifact`.
  - `approval.Gate` no longer imports the generic `audit` package. It emits
    approval-local audit events through `approval.AuditLogger`; hosts that want
    the shared JSONL audit format opt in through `approvalaudit.New`.
  - `prompt.MemoryPrompt` was moved to `memory/memoryprompt.New`; core `prompt` no longer imports `memory/`, and hosts opt into memory-backed retrieval through the explicit adapter package.
  - `tool.NewTyped` / `tool.MustTyped` were moved to `tool/typedtool`, and approval-capable tools now implement `approval.RequirementProvider`; core `tool` no longer imports approval state.
  - `tool/mcp` depends on `safety/`/`workspace/`, but MCP registration remains deferred in Ion and is not part of the native minimal loop.
- Verification:

```sh
go list -deps ./session ./runtime ./agent ./tool ./prompt ./llm ./governor | rg '^github.com/nijaru/canto/x/' || true
go test ./agent ./tracing ./x/swarm -count=1
go test ./runtime ./agent ./tool ./prompt ./llm ./governor ./tracing -count=1
go test -race ./agent ./runtime ./tracing ./x/swarm -count=1
go test ./... -count=1
```

### Phase 5 Whole-Codebase Follow-Up

`canto-hr9r` extends the C1-C6 core audit into framework-adjacent packages.
The rule is still concrete findings only: fix small correctness or boundary
issues in green slices, refactor oversized files when that makes ownership
clearer, and avoid Ion-specific product policy.

Reviewed/refactored surfaces so far:

- Root harness, runner/session APIs, prompt builder, provider request helpers,
  session stores/rebuilder/export, runtime scheduler/child/coordinator/lane,
  agent tool lifecycle, hooks, tracing, workspace VFS/search, memory manager,
  memory stores/index/VFS/vector search, coding executor/file tools, approval,
  service typed tools, skills, `x/context`, `x/tools`, `x/graph`, and `x/redis`.
- Runtime coordinator/lane were inspected after focused and race coverage; no
  local split was made because the files remain cohesive and better covered
  than the remaining optional surfaces.
- `x/redis` split and race-compiled under `-tags redis`; live Redis behavior
  still requires `CANTO_TEST_REDIS_URL`.

Recent concrete fixes from the follow-up pass:

- Core memory block retrieval now uses namespace-qualified synthetic memory IDs
  so same-name core blocks from multiple namespaces do not collapse during RRF
  fusion.
- `runtime.ChildRunner` no longer imports `skill/` or `agentskills`; hosts that
  want skill-scoped child agents compose `skill.RuntimeConfig` before spawning.
- Durable session artifact descriptors no longer make `session` import
  `artifact/`; artifact body storage and record helper composition now live in
  the artifact package.
- Approval circuit-breaker prompt injection no longer makes `governor` import
  `approval/`; hosts install `approval.CircuitBreakerGuard` when they need
  that prompt guard.
- File-reference expansion no longer treats email addresses as `@file`
  references and handles angle-bracketed references.
- Approval policy errors append a terminal `ApprovalCanceled` event so sessions
  do not remain durably waiting with no pending HITL request.
- `SmartResolver` now updates provider health when a stream finishes, so
  transient streaming failures cool the provider instead of being counted as
  success at stream start.
- macOS Seatbelt sandbox profiles escape path strings before interpolating them
  into profile rules.
- FileStore rejects path-like caller-supplied artifact IDs before using them in
  the `objects/<id>/...` storage layout.
- MCP client discovery/call handling now rejects nil external SDK tool/result
  values cleanly instead of panicking at the protocol boundary.
- Reference examples now close persistent session stores, and the autoresearch
  example checks restore/log write errors instead of silently continuing after a
  failed revert or JSONL append.
- Ignored examples now check setup, append, and input errors, and the quickstart
  example labels itself as the lower-level agent/session path instead of the
  absolute minimum host path.
- JSONL audit logger close is serialized with log writes and leaves the logger
  explicitly closed, so concurrent close/log callers cannot close the file while
  a line is being written.

Latest checkpoint:

```sh
go test ./governor -count=1 -timeout 180s
go vet ./...
go test ./... -count=1 -timeout 300s
git diff --check
```

Remaining non-code follow-up:

- Docs/godoc polish remains deferred under `canto-khhl`.
- Broader public API shape review should be driven by M1 readiness or fresh Ion
  feedback, not by continuing the Phase 5 code audit.

## Exit Criteria

- Each core area above is reviewed against its contract, with findings either fixed, logged as Canto tasks, or explicitly deferred as non-core.
- Canto tests pass:

```sh
go test ./session -count=1
go test ./runtime ./agent ./tool ./prompt ./llm ./governor -count=1
go test ./... -count=1
```

- Ion imports any Canto core fixes and validates the native loop there before this audit is closed.

## Current Outcome

The original C1-C6 native-loop blocker list is closed, but the Ion-first P1
kernel-reduction lane remains active. Keep reducing optional dependencies from
core packages while Ion is still pre-release and using Pi as the phase-1
control.
