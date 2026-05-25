# Plan

Sprints 01-06 delivered the load-bearing primitives on a dependency-ordered axis. The Phase 4 architecture correction (identity-first workspace, projections, execution-boundary security, cache-aware mutations, VFS reassembly) landed on top. Phase 5 changes rhythm.

## Phase 5: SOTA + DX

Two tracks, run in parallel, reinforcing each other:

- **SOTA track.** Continue the "research → synthesize → implement" discipline that produced sprints 01-06 and the Phase 4 correction. Monitor CC/Pi/Codex and academic sources for new patterns worth landing. New primitives keep arriving; canto's edge depends on landing them.
- **DX track.** Design a declarative authoring surface so an app author writes a working agent in ≤25 lines with no visible runtime wiring — and still drops down to full composition for advanced cases. The SOTA primitives are useless if users can't reach them ergonomically.

The tracks reinforce each other: a SOTA pattern that cannot be expressed cleanly through the authoring surface is a design signal, not an API problem. DX work surfaces which primitive shapes are wrong. SOTA work surfaces which ergonomic shortcuts are lossy.

Plan entries under either track need a named motivation — a SOTA source or a consumer/friction source — before landing on the frontier.

Canto remains a general-purpose primitives layer. It is not a coding-agent product, but Ion is a first-class coding-agent product built on top of it, with Pi -> Pi+ as Ion's roadmap. The Codex/Claude Code/Pi/Cursor-class coding agent remains a well-specified target that exercises the full primitives surface: durable sessions, workspace identity, tool runtime, compaction, approval, subagents, memory, and provider normalization. The rule: design primitives generally, validate them against serious hosts, and reject shapes that only make sense for one Ion UX choice. Ion's needs are first-class design pressure for Canto, but they do not define the whole framework scope.

The Canto/Ion split is:

- **Canto:** mechanism. Durable event log, replay/projections, prompt construction, context mutation, tool execution metadata, workspace/file capability, approval wait-state and policy seams, provider transforms, service-tool helpers, and examples/reference agents.
- **Ion:** policy and product. Terminal UX, command palette, planner/task behavior, approval copy and delivery, default shell classifier heuristics, memory aggressiveness, model choice defaults, and user workflow.
- **Shared boundary:** if every serious host would need to reimplement state-machine or durability plumbing, it belongs in Canto. If the choice expresses taste, UX, environment assumptions, or Ion's workflow, it belongs in Ion and plugs into a Canto seam.

Current correction: Ion's reopened P1 dogfood failures made Ion the acceptance
owner. Canto kept the long-term framework split and stayed a pre-M1 kernel
while Ion's Pi-level scenario matrix was red. Ion's full P1 gate passed on
2026-05-25 with deterministic, tmux, race, live backend/provider, and live
TUI/provider checks. Future Canto P1 work should start from a concrete
framework-owned Ion regression, not from broad kernel-reduction churn.

The next-phase roadmap lives in [design/framework-readiness-roadmap-2026-05-01.md](design/framework-readiness-roadmap-2026-05-01.md). Use it to keep work ordered: C0 Ion product validation, C1 M1 docs/examples/API readiness, C2 API/DX simplification, C3 workspace/sandbox, C4 eval/optimizer artifacts, C5 multi-agent/extensibility. Do not let C3-C5 research jump ahead of C0-C1.

Existing research already covers LangGraph, PydanticAI, AutoGen, Vercel AI SDK, provider SDKs, HITL, durability, graph workflows, tool orchestration, context engineering, memory, and security. The framework reference map should remain broader and current: OpenAI Agents SDK, Anthropic/Claude Agent SDK, MCP, A2A, Google ADK, Microsoft Agent Framework, Semantic Kernel, AutoGen, LangGraph/LangChain, Pydantic AI, LlamaIndex, CrewAI, Agno, Mastra, Vercel AI SDK, BeeAI, Letta, Flue, DSPy/GEPA, and Temporal/DBOS/Inngest-style durable execution systems are all valid inputs. Do not restart a broad framework survey unless a concrete gap appears. Current framework references are examples for Canto primitives, not blueprints; mixed reviews are expected. Ion is tracking a deferred recent-paper delta scan as `tk-k4y8`; it is no longer blocked by phase-1 stabilization, but it should wait until a phase-2 research lane is selected. DSPy and GEPA remain inputs for the optimization surface (signatures/modules/compilers vs. reflective prompt evolution), not the center of the design.

2026-05-01 delta: OpenAI Agents SDK, Mesa, Archil, BranchFS, foldb, and
similar systems reinforce the same direction: durable state,
versioned/isolated workspaces, snapshots/rehydration, sandbox/process
boundaries, and rich traces matter. They do not change the immediate order.
Canto can now resume M1 readiness when explicitly selected; external
workspace/storage systems are future adapters or design references, not core
dependencies.

### Canto Stabilization Roadmap

The earlier Ion-driven optimal-core closure is superseded by Ion's Pi-first P1
migration. Add Canto implementation work when it closes a concrete gap between
the current Canto/Ion split and Pi's proven session-scoped harness/session
shape.

Design source:
[`design/optimal-core-redesign-2026-05.md`](design/optimal-core-redesign-2026-05.md).
P1 stays Pi-level, with Pi as the primary core control. Codex app/CLI and
Claude Code inform P1 performance, UX, and lifecycle ergonomics. AX, DSPy,
GEPA, Slate, Droid, richer Codex/Claude workflows, and similar systems are
Phase 2/Pi+ references unless they reveal a primitive required for P1
correctness. Performance matters in this lane: stream latency, replay/resume
cost, and host-side assembly overhead are design inputs, not polish items.

| Gate | Task | Intent |
| :--- | :--- | :--- |
| 0 done | `canto-iusu` Ion-proven P1 kernel reduction | Audited Ion-used Canto surfaces and reduced, rewrote, deleted, or explicitly retained primitives based on Ion's Pi-level acceptance evidence before M1 posture resumes |
| 0 done | `canto-98el` Pi-like session facade state | `Harness.Session(id)` owns phase, active run, queue updates, save-point, settled, abort, wait-for-idle, queued prompt state, Pi-like steering/follow-up drain modes, and Ion-imported P1 facade primitives |
| 0 done | `canto-x8d0` runtime cancellation and timeout semantics | Runtime execution timeout is now opt-in; normal root/child runner turns no longer inherit a hidden whole-turn wall-clock deadline |
| 0 done | `canto-y88u` workspace/path contracts | Workspace glob patterns now reject absolute/traversal/malformed inputs like normal rooted paths and support recursive `**` matching |
| 0 done | `canto-9p8k` image content parts for Ion read parity | `llm.ContentPart` supports image data/URL, `tool.ContentTool` preserves structured tool results, and OpenAI/Anthropic converters emit image content |
| 0 done | `canto-dasc` streaming snapshot updates for Ion bash parity | Added a reusable streaming update contract so tools can replace current output snapshots while preserving final provider-visible tail output |
| 0 done | `canto-01ge` native Turn/Submit facade | Promoted the accepted turn transaction to the primary host API before Ion deleted adapter fallbacks |
| 0 done | `canto-sqtc` sequence-bounded event reads | Expose framework-owned `EventsAfter` so hosts can maintain typed projections without reaching into store internals |
| 0 done | `canto-d6kl` durable turn transaction identity and sequence | Moved turn identity/session sequence from host-only `RunEvent` metadata into durable session events/logs, aligned with AX `seq`/`exec_id` and Pi session-owned prompt lifecycle |
| 0 active | `canto-wuev` Submit/Turn public surface alignment | Make README, prompt docs, examples, and godoc teach native `Submit` / `Turn` as the common host path; `Prompt` and `PromptStream` are convenience wrappers |
| 0 done | `canto-vhjg` unified run-event envelope | Replaced `RunEvent.Type` and payload side fields with one typed payload under envelope metadata; Ion imported the exact revision |
| 0 done | `canto-33aq` typed tool authoring and environment toolkits | Promoted typed Go tool authoring and opt-in workspace/executor tools constructed from `Environment` |
| 0 done | `canto-re2x` session maintenance facade | Exposed replay, sequence-bounded events, projection snapshots, and fork on root `canto.Session`; compaction stays explicit through `governor.CompactSession` |
| 0 done | `canto-uduq` optimal-core contract tests | Added stream metadata and tests for ordered run events, usage-before-result, yielding hook settlement, and overflow-recovery stream identity |
| 1 done | `canto-dvtd` optimal-core turn transaction | Replaced `PromptStream` snapshot/watch/callback repair with an ordered session observer stream |
| 2 done | `canto-xz1w` optimal-core lifecycle events | Added typed RunEvent lifecycle/usage metadata plus compaction-start and overflow-retry events |
| 3 done | `canto-iq8h` Ion import/removal proof | Imported into Ion and removed generic lifecycle reconstruction from the Ion adapter |
| 0 done | `canto-2vxb` Flue/Pi harness facade review | Implemented the named harness/session target used as the M1 authoring seam |
| 0 done | `canto-5qb6` Roadmap stabilization pass | Aligned the roadmap around Canto mechanism vs Ion policy and removed stale frontier entries |
| 1 done | `canto-7mp1` Two-phase tool execution | Finalized sequential preflight, concurrent I/O, deterministic ordered observation emission, and execution-boundary `ToolStarted` events |
| 1 done | `canto-btl6` Alpha contract preflight | Named the concrete M1 blockers and updated `canto-2if9` so the alpha release note has a real gate |
| 1 done | `canto-8cl4` Provider matrix statement | Stated supported, provisional, bring-your-own, and deferred providers for M1 |
| 1 done | `canto-csp2` Load-bearing coverage audit | Verified load-bearing coverage and added focused workspace Root/OverlayFS/MultiFS regression tests |
| 1 done | `canto-u4vq` Approval classifier seam check | Verified host-owned shell classifiers plug in via `PolicyFunc` without Canto shipping heuristics |
| 2 done | `canto-p73h` Ion friction intake | Created the intake pattern for converting consumer findings into concrete Canto tasks |
| 2 done | `canto-3vjn` Naming consolidation | Stabilized approval/safety/hook/governor/audit names before any Ion migration depends on them |
| 2 done | `canto-5y3y` / `canto-87se` DSPy + GEPA review | Resolved to future explicit eval-trace/optimizer artifacts; no hot-path runtime prompt mutation |
| 2 done | `canto-q56s` Coding tool-surface audit | Removed preset helpers and glob/search aliases; kept shell configurable |
| 3 | `canto-khhl` Docs completeness pass | Fill only the docs needed for a new user to build a non-trivial agent and understand supported providers |
| 3 | `canto-2if9` First-alpha package contract | Publish the one-page alpha scope once blockers are named and validation is acceptable |

`canto-iusu` implemented reductions so far: memory prompt retrieval is now
`memory/memoryprompt.New`, not `prompt.MemoryPrompt`, so the core `prompt`
package no longer imports the optional memory subsystem. Typed tool authoring
is now `typedtool.New` / `typedtool.Must`, and approval-capable tools implement
`approval.RequirementProvider`, so the core `tool` package no longer imports
the approval state machine. Child skill validation, tool scoping, and prompt
preload now live behind `skill.RuntimeConfig`; `runtime.ChildSpec` accepts a
generic `agent.RuntimeConfig` and no longer imports `skill` or `agentskills`.
Artifact descriptors are now session-owned event references while artifact body
storage lives behind `artifact.StoreSessionArtifact`, so `session` no longer
imports `artifact`. Approval circuit-breaker prompt injection now lives in
`approval.CircuitBreakerGuard`, so budget/compaction-oriented `governor` no
longer imports approval. The governor review kept artifact-backed offload in
core because Ion uses `governor.CompactSession` for `/compact`, proactive
compaction, and overflow recovery, matching Pi's P1 context-governance class.
That review also fixed `MinKeepTurns` so offload and summarize retain complete
recent user turns rather than a raw message suffix. `agent.WithBudgetGuard`
now uses an agent-local cost guard and recognizes budget exhaustion through a
small marker interface; external `governor.NewBudgetGuard` processors still
settle as budget exhaustion, but the base agent no longer imports `governor`.
The root harness facade no longer owns concrete workspace/executor/safety
capabilities or capability-tool construction; opt-in workspace/executor tool
wiring now lives in `environmenttool`, and hosts can still register explicit
tools through `HarnessBuilder.Tools`. Approval audit logging now uses
approval-local audit events plus the opt-in `approvalaudit` adapter, so the
approval state machine stays available to `agent` without making base agent
imports pull in the generic `audit` package. Root `HarnessBuilder.Compaction`
and `Session.Compact` were removed as pre-alpha convenience APIs; compaction is
now an explicit host composition of `governor.CompactSession` with runtime
hooks, which matches Ion's current path and keeps the base `canto` import graph
free of `governor` and `artifact`. Root `HarnessBuilder.Approvals` was also
removed; hosts wire approval gates through `AgentOptions(agent.WithApprovalGate(...))`.
Final dependency audit found no remaining concrete P1 cleanup in
otherwise-core packages. `approval` remains a base typed-tool lifecycle
mechanism, `runtime.Bootstrap` intentionally uses `workspace` for explicit
workspace snapshots, `governor` intentionally keeps artifact-backed offload for
Ion's compaction/overflow path, and optional service/tool adapters remain
outside the base root/agent path.

### Deferred Or Conditional

These remain valid but should not block returning to Ion:

| Task | Disposition |
| :--- | :--- |
| `canto-3xay` | DESIGN.md pillar consolidation; pair with naming fallout if active docs drift |
| `canto-pc4b` | Add forked subagents only if Ion/runtime validation proves the current child-session model is insufficient |
| `canto-ic25` / `canto-mr13` | SOTA cadence and interrupt generalization; post-M1 unless a concrete blocker appears |

The initial authoring seam, typed service helper, coding-agent reference, core-vs-`x/` boundary cleanup, hello example, compaction hardening, `context/` -> `prompt` rename, cross-provider request transform, and pure turn-state extraction are already landed.

`canto-2vxb` produced and landed the pre-M1 harness direction: a `Harness`,
durable session handle, ordered run-event stream, and explicit environment
capabilities. That is now the authoring seam to preserve during M1 readiness.

Next selectable work after the Ion P1 kernel lane is Canto docs/release
posture plus any concrete framework issues returned from later Ion validation.
The session/turn rewrite, kernel reduction, and Ion import proof are done.

## Definition of Complete

"Complete" is not a single bar. Canto has three milestones, each with its own criteria:

### M1: Alpha (v0.0.1) — generally stable, usable

Framework is usable by one real consumer (Ion) end-to-end. No formal API-stability commitment yet — we work toward a generally stable codebase, fix issues as they surface, then release when it _feels_ stable.

- **Primitives:** All DESIGN.md pillars landed and load-bearing. (Done.)
- **DX:** Hello-agent ≤25 lines; declarative authoring surface has at least one implemented seam; the seam scales to Claude Code/Codex/Cursor-class coding-agent composition and external service/API tools. The no-credential hello path and reference coding agent are already landed; remaining DX work is cleanup and validation.
- **Consumer validation:** Ion validation is driven from the Ion repo. Before release, any Ion-discovered Canto framework issue must either be fixed here or explicitly deferred here; do not keep speculative Ion tasks in the Canto queue.
- **Docs:** README, examples, and per-package godoc are sufficient for a new user to build a non-trivial agent; at least one maintained Claude Code/Codex/Cursor-class reference coding/service-agent example is buildable without provider credentials.
- **Test coverage:** Load-bearing paths (agent loop, session replay, projection rebuild, workspace ops, tool execution, context mutation) covered by unit or integration tests.
- **Provider matrix:** Named providers explicitly supported; others explicitly deferred.
- **Release note:** `canto-2if9` ships a one-page "what this alpha is and isn't" note — no hard API-stability contract required.
- **Boundary hygiene:** Core vs `x/` placement is intentional; optional workspace and executor tools are stable capability packages, not a product-shaped coding package. The `coding/` promotion has been superseded by `executor/`, `workspacetool/`, and `executortool/`.
- **Typed turn input:** Root `Session.Submit` and `runtime.Runner.Send` use `llm.Prompt`; `Prompt`, `PromptStream`, `SendText`, and `SendTextStream` remain ergonomic text helpers.
- **Unified run-event envelope:** `RunEvent` carries session/turn/sequence,
  durability, usage, and lifecycle envelope metadata plus one typed payload
  (`RunChunkPayload`, `RunSessionPayload`, `RunRetryPayload`,
  `RunResultPayload`, or `RunErrorPayload`). Ion imports this in `9ff72a4`.
- **Typed tool authoring:** `typedtool.New` / `typedtool.Must` adapt typed Go
  handlers to the provider-facing JSON boundary; `service.New` remains the
  service/API retry layer.
- **Environment toolkit wiring:** `environmenttool.Tools` is the opt-in bridge
  from `Environment` workspace/executor capabilities to Canto workspace and
  executor tool modules.
- **Session maintenance facade:** root `canto.Session` exposes `Replay`,
  `EventsAfter`, `Snapshot`, `SnapshotIfNeeded`, and `Fork` so normal host
  maintenance does not reach into store/runtime internals. Compaction stays in
  `governor.CompactSession`.

### M2: Stable (v1.0) — compatibility commitment

(Not scoped during Phase 5. Entered once M1 ships and the codebase has demonstrated stability in practice.)

- API compatibility guarantee with a documented deprecation window.
- ≥2 independent consumers (Ion + another) or a public adoption signal.
- Provider support covers all actively-maintained frontier models.
- Performance characterized: benchmarks for turn latency, replay cost, projection rebuild cost, search index build cost.
- Security boundary audited: executor, secret injection, protected paths, policy engine.
- SOTA intake track has produced at least one landed-via-v1.0 upgrade demonstrating the intake works.

### M3: Mature (post-v1.0) — ecosystem signal

Not a canto-controlled milestone. Signal-only:

- Third-party extensions in `x/`-style modules.
- Non-Ion consumers in production.
- Competitive framework comparisons cite canto as a reference design.

The mature milestone is not a planned exit; it's a retrospective marker. Canto can be "done" without it.

### Current position

- M1 primitives: largely done. Remaining M1 work is docs, release posture, and any confirmed consumer-framework issues that come back from external validation.
- M1 blocker: `canto-2vxb` identified a small but real harness-facade cleanup
  before docs are worth polishing.
- M2: not yet planned. Enters scope once M1 ships.
- Phase 5 exit is the bridge from "primitives landed" to "M1 shippable."

### Phase 5 exit criteria

- **DX:** Hello-agent is ≤25 lines with no visible runtime wiring; advanced cases retain full composition with no lossy shortcuts forced. This is substantively landed and now needs consumer validation rather than another greenfield design pass.
- **DX:** Declarative agent/tool authoring surface has a landed design sketch and at least one implemented seam.
- **SOTA:** Existing framework research is treated as baseline; new reviews are delta-based and do not block Ion restart unless they identify a concrete M1 correctness or stability issue.
- **Consumer loop:** Ion migration happens outside this repo. Only confirmed Canto framework pain points become local tasks.
- **Research:** DSPy/GEPA implications are captured as Canto optimizer/eval-trace guidance, with no new pre-Ion runtime primitive required.
- **Release:** `canto-2if9` has either shipped or has explicit named blockers in its description.
- **Narrative:** `AGENTS.md`, `ai/DESIGN.md`, and `ai/README.md` consistently describe the same framework.

## Completed sprints (archive)

| Sprint                                                                            | Goal                                                              | Status                                  |
| :-------------------------------------------------------------------------------- | :---------------------------------------------------------------- | :-------------------------------------- |
| [01-core-loop-and-tool-runtime](sprints/01-core-loop-and-tool-runtime.md)         | Hardened agent loop and tool execution contract                   | complete                                |
| [02-durable-sessions-and-graphs](sprints/02-durable-sessions-and-graphs.md)       | Replay, idempotency, graph durability, wait-state recovery        | complete                                |
| [03-subagents-and-isolation](sprints/03-subagents-and-isolation.md)               | Spawning, delegation, isolation primitives                        | complete                                |
| [04-context-workspace-and-security](sprints/04-context-workspace-and-security.md) | Context budgeting, masking, validation, sandboxing, rebuild paths | complete                                |
| [05-memory-skills-and-retrieval](sprints/05-memory-skills-and-retrieval.md)       | Memory index/retrieval and skill progressive disclosure at scale  | complete                                |
| [06-eval-observability-and-alpha](sprints/06-eval-observability-and-alpha.md)     | Eval harnesses, telemetry, alpha release contract                 | complete except deferred alpha contract |

## Architecture-correction outcomes

- File/content identity is first-class: `workspace.WorkspaceFS`, `ContentRef`, file-read dedup, and search share one substrate.
- Session projections accelerate rebuilds alongside append-only replay.
- `runtime/` stays orchestration-only; workspace/security/memory compose into it.
- Secret injection, output compression, auto-mode classification, and cache-aware mutations attach at the correct boundaries.
- VFS memory reassembly via `workspace.MultiFS` and `memory.FS` is in place.
- Speculative VFS (`OverlayFS`) supports snapshot/restore/commit/discard for plan-mode workflows.

## Operating notes

- `p1` in `tk` means "on the alpha critical path", not "generally important".
- No API compatibility guarantees before v0.0.1. Replace whole surfaces when the architecture is better served.
- Product-specific or exploratory work stays off the alpha path until framework primitives support it.
