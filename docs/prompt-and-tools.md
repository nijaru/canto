# Prompts and Tools

Canto keeps agent behavior in the host. It provides the prompt pipeline, durable
state, and tool plumbing; the host provides the agent's role, workflow, policy,
and default tool set.

## Harness Construction

Use `canto.NewHarness` for normal applications:

```go
h, err := canto.NewHarness("assistant").
	Instructions("You are a concise assistant.").
	Model("gpt-5.4").
	Provider(provider).
	SessionStore(store).
	Environment(canto.Environment{Workspace: root}).
	Tools(tools...).
	Build()

turn, err := h.Session("session-1").Submit(ctx, canto.TextPrompt("Say hello."))
if err != nil {
	return err
}
for event := range turn.Events() {
	switch payload := event.Payload.(type) {
	case canto.RunChunkPayload:
		renderChunk(payload.Chunk)
	case canto.RunSessionPayload:
		updateProjection(payload.Event)
	case canto.RunErrorPayload:
		renderError(payload.Err)
	}
}
result, err := turn.Result()
if err != nil {
	return err
}
```

`Submit` accepts one durable typed turn transaction. The returned `Turn` owns
the stable turn ID, cancellation handle, one ordered `RunEvent` stream with
typed payloads, and final settlement through `Turn.Result`.

`Prompt` is the blocking text convenience wrapper for hosts that do not need
turn events. `PromptStream` is a stream convenience wrapper for older/simple
hosts that do not need a `Turn` handle. New live hosts should prefer typed
`Submit` over wiring `runtime.Runner.SendStream` and `runtime.Runner.Watch`
separately.

The session facade also exposes normal maintenance operations so hosts do not
need to reach through the harness store for routine replay, snapshots, or
branching. Context compaction stays in the opt-in `governor` package; hosts
load the session facade with `Replay` and call `governor.CompactSession`
explicitly so compaction policy does not become part of the root harness API.

```go
sess := h.Session("session-1")
events, err := sess.EventsAfter(ctx, lastSeq)
replayed, err := sess.Replay(ctx)
compactResult, err := governor.CompactSession(ctx, provider, "gpt-5.4", replayed, governor.CompactOptions{MaxTokens: 20_000, OffloadDir: ".canto/offload"})
snapshotted, err := sess.SnapshotIfNeeded(ctx, canto.SnapshotOptions{MaxEvents: 100})
branch, err := sess.Fork(ctx, "session-1-review", session.ForkOptions{BranchLabel: "review"})
```

Text helpers call through typed prompts:

```go
result, err := h.Session("session-1").Prompt(ctx, "Say hello.")
result, err = runner.SendText(ctx, "session-1", "Say hello.")
```

Typed prompts are the framework data model:

```go
prompt := llm.NewPrompt(llm.Message{
    Role: llm.RoleUser,
    Parts: []llm.ContentPart{llm.TextPart("Say hello.")},
})
turn, err := h.Session("session-1").Submit(ctx, prompt)
```

`Environment(...)` groups optional capabilities such as workspace, executor,
sandbox, secrets, and bootstrap context. It does not register tools or make
approval decisions by itself. `environmenttool.Tools(...)` is the opt-in
bridge for capability toolkits when a host wants Canto to construct workspace
or executor-backed tools from the environment.

Use `agent.New` when you need the lower-level `agent.Agent` without the root
harness assembling a `runtime.Runner`, `tool.Registry`, and `session.Store`.

This split is intentional:

| API | Use when |
| :--- | :--- |
| `canto.NewHarness` | You want the conventional path: harness + durable session handle. |
| `agent.New` | You are composing your own runner, graph, test harness, or custom framework layer. |
| `prompt.NewBuilder` | You need full control over request processors and commit-time mutators. |

## Instructions

`Instructions(...)` is host text. Canto inserts it as the leading system
message through `prompt.Instructions`.

If a system message already exists in the request, Canto prepends the
instructions to that message. If no system message exists, Canto creates one.
For models that do not use the `system` role, built-in providers prepare a
provider-specific request copy at send time and rewrite the message to the
model's configured instruction role there.

Canto does not ship a default persona. Empty instructions mean Canto adds no
agent-role prompt.

## Prompt Pipeline

`agent.New` and `canto.NewHarness(...).Build()` use this request pipeline:

1. `prompt.Instructions(instructions)` — insert host instructions.
2. `prompt.NewLazyTools(registry)` — expose tool specs or `search_tools`.
3. `prompt.History()` — append durable context and transcript history. Stable
   context is placed before the transcript and recorded as the cache prefix.
4. `prompt.CacheAligner(2)` — preserve the stable prompt prefix and recent
   history.

The builder can also run commit-time mutators before request construction, such
as compaction or artifact recording.

The order is deterministic. Request processors are stored in an ordered slice
and run in that order. In the default agent path, custom request processors are
inserted before cache alignment, so custom system blocks and tool changes are
included in cache markers. Provider-specific model capability adaptation happens
at the built-in provider send boundary on a request copy, not during prompt
construction.

## Cache Stability

Canto keeps the common path cache-friendly by making the stable prefix
automatic:

- system instructions and feature prompt blocks are assembled before history;
- durable stable context, such as bootstrap snapshots and compaction summaries,
  is placed before the transcript as non-privileged user-role context;
- request-specific context blocks are inserted after that stable prefix;
- tool schemas are sorted by the registry before request construction;
- cache alignment runs after host prompt/tool processors;
- provider-specific role/thinking/tool rewrites happen on a copy when the
  selected provider sends the request.

Adding, removing, or changing instructions, feature blocks, or tool schemas
necessarily changes the cache prefix. Processors that add timestamps, random
text, or request-specific data to the leading system message will also reduce
cache reuse. Hosts that need tighter control can use `prompt.NewBuilder`
directly, but the default agent path is ordered for prefix-cache reuse without
extra setup.

## Text Canto May Add

Most Canto text is opt-in. The default agent path only adds text when the
corresponding feature is enabled or triggered.

| Source | When added | Text shape |
| :--- | :--- | :--- |
| `Instructions(...)` | Host supplies instructions | Host-provided system text. |
| `prompt.NewLazyTools` | Registry has more than 20 tools, or tools marked `Deferred` | Short hint that additional tools are available through `search_tools`. |
| `runtime.Bootstrap` | Host calls `Runner.Bootstrap` | `# Workspace Snapshot` with cwd, root files, and tool names. |
| `memoryprompt.New` | Host adds the memory request processor | `<memory_context>...</memory_context>`. |
| `skill.PreloadPrompt` | Host preloads skills | `Preloaded Skills:` plus selected skill instructions. |
| `approval.CircuitBreakerGuard` | Approval circuit breaker is tripped and host installed the guard | Notice that automated approvals are disabled. |
| `governor.Summarizer` | Host enables summarization compaction | Internal summarizer prompt; result is stored as non-privileged stable context. |

## Tools

Canto registers no domain tools by default. The host decides which tools an
agent can call.

Available tool modules:

| Module | Tools |
| :--- | :--- |
| `typedtool.New` / `typedtool.Must` | First-class typed Go tool authoring. JSON stays at the tool boundary. |
| `workspacetool.NewReadFileTool(root)` | `read_file`. |
| `workspacetool.NewWriteFileTool(root)` | `write_file`. |
| `workspacetool.NewListDirTool(root)` | `list_dir`. |
| `workspacetool.NewEditTool(root)` | `edit` with one or more exact replacements in one file. |
| `executortool.ShellTool` | `shell`, using `executor.Executor`; defaults to `sh -c` and can be configured for another shell or wrapper. |
| `executortool.NewCodeExecutionTool(language)` | `execute_code` for a configured language. |
| `service.New` | Typed service/API tools from Go handlers with service retry helpers. |
| `agent.HandoffTool(target)` | Transfer to another agent. |
| `runtime.NewInputGate().Tool(sess)` | `request_human_input`. |
| `governor.NewCompactTool` | Manual compaction tool. |
| `skill` tools | Skill read/manage tools and skill prompt processors. |
| `tool/mcp` | MCP-discovered tools wrapped as Canto tools. |
| `tool.NewSearchTool` | `search_tools`; inserted automatically by lazy tool loading when needed. |

Canto does not provide coding-agent tool presets. Hosts assemble the exact tool
set they want. Search and glob behavior should usually come from the configured
shell, a host-owned code index, or MCP tools rather than a Canto default.
For capability-oriented defaults, use `environmenttool.Tools` explicitly:

```go
env := canto.Environment{Workspace: root, Executor: exec}
tools, err := environmenttool.Tools(env, environmenttool.Config{Workspace: true, Executor: true})
if err != nil {
	return err
}

h, err := canto.NewHarness("assistant").
	Model("gpt-5.4").
	Provider(provider).
	Environment(env).
	Tools(tools...).
	SessionStore(store).
	Build()
```

## Convention

Canto follows the common SDK convention:

- instructions/system prompt are supplied by the host;
- tools are explicit and modular;
- optional features add their own small prompt blocks;
- lower-level APIs remain available for custom runtimes.

The main Canto difference is durable state: prompt history comes from the
append-only session log and compaction snapshots, not from a caller-managed
message slice.
