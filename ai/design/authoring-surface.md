---
date: 2026-04-21
summary: Phase 5 authoring surface design for coding/service agents on Canto
status: active
---

# Authoring Surface

## Decision

Canto should not add a monolithic `Quickstart()` or DSPy-shaped framework layer. The M1 authoring surface should be a small set of stable builders and bundles over existing primitives:

1. **Message helpers** in `session/` for the common role/message operations.
2. **Agent/runner builder** for default provider, store, session, registry, hooks, approvals, workspace, memory, and runtime wiring.
3. **Stable tool bundles** for coding-agent tools and service/API tools, with canonical tools moved out of experimental `x/` placement.
4. **Typed service/tool helpers** that preserve explicit approval, secret, audit, retry, and structured-output boundaries.
5. **A maintained reference coding/service agent** proving the stack without provider credentials.

The design goal is not a shorter demo for its own sake. The real test is whether a user can build a Claude Code/Codex/Cursor-class coding agent, or a service-acting agent with web/API/MCP tools, without manually stitching every Canto subsystem together.

## 2026-05-02 Harness Facade Gate

Flue, Pi, OpenAI Agents SDK, and Mendral all point at the same product lesson:
strong primitives are not enough if the headless harness boundary is hard to
see. Canto already has most of the mechanisms, but the root surface still reads
like "assemble an agent, then manually use `Runner`" instead of "open a durable
agent session and prompt it."

The next API target is a clean harness/session facade, not a compatibility
wrapper around the current shape. This is pre-alpha code; if the names are
wrong, replace them wholesale and update consumers.

### External Signals

| Reference | Useful signal for Canto | Non-goal |
| --- | --- | --- |
| Flue | `init(...) -> agent.session() -> session.prompt()/skill()/task()/shell()` is the right authoring shape for programmable headless agents. Sessions persist separately from agent runtime/sandbox scope. | Do not copy the TypeScript packaging model, Cloudflare assumptions, or virtual sandbox dependency. |
| Pi | Small core loop and few tools beat a sprawling control plane. The framework surface should make the common path obvious. | Do not reduce Canto to a coding-agent product or Pi clone. |
| OpenAI Agents SDK | `Agent` + `Runner` + tools + sessions + guardrail/handoff seams is the market vocabulary. Streaming, durable execution integrations, and HITL are first-class concepts. | Do not import Python SDK patterns or vendor-specific prompt/runtime objects into Canto's core API. |
| Mendral | Harness and sandbox are separate boundaries. Hosted/multi-user systems benefit when the harness owns credentials/durability and sandbox execution is narrow RPC/capability access. | Ion is a local single-user product for now; do not design a hosted control plane or remote sandbox protocol in M1. |

### Desired Public Shape

The author should be able to express the product-neutral harness in one path:

```go
h, err := canto.NewHarness(canto.HarnessSpec{
    ID:           "code",
    Instructions: "You are a coding agent.",
    Model:        "openai/gpt-5.5",
    Provider:     provider,
    Store:        store,
    Tools:        tools,
    Environment: canto.Environment{
        Workspace: root,
        Executor:  executor,
    },
    Approvals: approvalGate,
    Compaction: compactOptions,
})

sess, err := h.Session("project-123")
events, err := sess.PromptStream(ctx, "fix the failing tests")
```

Names can change during implementation, but the boundary should not:

- **Harness:** owns provider, model, prompt builder, registry, runtime runner,
  store, hooks, approval gate, compaction, and child/session lifecycle
  mechanisms.
- **Session handle:** owns one durable conversation ID and exposes
  `Prompt`, `PromptStream`, `Events`, `Cancel`, and later explicit
  approve/input methods. Hosts should not load sessions, wire writers, and
  call `Runner` directly for the common path.
- **Environment:** groups workspace, executor, sandbox, secrets, and
  bootstrap/context capabilities that tools may use. It is not product policy
  and it is not necessarily local filesystem access.
- **Run events:** one ordered stream for live model chunks, durable session
  events, tool lifecycle, approval/input waits, terminal state, and final
  result. Hosts should not merge an ad hoc chunk callback with a separate
  session watcher to reconstruct one user-facing turn.

### What Changes From The Current Surface

- Replace the root mental model of `App` with `Harness`. `App.Send` is close to
  the right convenience method, but the type name and field exposure encourage
  callers to treat Canto as a loose bag of primitives.
- Add a first-class session handle so common host code does not call
  `Runner.Watch`, then `Runner.SendStream`, then reconcile two channels.
- Keep direct access to `agent`, `runtime`, `session`, and `tool` packages for
  advanced composition, but do not make Ion use those internals for normal
  native turns.
- Keep tool bundles explicit. Canto may provide coding/service tool
  primitives, but Ion decides which tool catalog is visible and how approvals
  are presented.
- Keep sandboxing as an execution capability. Canto should model "where tool
  effects happen"; Ion or another host chooses local YOLO, local sandbox,
  remote sandbox, or no sandbox.

### Canto vs Ion Boundary

| Concern | Canto | Ion |
| --- | --- | --- |
| Durable session log, replay, projections | Owns | Consumes |
| Provider-visible request validity | Owns | Tests through live providers |
| Ordered run event stream | Owns | Renders |
| Tool lifecycle and result durability | Owns | Chooses visible display/output policy |
| Tool catalog and shell heuristics | Provides primitives | Owns defaults and UX |
| Approval state machine | Owns gate/events/resume seams | Owns copy, thresholds, delivery, mode policy |
| Workspace/sandbox capabilities | Defines interfaces/adapters | Chooses local policy and config |
| TUI/CLI commands, settings, queue, progress | Not involved | Owns |

### Refactor Slices

1. **Root harness rename/shape.** Replace `App` with a `Harness`-named root
   type and spec/builder API. No compatibility aliases. Keep the hello example
   green.
2. **Session handle.** Add `Harness.Session(id)` returning a small durable
   session facade with `Prompt` and non-streaming `Events` access. Move
   `Send`/`Run` convenience there.
3. **Unified stream.** Replace the callback-plus-watch common path with a
   single ordered run event iterator/channel. Include final result and terminal
   error in the stream.
4. **Environment grouping.** Introduce one product-neutral environment struct
   for workspace/executor/sandbox/secret/bootstrap capabilities. Keep it
   optional and explicit.
5. **Ion alignment.** Update Ion's `CantoBackend` to use the harness session
   facade. Ion should stop reconstructing Canto lifecycle semantics from raw
   packages when the facade provides them.

### Acceptance

- The hello example remains small and buildable without credentials.
- The reference coding/service agent uses the same harness/session path as Ion.
- A host can stream one turn from one API without manually merging stream
  callbacks and watcher events.
- Provider-visible history remains generated by Canto session/prompt
  primitives only.
- Ion's native backend becomes a product adapter over the harness facade, not a
  second runtime.

## Ground Truth

Much of the substrate is already implemented:

| Capability | Current primitive | State |
| --- | --- | --- |
| Durable sessions | `session.Store`, SQLite/JSONL, replay, projections, ancestry | Built |
| Runner/session lifecycle | `runtime.Runner`, `ChildRunner`, scheduler, lane queue | Built |
| Agent loop | `agent.BaseAgent`, step/turn, streaming, tool batching, turn stops | Built |
| Workspace safety | `workspace.Root`, `WorkspaceFS`, `ContentRef`, `OverlayFS`, `MultiFS` | Built |
| Coding tools | `x/tools` file/edit/shell/code/task/memory tools | Built, wrong stability surface |
| Service tools | `tool.Func`, `tool/mcp.Client`, `tool.Registry`, metadata | Built, needs DX and examples |
| Approval/HITL | `approval.Gate`, approval tools, wait events, audit | Built |
| Sandbox/secrets | `safety.Sandbox`, `SecretInjector`, `x/tools.Executor`, audit | Built |
| Memory/skills | `memory/`, `skill/`, memory VFS and memory tools | Built |
| Eval/tracing | `x/eval`, `tracing`, trajectory/cost metrics | Eval is extension-scoped; tracing is core because `agent/` uses it |
| Test provider | `x/testing.FauxProvider` | Built, needs example-facing path |

The main gap is authoring shape: users can reach the primitives, but they must know too much about package boundaries and wiring order.

## Target Agents

### Coding Agent

Comparable to Claude Code, Codex CLI, Cursor agent mode, opencode, or Ion:

- durable project sessions and resume
- streaming turn events for TUI/CLI/UI reconciliation
- read/list/edit/write tools scoped to a workspace
- shell execution with approvals, sandboxing, secrets, and audit
- codebase search/index hooks
- plan mode via `workspace.OverlayFS`
- memory/skills/context compaction
- subagents/delegation
- structured tool lifecycle events and idempotency
- eval/tracing export

### Service/API Agent

The same runtime should also support agents acting on external systems:

- HTTP/API tools
- web/search tools
- MCP tools
- credentials and secret injection
- approval tiers for external side effects
- structured outputs and typed validation
- retries and transient-error policies
- audit metadata for "who/what/where" actions
- test doubles and recorded fixtures

These should use the same tool, approval, audit, session, and eval boundaries as coding tools. Do not add product-specific integrations to core.

## Proposed Surface

### 1. Session Message Helpers

Add small helpers without hiding `llm.Message`.

```go
msg := session.UserMessage("fix the failing tests")
event := session.NewUserMessage(sess.ID(), "fix the failing tests")
err := sess.AppendUser(ctx, "fix the failing tests")
```

Also useful:

```go
session.SystemMessage(text)
session.AssistantMessage(text)
session.ToolMessage(name, id, output)
```

Rules:

- Helpers only cover common cases.
- `session.NewMessage(sessionID, llm.Message{...})` remains the escape hatch.
- Helpers must not erase role, tool-call, cache, reasoning, or provider-specific fields.

### 2. Harness/Session Facade

Add a harness builder that produces the existing primitives, then make the
durable session handle the common execution path.

```go
h, err := canto.NewHarness("code").
    Instructions("You are a coding agent.").
    Model("gpt-4o").
    Provider(providers.OpenAI()).
    SessionStore(store).
    Workspace(root).
    Tools(coding.Tools(root), service.Tools(...)).
    Approvals(approvalManager).
    Memory(memoryManager).
    Build()

res, err := h.Session("proj").Prompt(ctx, "fix the failing tests")
```

Sketch types:

```go
type Harness struct {
    Agent  agent.Agent
    Runner *runtime.Runner
    Tools  *tool.Registry
    Store  session.Store
}

type Session struct {
    // durable session handle
}
```

The harness should expose:

- `Session(id)` for common prompt/run/event access
- `Agent`, `Runner`, `Tools`, and `Store` for advanced composition
- options for custom `prompt.RequestProcessor`, hooks, approval gate, memory, skills, and workspace
- no hidden global provider or process-wide mutable state

Use `h.Session(id).Prompt` for simple turns. Advanced users still call
`agent.New`, `session.New`, `runtime.Runner`, and `Turn` directly.

### 3. Stable Tool Bundles

Canonical coding tools should not remain in an experimental namespace. `canto-l2iy` should classify:

| Tool group | Proposed home | Notes |
| --- | --- | --- |
| file read/list/write | core stable package | Basic rooted workspace access |
| edit/multiedit/apply patch | core stable package | Required for coding agents |
| shell/command executor | core stable package | Must be shell-configurable and keep approval/sandbox/secret boundaries explicit |
| task/memory helpers | decide case-by-case | Stable if generic, otherwise extension |
| experimental swarm/pool/redis | keep `x/` or cut | Not needed for M1 authoring |

The user-facing shape should be bundle-oriented:

```go
reg := tool.NewRegistry()
coding.Register(reg, coding.Config{
    Workspace: root,
    Executor: executor,
    Approvals: approvals,
})
```

or:

```go
tools := coding.Tools(root, coding.WithExecutor(executor))
```

The bundle must not make policy decisions silently. Dangerous tools expose `ApprovalRequirement`, sandbox options, audit metadata, and secret names.

### 4. Service/API Tool Helpers

`tool.Func` is useful but too raw for service tools that need schemas, typed args/results, retries, credentials, and approval metadata.

Add a typed helper layer:

```go
weather := service.Tool[WeatherArgs, WeatherResult]{
    Name: "get_weather",
    Description: "Fetch current weather for a city.",
    Schema: schema.From[WeatherArgs](),
    Execute: func(ctx context.Context, deps service.Deps, args WeatherArgs) (WeatherResult, error) {
        return deps.HTTP.GetWeather(ctx, args.City)
    },
    Approval: service.ReadOnly("weather", "city"),
}
```

Design requirements:

- Tool still implements `tool.Tool` / `tool.ApprovalTool`.
- Structured result is encoded consistently.
- Secrets are injected through `safety.SecretInjector`, not environment leaks.
- HTTP/API retries are opt-in and visible.
- Audit metadata records service, operation, resource, side-effect category, and secret count.
- MCP-discovered tools can be wrapped with the same approval/file-policy metadata path.

Do not add built-in product integrations like GitHub/Jira/Slack in core for M1. Provide the pattern and adapters.

### 5. Typed Task/Result Contract

Typed tasks are useful, but should not block M1. Treat this as a later seam unless it falls out naturally from service tool helpers.

Possible shape:

```go
type Task[I any, O any] struct {
    Name string
    Instructions string
    Strategy Strategy
}

result, err := task.Run(ctx, app, input)
```

This is the transferable part of DSPy/PydanticAI: typed task contracts and structured outputs. Do not adopt DSPy's optimizer-first programming model as the main authoring interface.

## First Implementation Cut

`canto-gymf` should implement the smallest coherent seam:

1. Add `session` message helpers:
   - `UserMessage`
   - `NewUserMessage`
   - `AppendUser`
2. Add or expose a no-credential `FauxProvider` path suitable for examples.
3. Add an authoring builder or package-level constructor that produces:
   - `agent.Agent`
   - `runtime.Runner`
   - default empty registry
   - default in-memory or JSONL store
4. Keep all advanced hooks/options available.
5. Update or add a buildable hello example that hits ≤25 lines.

This seam should be enough to prove the shape without forcing tool migration, typed tasks, or optimizer artifacts into the first patch.

## Follow-On Cuts

### `canto-l2iy`: Tool Placement

Resolve stable package boundaries for coding tools. This should happen before the reference agent becomes the docs anchor.

### `canto-umuc`: Service/API Tool Surface

Design and implement typed service/API helper patterns for HTTP/API/web/MCP tools. This can share schema generation with structured output work.

Status 2026-04-22:

- First code cut landed in `service/`.
- `service.New[A, R]` adapts typed args/results to `tool.Tool`, infers JSON Schema with `jsonschema-go`, emits JSON results, and supports metadata, approval hooks, and retry policy.
- `service.Requirement`, `ReadOnly`, `Mutation`, and `Execution` provide typed approval helpers over existing `approval.Requirement` and `safety.Category` boundaries.
- `examples/service-agent` proves the helper through the root `canto.NewHarness` builder, durable SQLite store, faux model tool call, and real runner turn.
- Remaining validation belongs in `canto-43vh`: use the helper in a runnable coding/service reference agent with web/API fixture tools, MCP wrapping, secrets policy, and approval flow.

### `canto-43vh`: Reference Agent

Build one maintained reference coding/service agent with FauxProvider fixtures:

- streaming event feed
- durable session store
- workspace file/edit/shell tools
- approval/sandbox path
- stubbed service/API or web-search tool
- MCP adapter path if feasible
- resume flow
- codebase search placeholder

This is the real M1 proof, not the hello example.

Status 2026-04-22:

- `examples/codeagent` is now buildable and no-credential.
- The reference uses `canto.NewHarness`, explicit `SessionStore`, `llm.FauxProvider`, `runtime.Runner.Watch`, durable SQLite sessions, workspace read/edit tools, workspace-scoped shell, Python code execution, typed `service.New` web-search fixture, approval manager with safety policy, hooks, and same-session resume.
- `coding.ShellTool` now accepts `Dir` and shell configuration so execution can be pinned to the workspace without assuming bash.
- Canonical coding tools moved from `x/tools` to stable `coding/`; `examples/codeagent` now imports the stable package.
- Remaining M1 reference gaps: MCP wrapping in the reference path, docs/README anchor, and Ion consumption pass.

### `canto-p73h` / `canto-m4nb`: Ion Validation

Ion should become the consumer truth source. Every workaround becomes:

- fixed in Canto,
- explicitly deferred,
- or documented as Ion policy outside Canto.

## Non-Goals

- No hosted platform abstraction.
- No product-specific Ion policy in Canto.
- No hidden global environment/provider registry.
- No DSPy clone.
- No forced typed-task model for every agent.
- No compatibility shims before v0.0.1.
- No service-specific integrations in core M1.

## Open Questions

1. Should the builder live at repo root package `canto` or under `runtime/authoring`?
   - Bias: root package if it is only a composition layer; otherwise `runtime/authoring`.
2. Should `FauxProvider` move from `x/testing` to `llm/testing` or stay extension-only?
   - Bias: provide a stable test/example package because buildable examples need it.
3. Should typed service tools rely on `invopop/jsonschema` directly?
   - Bias: yes, if the project already uses it; keep the dependency behind helper APIs.
4. How much of `x/tools` should move before M1?
   - Bias: file/edit/shell/code-search canonical tools need stable import paths; speculative tools can stay in `x/` or be cut.
5. Should web/search be a built-in generic tool or only an example pattern?
   - Bias: example/stub first; stable generic HTTP/search helpers only after Ion/reference-agent validation.

## Success Criteria

- Hello agent is ≤25 lines without visible runtime wiring.
- A maintained reference coding/service agent builds without provider credentials.
- Canonical coding tools have stable import paths and clear approval/sandbox semantics.
- Service/API tools can be authored with typed args/results, secret injection, approvals, retries, and audit metadata.
- Advanced users can still compose `agent.New`, `runtime.Runner`, `session.Store`, `tool.Registry`, and processors directly.
