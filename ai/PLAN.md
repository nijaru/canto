# Plan

Sprints 01-06 delivered the load-bearing primitives on a dependency-ordered axis. The Phase 4 architecture correction (identity-first workspace, projections, execution-boundary security, cache-aware mutations, VFS reassembly) landed on top. Phase 5 changes rhythm.

## Phase 5: SOTA + DX

Two tracks, run in parallel, reinforcing each other:

- **SOTA track.** Continue the "research → synthesize → implement" discipline that produced sprints 01-06 and the Phase 4 correction. Monitor CC/Pi/Codex and academic sources for new patterns worth landing. New primitives keep arriving; canto's edge depends on landing them.
- **DX track.** Design a declarative authoring surface so an app author writes a working agent in ≤25 lines with no visible runtime wiring — and still drops down to full composition for advanced cases. The SOTA primitives are useless if users can't reach them ergonomically.

The tracks reinforce each other: a SOTA pattern that cannot be expressed cleanly through the authoring surface is a design signal, not an API problem. DX work surfaces which primitive shapes are wrong. SOTA work surfaces which ergonomic shortcuts are lossy.

Plan entries under either track need a named motivation — a SOTA source or a consumer/friction source — before landing on the frontier.

Canto remains a general-purpose primitives layer. It is not a coding-agent product, but the Codex/Claude Code/Pi/Cursor-class coding agent is a well-specified validating target that exercises the full primitives surface: durable sessions, workspace identity, tool runtime, compaction, approval, subagents, memory, and provider normalization. Ion is one host in that class. The rule: design primitives generally, validate them against that target, reject shapes that only make sense for Ion or coding agents. Ion validates Canto; it does not define Canto's scope.

The Canto/Ion split is:

- **Canto:** mechanism. Durable event log, replay/projections, prompt construction, context mutation, tool execution metadata, workspace/file capability, approval wait-state and policy seams, provider transforms, service-tool helpers, and examples/reference agents.
- **Ion:** policy and product. Terminal UX, command palette, planner/task behavior, approval copy and delivery, default shell classifier heuristics, memory aggressiveness, model choice defaults, and user workflow.
- **Shared boundary:** if every serious host would need to reimplement state-machine or durability plumbing, it belongs in Canto. If the choice expresses taste, UX, environment assumptions, or Ion's workflow, it belongs in Ion and plugs into a Canto seam.

The next-phase roadmap lives in [design/framework-readiness-roadmap-2026-05-01.md](design/framework-readiness-roadmap-2026-05-01.md). Use it to keep work ordered: C0 Ion acceptance, C1 M1 docs/examples/API readiness, C2 API/DX simplification, C3 workspace/sandbox, C4 eval/optimizer artifacts, C5 multi-agent/extensibility. Do not let C3-C5 research jump ahead of C0-C1.

Existing research already covers LangGraph, PydanticAI, AutoGen, Vercel AI SDK, provider SDKs, HITL, durability, graph workflows, tool orchestration, context engineering, memory, and security. Do not restart a broad framework survey unless a concrete gap appears. DSPy and GEPA deserve deeper reviews because neither was previously covered at the same depth and they target the optimization surface (signatures/modules/compilers vs. reflective prompt evolution). They are inputs among several, not the center of the design; pair them so the reviews inform each other.

2026-05-01 delta: OpenAI Agents SDK, OpenHands SDK, Mesa, Archil, BranchFS, and foldb reinforce the same direction: durable state, versioned/isolated workspaces, snapshots/rehydration, sandbox/process boundaries, and rich traces matter. They do not change the immediate order. Canto should first finish M1 readiness after Ion stabilizes; external workspace/storage systems are future adapters or design references, not core dependencies.

### Active Canto Stabilization Roadmap

The near-term goal is a targeted harness-facade review, not another broad
survey. Flue's headless runtime shape, Pi's small core, OpenAI's model-native
harness/sandbox direction, and Mendral's harness-outside-sandbox argument are
concrete enough to re-check Canto's M1 authoring surface before more Ion
runtime refactors. Keep the queue focused on Canto-owned framework work; bring
Ion findings back only when they identify a concrete Canto issue.

| Gate | Task | Intent |
| :--- | :--- | :--- |
| 0 active | `canto-2vxb` Flue/Pi harness facade review | Decide the concrete Canto headless harness facade and refactor targets before Ion aligns `CantoBackend` to it |
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

### Deferred Or Conditional

These remain valid but should not block returning to Ion:

| Task | Disposition |
| :--- | :--- |
| `canto-3xay` | DESIGN.md pillar consolidation; pair with naming fallout if active docs drift |
| `canto-pc4b` | Add forked subagents only if Ion/runtime validation proves the current child-session model is insufficient |
| `canto-ic25` / `canto-mr13` | SOTA cadence and interrupt generalization; post-M1 unless a concrete blocker appears |

The initial authoring seam, typed service helper, coding-agent reference, core-vs-`x/` boundary cleanup, hello example, compaction hardening, `context/` -> `prompt` rename, cross-provider request transform, and pure turn-state extraction are already landed. Remaining work in this repo is Canto docs/release posture plus any concrete framework issues returned from consumer validation.

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
- **Boundary hygiene:** Core vs `x/` placement is intentional; canonical coding-agent tools are not stranded behind an experimental namespace. The `coding/` promotion is landed.

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
- New M1 blocker candidate: `canto-2vxb` may identify a small harness-facade
  cleanup before docs are worth polishing.
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
