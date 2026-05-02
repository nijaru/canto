---
date: 2026-04-12
summary: Unified framework architecture for Canto after the identity-first workspace, projection, and search correction.
status: active
---

# Canto Framework Architecture

## 1. Positioning: The Mechanism Layer

Canto is the primitives layer for building durable agent backends. It sits _below_ agent applications — closer to `net/http` than `gin`. Canto provides the mechanism (durable state, execution semantics, tools, graphs); applications like Ion provide the policy (UX, planner logic, approval prompts).

The test: if you can build an overnight autoresearch loop, a long-horizon SWE agent, a Claude Code/Codex/Cursor-class coding agent, or an agent that safely acts across external services/APIs using these primitives, the framework is correct.

## 2. Core Architectural Pillars (Synthesized from SOTA)

### 2.0. Headless Harness Facade

Canto has strong primitives, but the product-level lesson from Flue, Pi,
OpenAI Agents SDK, and harness/sandbox architecture work is that app authors
need one obvious programmable harness path before they need every primitive.
The facade should make this path natural:

```text
init/runtime -> agent -> session -> prompt/skill/task/shell -> events/store
```

This is not a TUI, CLI, or coding-agent preset. It is a framework-owned
composition surface that exposes sessions, event durability, tool lifecycle,
session environments, scoped commands/tools, compaction, and interrupts without
making hosts wire every package manually. Product policy stays in Ion or other
hosts.

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
| **L3**  | `tool/`, `skill/`, `coding/`                           | Tool registry/execution metadata, stable coding-agent tools, MCP integration, skill routing  |
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
- **Stable coding surface:** `coding/` holds individual coding-agent primitives: workspace read/write/list, exact edit/multiedit, configurable shell execution, Python code execution, executor/output boundaries, and search-tool aliasing. It does not ship tool presets or built-in glob/grep tools.
- **Extension (under review):** `x/tools/` now only holds memory/task/WASM helpers. These remain extension-scoped until Ion/reference-agent usage proves they belong in a stable package.

## 7. Phase 5 Frontier

The architecture-correction tranche is shipped. Canto stays a general-purpose primitives layer; the Codex/Claude Code/Pi/Cursor-class coding/service agent is Phase 5's validating target because it exercises the full surface, not because it defines scope. Primitives are designed generally, validated against that target, and rejected if they only make sense for coding agents. Ion is one instance of that class; its consumption pass validates and amends the shape but does not gate or narrow it. See `ai/PLAN.md` for the live frontier and exit criteria.

The current delta is `canto-2vxb`: review the authoring/runtime surface against
Flue/Pi/OpenAI/Mendral and decide whether the existing `canto.NewAgent` builder,
`runtime.Runner`, `SessionEnv`-like workspace/sandbox capabilities, and
interrupt primitives need a cleaner harness facade before M1 docs/release work.
