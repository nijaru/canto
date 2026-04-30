---
date: 2026-04-30
summary: Focused Canto core API contract audit under Ion validation.
status: active
---

# Canto Core API Contract Audit

## Purpose

Audit Canto from the framework/API-contract side, not from isolated Ion symptoms. Ion remains the acceptance test, but Canto must stay a general-purpose agent framework: move mechanisms every serious host needs into Canto, and leave product policy, terminal UX, and coding-agent defaults in Ion.

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
| C4 Prompt/provider-visible request construction | pending | `prompt/*.go`, `llm/*.go` | Are system/developer/context/cache boundaries valid across providers? |
| C5 Retry/compaction/budget | pending | `governor/*.go`, runtime integration | Does overflow/retry rebuild from session state and leave durable resumable traces? |
| C6 Non-core quarantine | pending | `memory/`, `skill/`, `workspace/`, `safety/`, `coding/`, `x/*` | Which packages are deferred vs load-bearing for the native loop? |

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

## Exit Criteria

- Each core area above is reviewed against its contract, with findings either fixed, logged as Canto tasks, or explicitly deferred as non-core.
- Canto tests pass:

```sh
go test ./session -count=1
go test ./runtime ./agent ./tool ./prompt ./llm ./governor -count=1
go test ./... -count=1
```

- Ion imports any Canto core fixes and validates the native loop there before this audit is closed.
