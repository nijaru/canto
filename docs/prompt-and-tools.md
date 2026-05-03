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
	Tools(tools...).
	Build()

result, err := h.Session("session-1").Prompt(ctx, "Say hello.")
```

For live hosts, `PromptStream` returns one stream of `RunEvent` values that
contains model chunks, durable session events, and the final result/error.
Hosts should prefer that over wiring `runtime.Runner.SendStream` and
`runtime.Runner.Watch` separately.

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
| `prompt.MemoryPrompt` | Host adds the memory request processor | `<memory_context>...</memory_context>`. |
| `skill.PreloadPrompt` | Host preloads skills | `Preloaded Skills:` plus selected skill instructions. |
| `governor.CircuitBreakerGuard` | Approval circuit breaker is tripped and host installed the guard | Notice that automated approvals are disabled. |
| `governor.Summarizer` | Host enables summarization compaction | Internal summarizer prompt; result is stored as non-privileged stable context. |

## Tools

Canto registers no domain tools by default. The host decides which tools an
agent can call.

Available tool modules:

| Module | Tools |
| :--- | :--- |
| `coding.NewReadFileTool(root)` | `read_file`. |
| `coding.NewWriteFileTool(root)` | `write_file`. |
| `coding.NewListDirTool(root)` | `list_dir`. |
| `coding.NewEditTool(root)` | `edit`. |
| `coding.NewMultiEditTool(root)` | `multi_edit`. |
| `coding.ShellTool` | `shell`, using `coding.Executor`; defaults to `sh -c` and can be configured for another shell or wrapper. |
| `coding.NewCodeExecutionTool(language)` | `execute_code` for a configured language. |
| `service.New` | Typed service/API tools from Go handlers. |
| `agent.HandoffTool(target)` | Transfer to another agent. |
| `runtime.NewInputGate().Tool(sess)` | `request_human_input`. |
| `governor.NewCompactTool` | Manual compaction tool. |
| `skill` tools | Skill read/manage tools and skill prompt processors. |
| `tool/mcp` | MCP-discovered tools wrapped as Canto tools. |
| `tool.NewSearchTool` | `search_tools`; inserted automatically by lazy tool loading when needed. |

Canto does not provide coding-agent tool presets. Hosts assemble the exact tool
set they want. Search and glob behavior should usually come from the configured
shell, a host-owned code index, or MCP tools rather than a Canto default.

## Convention

Canto follows the common SDK convention:

- instructions/system prompt are supplied by the host;
- tools are explicit and modular;
- optional features add their own small prompt blocks;
- lower-level APIs remain available for custom runtimes.

The main Canto difference is durable state: prompt history comes from the
append-only session log and compaction snapshots, not from a caller-managed
message slice.
