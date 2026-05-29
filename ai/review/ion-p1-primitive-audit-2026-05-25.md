---
date: 2026-05-25
summary: Canto-side classification and closeout of Ion P1 ideal-first gaps.
status: resolved
---

# Ion P1 Primitive Audit

## Rule

Canto provides basic durable primitives that serious agent hosts need. If Ion
cannot build a simple Pi-level controller over a Canto primitive, treat that as
a Canto design issue until proven Ion-product-specific.

## Inputs

- Ion target:
  `/Users/nick/github/nijaru/ion/ai/design/p1-from-scratch-ideal-architecture-2026-05-25.md`
- Ion controller task: `tk-1xl1`
- Ion terminal task: `tk-6jqe`
- Ion projection task: `tk-06fd`
- Ion Canto adapter task: `tk-v45v`
- Ion tool-boundary task: `tk-5kxk`
- Ion timeout task: `tk-fziy`
- Ion scenario task: `tk-4gnm`

## Classification

| Ion gap | Default owner | Canto action |
| :--- | :--- | :--- |
| Product session owner | Ion controller over Canto primitives | `canto-21o6`: prove `Harness.Session`, `Submit`/`Turn`, `RuntimeEvents`, terminal settlement, save-point, abort, and queue/steer/follow-up are sufficient without Ion reconstructing lifecycle semantics. |
| Terminal commit boundary | Ion | No Canto work unless Canto events lack enough ordering/durability metadata for Ion to commit exactly once. |
| Live vs replay projection | Shared | `canto-wfim`: prove replay, `EventsAfter`, snapshots, effective history, provider request construction, and compaction outputs cannot diverge from valid durable events. Ion owns display formatting. |
| Runtime event contract | Canto | `canto-21o6`: Canto event envelope must be the ordered semantic source. Ion may normalize for product state, but must not infer terminal status from side effects. |
| Command/control plane | Ion | No Canto work. Slash commands, pickers, busy-input policy, and settings are product control. |
| Coding tool runtime boundary | Shared | `canto-ta4w`: Canto owns generic tool lifecycle, structured errors, content parts, streaming snapshots, ordered persistence, and cancellation. Ion owns coding-tool schema/catalog/display policy. |
| Timeout/error posture | Shared | `canto-pqk5`: Canto host-facing timeouts and terminal errors must be operation-specific and actionable. Ion owns product copy and local operation choices. |
| Acceptance gates | Ion with Canto contract hooks | No separate Canto gate unless an Ion scenario exposes missing Canto contract coverage; then add Canto tests in the owning primitive task. |
| Repo split | Shared governance | `canto-fnag`: every temporary Ion-local framework workaround needs a Canto fix, redesign, or explicit rejection. |

## Findings

### 2026-05-25 - Closeout

Owner: Canto primitive audit.

The Canto-owned P1 gaps found in this pass are fixed or have explicit
ownership:

- Session/event spine: fixed by carrying facade queue/save-point/settled events
  on the active `Turn.Events()` stream before final result/error.
- Replay/provider context: validated that provider request construction and Ion
  replay share `session.EffectiveEntries()`; added content-part recovery
  coverage.
- Tool lifecycle/results: fixed durable recovery for content parts and skipped
  preflight error results.
- Timeout/error surfaces: fixed Canto-owned waits to return operation-specific
  timeout errors while preserving deadline classification.
- Product/TUI/control plane: remains Ion-owned.

### 2026-05-25 - Active turn stream must carry facade settlement

Owner: Canto primitive.

Ion currently has to merge `Turn.Events()` with a separate
`Session.RuntimeEvents()` stream to know about queue updates, save-points, and
settled state. That is framework reconstruction in the host: terminal result
and facade settlement can race across two channels, so Ion needs local
`terminal` plus `settled` tracking.

Fix direction landed in Canto code: `RunEvent` now has a `RunHarnessPayload`
for `HarnessEvent` values. The active `Turn.Events()` stream receives
queue-update events for the current turn and receives save-point/settled events
before the final result/error event. `RuntimeEvents()` remains for out-of-turn
or multi-subscriber observers, but a normal live host can project one active
turn from one ordered stream.

### 2026-05-25 - Provider history must preserve recovered content parts

Owner: Canto primitive.

`prompt.History` already uses `session.EffectiveEntries()`, so provider request
construction and Ion replay now share the same rebuilt durable history source.
Added coverage proving a recovered tool-result message preserves
`llm.ContentPart` image data from `ToolCompletedData`.

### 2026-05-25 - Preflight tool errors need lifecycle recovery

Owner: Canto primitive.

Skipped preflight errors produced the same provider-visible tool-result message
shape as executed tools, but lacked a durable `ToolCompleted` lifecycle event.
That meant a failed final message append could lose the recoverable error
observation. Canto now persists `ToolCompletedData` for skipped preflight error
outputs before appending the tool-result message. Context-canceled/deadline
preflight aborts still produce no tool result.

## First Execution Order

1. Run `canto-21o6` beside Ion `tk-1xl1` and `tk-v45v`.
2. Run `canto-wfim` beside Ion `tk-06fd`.
3. Run `canto-ta4w` beside Ion `tk-5kxk`.
4. Run `canto-pqk5` beside Ion `tk-fziy`.
5. Feed any missing Canto contract tests into Ion `tk-4gnm` scenario traces.

## Non-Goals

- Do not move TUI rendering, slash command UX, provider/model picker behavior,
  or product settings into Canto.
- Do not resume Canto M1 docs/release work until this audit classifies or
  closes the framework-owned P1 gaps.
- Do not promote Pi+ systems just because they are present in AX, DSPy, GEPA,
  Codex, Claude Code, Slate, Droid, or other references.
