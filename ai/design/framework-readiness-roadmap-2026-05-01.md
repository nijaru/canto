---
date: 2026-05-01
summary: Canto roadmap, currently blocked behind Ion-driven P1 primitive audit
status: active
---

# Canto Roadmap: Framework Readiness After Ion

## Answer First

Canto should stay a general-purpose Go agent framework, but this roadmap is
not the active lane while Ion's P1 primitive audit is reopened. The current
sequence is:

1. **Reopen C0 for primitive evidence.** Ion's prior full-wrapper pass is
   historical evidence, not closure. If Ion cannot use Canto primitives cleanly
   for the Pi-level loop, classify and fix the Canto primitive first.
2. **When C0 closes again, do M1 framework readiness.** Public API, docs,
   examples, package boundaries, and authoring ergonomics come before new
   optional primitives.
3. **Treat SOTA features as extension tracks.** DSPy/GEPA, sandbox/filesystem, multi-agent, skills, MCP expansion, memory, and routing should be explicit opt-in packages or examples until they prove they belong in core.

## Roadmap

| Phase | Name | Goal | Work |
| --- | --- | --- | --- |
| C0 | Ion primitive acceptance gate | Reopened. | Classify Ion ideal-first gaps as Canto primitive, Ion product policy, temporary Ion-local glue with re-extraction, or rejected/non-P1; import Canto revisions into Ion and verify there. |
| C1 | M1 framework readiness | Blocked on C0. | README, godoc, examples, provider docs, alpha scope note, harness facade and typed tool examples. |
| C2 | API/DX simplification | Remove awkward or duplicated authoring paths revealed by examples. | Public API audit, core vs `x/` pass, fewer constructors, clearer package names, no compatibility shims before v0.0.1. |
| C3 | Workspace/sandbox track | Evaluate workspace/versioning/sandbox primitives after local Ion is stable. | WorkspaceFS refinement, OverlayFS/branch semantics, sandbox adapter seams, optional external storage adapters. |
| C4 | Eval/optimizer track | Make optimization possible without making runtime dynamic. | Stable trajectory artifacts, textual feedback fields, candidate prompt/tool/config artifacts, DSPy/GEPA adapters under `x/eval` or `x/optimize`. |
| C5 | Multi-agent/extensibility track | Add higher-level orchestration only after the solo loop and authoring surface are clear. | Subagents, skills, MCP, handoffs, policy manifests, tracing views, examples. |

## Research Triage

| Reference | Canto Lesson | Disposition |
| --- | --- | --- |
| Vercel AI SDK | A good agent loop is small: model step, tool call, observation, repeat with explicit stop conditions. | Keep Canto's core loop similarly small and visible. |
| LangGraph | Durable execution requires deterministic/idempotent replay and persisted side-effect boundaries. | Use as the correctness bar for runtime/session recovery. |
| PydanticAI | Typed deps/tools/outputs are a DX advantage. | Consider typed task/signature helpers after C1 examples expose friction. |
| OpenAI Agents SDK | Market direction is converging on MCP, skills, sandbox-aware workspaces, snapshots/rehydration, tracing. | Track, but do not expand Canto core before Ion primitive acceptance. |
| OpenHands SDK | Older coding-agent SDK precedent for workspaces, tools, sandbox, context compression, and model-agnostic execution. | Historical/supporting input only; do not treat as a current primary reference. |
| CrewAI / Mastra / AutoGen / BeeAI | Useful multi-agent/workflow/enterprise patterns. | Extension-track input, not core-loop guidance. |
| DSPy / GEPA | Optimizers need stable signatures/modules/adapters and rich eval traces with textual feedback. | Future offline optimizer artifacts only; no hot-path prompt mutation. |
| Mesa | Versioned POSIX filesystem for agents: branch, review, rollback, sparse materialization, access control. | Future workspace/storage adapter reference. |
| Archil | Elastic POSIX-like agent disks with shared mounts and serverless execution. | Future sandbox/storage reference; likely external adapter, not core dependency. |
| BranchFS / branch context | Copy-on-write workspace and process isolation with commit/abort semantics. | Future local sandbox/branch model reference. |
| foldb | Deterministic state machine database: log is truth, state is `fold(log)`, no wall-clock/RNG in fold. | Reinforces Canto's event log/projection invariants and test strategy. |

## Cross-Repo Contract

- Canto owns durable event semantics, provider-visible history, prompt/runtime boundaries, tool lifecycle, retry/cancel/terminal states, compaction primitives, workspace abstractions, and reusable tracing/eval hooks.
- Ion owns terminal UX, coding-tool mix, settings/config UX, trust/mode policy, provider/model workflow, local command catalog, and product defaults.
- If every serious host would have to reimplement a state machine or replay invariant, move it into Canto.
- If the choice expresses user workflow, terminal taste, shell heuristics, or coding-agent defaults, keep it in Ion.

## Immediate Next Work

1. Run the reopened Ion primitive audit before C1.
2. Do not add new ready Canto tasks from research alone.
3. Do not start C1 docs/examples/API readiness until Ion P1 primitive
   acceptance closes again.
4. Keep C3-C5 as explicit later tracks so SOTA work remains planned but cannot jump ahead of the M1 framework readiness gate.

## Sources Checked

- Vercel AI SDK `ToolLoopAgent` docs — minimal reusable loop with streaming, tools, and stopping conditions.
- LangGraph durable execution docs — persistence, replay, interrupt, and idempotency reference.
- OpenAI Agents SDK update — sandbox-aware Codex-like tools, MCP, skills, snapshots/rehydration, memory, tracing.
- OpenHands SDK docs/blog — historical coding-agent SDK reference; supporting evidence only.
- DSPy and GEPA docs — task signatures/modules/adapters and offline prompt/config/tool-description optimization.
- Mesa filesystem blog — versioned POSIX filesystem for agent workloads.
- Archil docs — elastic shared disks, checkout/checkin ownership, serverless execution, object-store backing.
- BranchFS / branch context papers — OS-level fork/explore/commit/rollback primitives.
- `jeremytregunna/foldb` local checkout — deterministic `fold(log)` state model and invariant documentation.
