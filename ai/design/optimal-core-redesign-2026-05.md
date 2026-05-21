---
date: 2026-05-20
summary: Canto-side optimal core redesign driven by Ion before Phase 2 product work.
status: active
---

# Optimal Core Redesign

## Decision

Canto should not wait for Ion dogfood to expose each core ownership problem.
Ion is about to build more product surface, so Canto needs a stronger primary
runtime contract now.

Canonical cross-repo scratch spec:
`/Users/nick/github/nijaru/ion/ai/design/ideal-canto-ion-core-2026-05.md`.
Use this Canto doc for the Canto-owned implementation slice, but keep tasks
aligned with that ideal spec.

The high-level architecture remains correct:

- Canto owns generic agent runtime mechanisms.
- Ion owns coding-agent product policy and presentation.

The rewrite target is the common Canto host path: session facade, ordered turn
stream, lifecycle events, tool settlement, usage, cancellation, retry, and
compaction. Lower-level primitives can remain for advanced composition, but
the default host path should be hard to misuse.

Reference posture:

- P1 remains Pi-level. Pi is the primary control for the default coding-agent
  core and the simplest correct runtime shape.
- Codex app/CLI and Claude Code inform P1 ergonomics, input/turn lifecycle,
  permission/profile seams, terminal UX, and performance discipline.
- Google AX is a Canto-level reference for substrate/runtime shape, resumable
  streams, approvals, distributed execution, and future orchestration.
- DSPy, GEPA, Slate, Droid, richer Codex/Claude workflows, AX orchestration,
  and similar systems are Phase 2/Pi+ inputs unless they reveal a primitive
  required for P1 correctness.
- Performance and UX are part of correctness: the Canto stream must avoid
  unnecessary latency, polling, host-side flush loops, and replay costs that
  would make Ion feel worse.

## Problems To Remove

| Area | Current Risk | Target |
| --- | --- | --- |
| Stream assembly | Root `Session.PromptStream` combines event snapshots, live watchers, and `SendStream` chunk callbacks. Ordering depends on careful flushing around callbacks. | A native turn transaction emits one ordered framework stream. |
| Thin facade | Host-facing `Session` delegates to `Runner` while key semantics live across runtime, agent, session, hooks, overflow recovery, and subscriptions. | The session facade is the normal API for submit, stream, cancel, replay, compact, usage, and runtime metadata. |
| Host lifecycle burden | Ion reconstructs generic usage deltas, active-tool status, cancellation gates, compaction status, and terminal settlement around Canto events. | Canto exposes those as durable events or stream metadata so hosts project instead of reconstruct. |
| Async host hooks | Hook or handler delays can expose ordering bugs unless the durable stream contract explicitly covers yielded host work. | Contract tests prove host-observable assistant/tool/terminal events cannot outrun durable settlement. |

## Target Facade

The exact API can change during implementation, but the normal host shape should
look like this:

```go
type Session interface {
    ID() string
    Submit(ctx context.Context, prompt Prompt) (*Turn, error)
    Replay(ctx context.Context, opts ReplayOptions) ([]Event, error)
    Compact(ctx context.Context, opts CompactOptions) (CompactResult, error)
}

type Turn interface {
    ID() string
    Events() <-chan Event
    Cancel(ctx context.Context) error
    Result() (TurnResult, error)
}
```

Required semantics:

- one turn ID;
- monotonically ordered events;
- durable user commit before turn start;
- assistant/tool/error/usage/terminal events settle before host projection;
- exactly one terminal event for each accepted turn;
- cancellation settles through the same terminal path;
- retry and overflow recovery are visible framework state, not host guesses;
- usage is cumulative or delta-tagged consistently.

## Implementation Slices

1. Add Canto contract tests for async hook/handler settlement, tool ordering,
   cancellation, overflow recovery, and usage/terminal ordering.
2. Replace `Session.PromptStream` snapshot/watch/callback assembly with a
   native turn transaction or broker.
3. Promote lifecycle metadata/events for usage, active tools, compaction,
   retry, cancellation, and terminal states.
4. Keep `Runner` as an implementation/advanced-composition detail where
   possible; make the session facade the documented primary path.
5. Import into Ion and remove Ion-owned generic lifecycle reconstruction.

## Done

- Ion can call one Canto session facade for the normal native loop.
- Ion no longer needs local generic turn-state, usage-delta, tool-lifecycle,
  cancel-settlement, or compaction-correctness logic.
- Canto full tests, focused runtime/session tests, vet, and relevant race
  subsets pass.
- Ion's full optimal-core acceptance pass succeeds after import.
