---
date: 2026-04-12
summary: Unified framework architecture for Canto after the identity-first workspace, projection, and search correction.
status: active
---

# Canto Framework Architecture

## 1. Positioning: The Mechanism Layer

Canto is the primitives layer for building durable agent backends. It sits _below_ agent applications — closer to `net/http` than `gin`. Canto provides the mechanism (durable state, execution semantics, tools, graphs); applications like Ion provide the policy (UX, planner logic, approval prompts).

The test: if you can build an overnight autoresearch loop, a long-horizon SWE agent, a Claude Code/Codex/Cursor-class coding agent, or an agent that safely acts across external services/APIs using these primitives, the framework is correct.

## 1.1. Reference Discipline

Ion is the active first-party consumer and a first-class product built on
Canto, not merely a test harness. Use local Pi as the primary regression
control for Ion's phase-1 core-agent internals, and Claude Code plus Codex as
secondary long-term product references.
Agent products such as Amp, Droid, Crush, OpenCode, Gemini CLI, Copilot CLI,
Cursor, Zed, Factory Droid, Jules, Slate-style swarms, mem0, Letta, and similar
systems are evidence for specific UX or workflow questions, not product
checklists.

Canto is an agent framework, so framework and SDK references remain relevant:
OpenAI Agents SDK, Anthropic/Claude Agent SDK, MCP, A2A, Google ADK, Microsoft
Agent Framework, Semantic Kernel, AutoGen, LangGraph/LangChain, Pydantic AI,
LlamaIndex, CrewAI, Agno, Mastra, Vercel AI SDK, BeeAI, Letta, Flue,
DSPy/GEPA, and Temporal/DBOS/Inngest-style durable execution systems can inform
primitives such as sessions, tool loops, approvals, checkpoints, hooks, tracing,
evals, memory, and extension packaging. The map is intentionally
non-exhaustive and current-state dependent. Reviews and fit are mixed across
those frameworks, so extract small testable patterns instead of adopting their
architectures.

Recent academic/SOTA paper scans should be delta-based. They are deferred until
a phase-2 research lane is selected, unless a concrete framework defect needs
current research to resolve it.

## 2. Core Architectural Pillars (Synthesized from SOTA)

### 2.0. Headless Harness Facade

Canto has strong primitives, but the product/framework reference lesson is
that app authors need one obvious programmable harness path before they need
every primitive. The facade should make this path natural:

```text
harness -> session -> prompt/skill/task/shell -> ordered run events -> store
```

This is not a TUI, CLI, or coding-agent preset. It is a framework-owned
composition surface that exposes sessions, event durability, tool lifecycle,
session environments, scoped commands/tools, compaction, and interrupts without
making hosts wire every package manually. Product policy stays in Ion or other
hosts.

Target shape:

- `Harness` owns provider/model, agent, prompt builder, registry, runtime
  runner, store, hooks, approval gate, compaction, and child/session lifecycle
  mechanisms.
- `Harness.Session(id)` returns the common host-facing durable conversation
  handle. Normal hosts prompt through that handle; direct `runtime.Runner` and
  `agent.Turn` use remains the advanced escape hatch.
- `Session.PromptStream` exposes one ordered run-event stream for model chunks,
  durable lifecycle events, tool starts/results, approval/input waits, terminal
  state, and final result. Hosts should not merge a chunk callback and watcher
  channel to reconstruct one turn.
- `RunEvent.Lifecycle` and `RunEvent.Usage` project generic framework state
  directly: usage deltas/cumulative usage, tool status and active tool
  snapshots, compaction start/completion, retry, cancellation, and terminal
  settlement.
- `Environment` groups workspace, executor, sandbox, secret injection, and
  bootstrap/context capabilities. It describes where effects happen; it does
  not encode product policy.

Canto should rename/refactor the current root `App`/`Runner`-first authoring
surface into this harness/session path before Ion aligns `CantoBackend` again.
This is a pre-alpha clean break, not a compatibility layer.

### 2.1. Session Durability & Event Sourcing (Topic 5)

- **Append-Only Event Log:** Foundational state model. Never mutate, ever. Backed by JSONL (local) or SQLite (FTS5) for production.
- **Replay + Projections:** `session.Replayer` is the source of truth for rebuilding in-memory sessions, while projection snapshots accelerate rebuilds without weakening append-only durability.
- **Time-Travel & Branching:** Support for session rewind, persisted branching, and checkpoint-restore.
- **Cross-Host Transports:** Unified `session.Store` interface abstracting local and cloud syncs.

### 2.2. Context Engineering & Memory (Topics 1, 4)

- **Budget-Aware Execution:** `BudgetGuard` evaluates remaining context capacity _before_ observation loading.
- **Tiered Compaction:** Offloader (reversible deletion) → Observation Masking (cheap) → Full LLM Summarization (last resort).
- **Index-Layer Memory:** Progressive disclosure of long-term facts; `memory.CoreStore` bridging vector/FTS5 indexes.

### 2.3. Workspace, File Identity & Search (Topics 10, 13)

- **Rooted Filesystem Capability:** `workspace.Root` and `workspace.Validator` define the trusted path boundary.
- **Identity-First Reads:** `workspace.WorkspaceFS` and `ContentRef` separate file identity from materialization, while durable file-reference records keep repeated reads from inflating context.
- **Search On The Same Substrate:** Workspace indexing and file-read dedup should build on stable content identity instead of bypassing the filesystem boundary.

### 2.4. Tool Execution & Skills (Topics 2, 7)

- **Deferred Loading:** Present `search_tools` meta-tool when > 20 tools are registered to save 85% prompt tokens.
- **Progressive Disclosure Skills:** L1 (description) -> L2 (schema) -> L3 (execution). `agentskills.io` standard (YAML+MD).
- **Self-Extension:** Framework provides `manage_skill` tool primitives for RL-style self-improvement.

### 2.5. Agent Loop, Subagents & Graphs (Topics 3, 6, 9)

- **Two-Layer Orchestration:** Outer durable execution (Temporal-style DAG routing) + Inner agent logic layer (`LoopNodes`).
- **Parent/Child Runtime:** Isolated child sessions. Delegation via Sync, Async, or Scheduled APIs. Outputs are summaries + artifact refs.
- **Programmatic Control:** Graph routing and task assignment are Go code; LLMs decide behavior within a turn.

### 2.6. Governance, Observability & HITL (Topics 10, 11, 12, 13, 14)

All of these share the pattern "attach at the boundary, stay cycle-free, opt-in":

- **Hook-Based Governance:** Policy hooks are explicit seams; audit and safety stay as separate concerns.
- **Execution-Boundary Security:** Sandbox wrapping, env sanitization, secret injection, and protected-path checks attach at executor/file boundaries, not across callers.
- **Policy Engine:** Deterministic rule evaluation via composable `approval.PolicyFunc` chains.
- **Structured Audit Trail:** `audit/` is an append-only sink reused by approval, safety, protected-path, env sanitization, and sandbox wrapping.
- **OTel GenAI Semantic Conventions:** Hierarchical traces (Session → Turn → LLM → Tool). Privacy-first (no prompts by default).
- **Tiered Approval & Auto Mode:** `WaitState` exposes interruptions; LLM-as-judge classifiers plug in; breaker state is visible to the request builder.
- **Observation Masking:** Cost optimization by partial truncation of tool outputs in the context stream.

### 2.7. Evaluation & Benchmarking (Topic 8)

- **Trajectory-Level Eval:** Moving beyond pass@1 to Reliability Metrics (GDS, VAF, MOP) and repeated-run summaries.
- **Harness Integration:** Framework exports deterministic trajectory logs for SWE-bench Pro / Harbor compatibility.

## 3. Package Boundaries

| Layer   | Packages                                               | Responsibility                                                                               |
| ------- | ------------------------------------------------------ | -------------------------------------------------------------------------------------------- |
| **L1**  | `llm/`                                                 | Provider interface, message types, streaming, cost, and usage/tracing hooks                  |
| **L2**  | `agent/`                                               | Turn loop, turn-stop semantics, step aggregation, and tool orchestration                     |
| **L3**  | `session/`                                             | Append-only event log, replay, projections, branching, SQLite/JSONL stores                   |
| **L3**  | `prompt/`                                              | Request shaping, budget checks, compaction, observation masking                              |
| **L3**  | `tool/`, `skill/`, `executor/`, `workspacetool/`, `executortool/` | Tool registry/execution metadata, host execution, optional capability tools, MCP integration, skill routing |
| **L3**  | `workspace/`                                           | Rooted filesystem boundary, validation, `WorkspaceFS`, `ContentRef`, indexing substrate      |
| **L3**  | `runtime/`                                             | Orchestration, child lifecycle, scheduling, bootstrap sequencing                             |
| **L3**  | `memory/`                                              | Repository/index/retrieval layers and vector-backed recall                                   |
| **L3**  | `approval/`, `safety/`, `hook/`, `audit/`, `governor/`, `tracing/` | Policy, execution safety, middleware hooks, audit/tracing, and prompt/execution guards |
| **Ext** | `x/graph/`                                             | DAG nodes, loop cycles, parallel execution                                                   |
| **Ext** | `x/eval/`                                              | Deterministic task specs, benchmark connectors, repeat runners, scorers, reliability metrics |

## 4. Dependency Graph

```text
x/graph, x/eval
        ↓
     runtime/
        ↓
agent/, prompt/, tool/, skill/, workspace/, memory/, approval/, safety/, hook/, audit/, governor/, tracing/
        ↓
  session/, llm/
```

## 5. Current Design Corrections

The current load-bearing corrections have been implemented:

- **Identity-first workspace/files:** `workspace.WorkspaceFS`, `ContentRef`, file-read dedup, and search indexing share one substrate. Complete.
- **Replay plus projections:** `session.Replayer` remains canonical, but projections/snapshots accelerate rebuilds without becoming mutable truth. Complete.
- **Narrow runtime:** `runtime/` composes orchestration primitives; it does not own filesystem or security internals. In force.
- **Execution-boundary security:** secret injection attaches at the executor boundary after env sanitization. Complete.
- **Cache-aware context mutations:** prompt cache alignment boundaries are structurally predictable; mutations batch within the suffix. Complete.
- **VFS memory reassembly:** `workspace.MultiFS` and `memory.FS` expose semantic memory as virtual filesystem paths. Complete.

## 6. Core vs `x/` Boundary

`x/` exists for orthogonal concerns that a minimal agent could omit without losing core meaning. The test: if removing the package breaks the "hello agent" path, it belongs in core, not `x/`.

- **Core (retain):** `agent/`, `llm/`, `session/`, `prompt/`, `tool/`, `skill/`, `workspace/`, `runtime/`, `memory/`, `approval/`, `safety/`, `hook/`, `audit/`, `governor/`, `tracing/`.
- **Extension (retain in `x/`):** `x/graph/` (DAG topologies), `x/eval/` (benchmarks/scorers).
- **Stable capability tools:** `executor/` owns bounded host command execution, `workspacetool/` owns rooted workspace read/write/list/exact-edit tools, and `executortool/` owns shell/code-execution tool adapters. Canto does not expose a product-shaped coding package, tool presets, model-visible `multi_edit`, or built-in glob/grep tools.
- **Extension (under review):** `x/tools/` now only holds memory/task/WASM helpers. These remain extension-scoped until Ion/reference-agent usage proves they belong in a stable package.

## 7. Phase 5 Frontier

The architecture-correction tranche is shipped. Canto stays a general-purpose primitives layer; the Codex/Claude Code/Pi/Cursor-class coding/service agent is Phase 5's validating target because it exercises the full surface, not because it defines scope. Primitives are designed generally, validated against that target, and rejected if they only make sense for coding agents. Ion is one instance of that class; its consumption pass validates and amends the shape but does not gate or narrow it. See `ai/PLAN.md` for the live frontier and exit criteria.

The current delta from `canto-2vxb` is that a cleaner harness/session facade is
needed before M1 docs/release work and before Ion's next runtime-boundary
refactor. See `ai/design/authoring-surface.md` for the concrete refactor
slices.
