# Status

**Phase:** Phase 5: M1 stabilization before Ion rebuild
**Focus:** M1 stabilization under Ion validation. Current pass is framework-owned core-loop correctness exposed by Ion: session append/projection validity, runner/agent terminal states, tool durability, prompt/provider history, retry, and compaction reliability.
**Blockers:** None.
**Updated:** 2026-04-28

## Context

Sprints 01-06 and the Phase 4 architecture-correction tranche are complete. The primitives are load-bearing: durable sessions with replay/projections, identity-first workspace (WorkspaceFS, ContentRef, dedup, search, OverlayFS, MultiFS+memory.FS), tiered compaction, cache-aware mutations, subagent delegation, progressive-disclosure skills, MCP tools, approval/auto-mode with circuit breaker, OTel tracing, and eval harnesses.

Phase 5 still has SOTA and DX inputs, but the active operating mode is now Canto stabilization:

- **Canto owns mechanism:** durable sessions, prompt/runtime boundaries, tool execution, workspace capability, compaction, approval state-machine seams, provider normalization, and examples that prove the pieces compose.
- **Ion owns product policy:** terminal UX, task/planner behavior, approval delivery and thresholds, shell classifier heuristics, memory aggressiveness, command catalog choices, and end-user workflow.
- **Ion validates Canto externally:** Ion should expose missing or awkward primitives, but Ion work is not active in this repo. Do not keep standing Ion tasks in Canto; add a Canto task only when separate Ion work identifies a concrete framework issue.
- **Ion feedback tracker:** confirmed Ion-derived framework issues live in `ai/review/ion-feedback-tracker-2026-04-28.md`. `ai/ion-framework-issues.md` is now only a legacy pointer.
- **Ion as acceptance test:** defer public-framework expansion, SOTA primitives, and release/docs polish while Ion is exposing native core-loop failures. Fix concrete framework defects here, then import the Canto revision into Ion and verify there.

SOTA/DX research is part of the Canto pre-Ion gate when it can change stable API or primitives. New research remains delta-based and must name the Canto primitive it would change.

Current authoring-surface inputs:

- `ai/design/authoring-surface.md` completed `canto-0j58`; `canto-gymf`, `canto-43vh`, and `canto-umuc` landed the root authoring seam, maintained coding-agent reference, and typed service/API helper.
- `ai/design/api-surface-review-canto-3p5m.md` now distinguishes real DX gaps from stale scratch findings.
- `ai/research/dspy-authoring-insights-2026-04.md` captures DSPy lessons for signatures, modules, adapters, eval metrics, and offline optimization.
- Existing `ai/research/frameworks/` notes already cover LangGraph, PydanticAI, AutoGen, Vercel AI SDK, MCP, and adjacent framework comparisons. Future SOTA work should be delta-based.

## Next (M1 stabilization order)

**Ion validation gate:**

- Active work comes from Ion's core-loop audit. Do not add broad Canto roadmap work unless Ion identifies a concrete framework-owned issue.
- Current local finding under review: `session.Append` should reject future empty/no-payload assistant `MessageAdded` rows at the session boundary, while projection sanitation remains for legacy/corrupt history.

**Release/doc gate:**

- `canto-khhl` (p3, deferred) Docs completeness pass for M1 - README, examples, provider doc, and godoc enough for a new user.
- `canto-2if9` (p3, blocked on docs and any future confirmed consumer-framework issues) Publish first-alpha package contract - one page, no compatibility promise beyond the stated alpha scope.

**Deferred research and optional primitives:**

- `canto-pc4b` (p4) Forked subagents from parent session snapshots - only if Ion or runtime validation proves the current child-session model is insufficient.
- `canto-ic25` / `canto-mr13` (p4) SOTA cadence and interrupt generalization - post-M1 unless a concrete blocker appears.

**Design hygiene:**

- `canto-3xay` (p3, deferred) DESIGN.md pillar consolidation follow-through — documentation hygiene, not an Ion-switch blocker.

## Recently landed

- Ion feedback tracking cleanup — stale Ion issue notes were consolidated into `ai/review/ion-feedback-tracker-2026-04-28.md`; there are no open confirmed Ion-derived Canto framework issues as of 2026-04-28.
- `canto-h9vq` — Ion compaction reliability feedback fixed: working-set extraction now recognizes common coding-agent tool names (`read`, `list`, `grep`, `write`) and `file_path` arguments, so durable compaction snapshots preserve files from real Ion sessions. Focused governor tests and `go test ./... -count=1` pass.
- current Ion feedback slice — agent write-side assistant payload validation now matches effective-history projection: whitespace-only assistant content/reasoning is not durably appended, reasoning-only payloads are preserved, and provider errors leave durable `TurnCompleted` error data. `go test ./...` passes.
- `canto-on6q` — RetryProvider now supports retry-until-context-cancel and emits retry callbacks so hosts can show/persist transient provider retry status without owning backoff mechanics.
- `canto-wtau` — prompt/session boundary fixed: effective session history demotes durable `system`/`developer` messages to transcript context, compaction summaries/working sets are non-privileged user context, and request validation rejects privileged messages after transcript messages before provider send.
- `canto-s73a` — session log split into transcript (`MessageAdded`), model-visible context (`ContextAdded`), and hidden durable events; bootstrap, harness, memory, file-reference, and compaction context now replay as non-privileged user-role context.
- `canto-4jhd` — effective history entries now preserve source event type and context kind so consumers can distinguish transcript from context without parsing prompt text.
- `canto-y46p` — stable context placement and cache-prefix boundaries landed as Canto primitives.
- `canto-bwjh` — full design review cleanup: session owns canonical stable-context ordering, `HistoryEntry.ContextPlacement`, and `llm.Request.CachePrefixMessages`.
- follow-up cache audit — `LazyTools` now preserves `CachePrefixMessages` when inserted after history, with regression coverage for that ordering.
- `canto-eb75` — audit pass found and fixed export/eval contract drift: trajectory inputs now preserve typed context-vs-transcript entries, and static eval environments seed context explicitly instead of provider-style setup messages.
- `canto-tsbj` — `llm.Request` gained cache-aware message insertion helpers; built-in request processors now use them; builder append now lands before cache/capability finalizers by default.
- `canto-xi6e` — built-in OpenAI-compatible and Anthropic providers prepare provider-specific request copies at send time, preserving neutral context for provider/model switches.
- `canto-gymf` — root `canto.NewAgent` authoring seam, message helpers, and public `llm.FauxProvider`
- `canto-43vh` — buildable Claude Code/Codex/Cursor-class reference coding/service agent
- `canto-umuc` — typed service/API tool helper plus reference-agent validation
- `canto-l2iy` — canonical coding-agent tools promoted from `x/tools` to stable `coding/`
- `canto-u99s` — runtime-level overflow recovery and proactive compaction path
- `canto-20vn` — iterative compaction coverage plus split-turn summary preservation
- `canto-i0h0` — absorbed into `canto-gymf`; hello example/FauxProvider path is landed
- `canto-qmxu` — `context/` renamed to `prompt/` with no compatibility shim
- `canto-r9de` — cross-provider request transform for provider/model switching
- `canto-ijl4` — shared turn-state logic extracted into pure agent logic
- `canto-7mp1` — two-phase tool execution finalized: sequential preflight, concurrent metadata-driven I/O, ordered result emission, and execution-boundary `ToolStarted` events
- `canto-btl6` — alpha blocker preflight complete; first-alpha release gate names M1 blockers, consumer validation expectations, provider matrix, coverage audit, single-Runner live-session scope, and distributed-worker non-claim
- `canto-8cl4` — M1 provider matrix documented in `docs/providers.md`; README now states supported, provisional, bring-your-own, and deferred provider levels
- `canto-csp2` — load-bearing coverage audit complete; added workspace Root/OverlayFS/MultiFS regression coverage and recorded package coverage snapshot
- `canto-u4vq` — approval classifier seam verified; `PolicyFunc` supports host-owned local shell classifiers with HITL escalation via `handled=false`
- `canto-p73h` — Ion friction intake created at `ai/design/ion-friction-intake.md`; future Ion findings should return only as concrete Canto framework issues
- `canto-mofv` — prompt/defaults review tightened: `docs/prompt-and-tools.md` replaced vague defaults docs and custom/runtime request processors now run before cache alignment
- `canto-5y3y` / `canto-87se` — targeted DSPy/GEPA review resolved; future optimizer work should be explicit eval-trace artifacts in `x/eval`/`x/optimize`, not runtime prompt mutation
- `canto-3vjn` — governance API names stabilized: `approval.Gate`, `safety.Config`, `hook.Handler`/`FromFunc`, `audit.StreamLogger`, `governor.CompactQueue`
- `canto-q56s` — tool-surface audit updated: no tool presets, no built-in glob/grep-style coding tools, configurable `ShellTool`, and `x/tools` remains extension-scoped
- `canto-m4nb` / `canto-39ur` — closed from the Canto queue; Ion migration/notes now live in the Ion repo and will feed back only concrete Canto issues

## Backlog (p4)

- `canto-j6j2`: Migration concept maps (explicitly deferred)
- `canto-3xay`: DESIGN.md pillar consolidation follow-through

## Deferred

- `canto-2if9`: Publish alpha release note (blocked on docs and any confirmed consumer-framework issues)

## Completed: Phase 4 Tranche and Frontier

See PLAN.md for the completed sprint stack and architecture-correction outcomes.
