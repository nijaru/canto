# Status

**Phase:** Ion Pi-first P1 migration support
**Focus:** reduce Canto to the Ion-proven P1 kernel before M1 docs/release
posture resumes.
**Blockers:** Ion P1 remains open; Canto should only keep or add P1 work when
Ion acceptance evidence identifies a framework-owned gap.
**Updated:** 2026-05-24

## Context

Sprints 01-06 and the Phase 4 architecture-correction tranche are complete. The primitives are load-bearing: durable sessions with replay/projections, identity-first workspace (WorkspaceFS, ContentRef, dedup, search, OverlayFS, MultiFS+memory.FS), tiered compaction, cache-aware mutations, subagent delegation, progressive-disclosure skills, MCP tools, approval/auto-mode with circuit breaker, OTel tracing, and eval harnesses.

Phase 5 still has SOTA and DX inputs, but the active operating mode is now
supporting Ion's pre-v0 design-closure review after the Ion-driven optimal-core
work:

- **Canto owns mechanism:** durable sessions, prompt/runtime boundaries, tool execution, workspace capability, compaction, approval state-machine seams, provider normalization, and examples that prove the pieces compose.
- **Ion owns product policy:** terminal UX, task/planner behavior, approval delivery and thresholds, shell classifier heuristics, memory aggressiveness, command catalog choices, and end-user workflow.
- **Ion is the flagship consumer:** Ion should expose missing or awkward
  primitives through design review or real product use, but Ion work is not
  active in this repo. Do not keep standing Ion product tasks in Canto; add a
  Canto task only when separate Ion work identifies a concrete framework issue.
- **Canto API audit:** active core-contract review lives in `ai/review/core-api-contract-audit-2026-04-30.md`; use it to track which core packages have actually been reviewed.
- **Ion feedback tracker:** confirmed Ion-derived framework issues live in `ai/review/ion-feedback-tracker-2026-04-28.md`.
- **Ion as framework pressure:** Ion's prior ideal-core lane is no longer final
  proof. Pi is the P1 design control, and Canto should close framework-owned
  gaps where Pi has one session-scoped harness owner instead of split
  Canto/Ion ownership.
- **Ion-first kernel rule:** Until Ion passes its Pi-level scenario matrix,
  Canto is a pre-M1 kernel, not a stable upstream dependency. Keep reusable
  primitives in Canto only when Ion needs and proves them. If a Canto boundary
  slows Ion P1, Ion may carry a clean local path while Canto tracks whether to
  re-extract, simplify, or delete the primitive after acceptance.
- **Next-phase roadmap:** [ai/design/framework-readiness-roadmap-2026-05-01.md](design/framework-readiness-roadmap-2026-05-01.md) remains the sequencing source, amended by `canto-2vxb`: make the harness facade clear before polishing M1 docs/release language.

SOTA/DX research is part of the Canto pre-Ion gate when it can change stable API or primitives. New research remains delta-based and must name the Canto primitive it would change.

Current authoring-surface inputs:

- `ai/design/authoring-surface.md` completed `canto-0j58`; `canto-gymf`, `canto-43vh`, and `canto-umuc` landed the root harness seam, maintained coding-agent reference, and typed service/API helper.
- `ai/design/api-surface-review-canto-3p5m.md` now distinguishes real DX gaps from stale scratch findings.
- `ai/research/dspy-authoring-insights-2026-04.md` captures DSPy lessons for signatures, modules, adapters, eval metrics, and offline optimization.
- Existing `ai/research/frameworks/` notes already cover LangGraph, PydanticAI, AutoGen, Vercel AI SDK, MCP, and adjacent framework comparisons. Future SOTA work should be delta-based.

Current Ion/Canto reference discipline: Ion's roadmap is Pi -> Pi+, with Pi as
the primary phase-1 internal control and Claude Code/Codex as secondary
long-term product references. Canto should inspect major current frameworks and
SDKs for primitive insights, including OpenAI Agents SDK, Anthropic/Claude
Agent SDK, MCP, A2A, Google ADK, Microsoft Agent Framework, Semantic Kernel,
AutoGen, LangGraph/LangChain, Pydantic AI, LlamaIndex, CrewAI, Agno, Mastra,
Vercel AI SDK, BeeAI, Letta, Flue, DSPy/GEPA, and durable-execution systems
such as Temporal, DBOS, and Inngest. That list is a living map, not exhaustive.
No framework or paper should create implementation work here unless M1
readiness or a concrete Canto contract gap requires it. The next recent-paper
scan is tracked from Ion as `tk-k4y8` and is deferred until a phase-2 research
lane is selected.

## Next

**Audit outcome:**

- `canto-hr9r` reviewed and refactored framework packages in green slices:
  root harness/session APIs, prompt, providers, session stores/rebuilder/export,
  runtime scheduler/child/coordinator/lane, agent tool lifecycle, hooks,
  tracing, workspace VFS/search, memory manager/stores/index/VFS/vector search,
  coding tools/executor, approval, service, skills, artifact/audit/safety/MCP,
  examples, and `x/*` extension packages.
- Runtime coordinator/lane were inspected earlier in this audit and have strong
  FIFO/retry/cancel/parallel-session coverage; revisit only if new evidence
  points there.
- `x/redis` is structurally split and compile/race-checked under `-tags redis`,
  but live Redis behavior still requires `CANTO_TEST_REDIS_URL`.
- The completed Canto redesign source is
  `ai/design/optimal-core-redesign-2026-05.md`, plus Ion's completed
  `/Users/nick/github/nijaru/ion/ai/sprints/02-ideal-core-completion.md`.
  The refreshed Pi/AX comparison drove durable turn identity and session
  sequence; that P1 architecture blocker is closed.
- Active task graph: `canto-iusu` is the current Canto P1 task: audit Ion-used
  surfaces and reduce, rewrite, delete, or explicitly defer framework
  primitives based on Ion P1 acceptance evidence. `canto-wuev` remains the
  public-surface docs/examples slice when docs posture is selected. The prior
  `canto-98el` facade slice is now closed. `canto-sqtc` added the
  sequence-bounded event-read API required by Ion's typed display projection,
  `canto-01ge` landed the native `Turn`/`Submit` facade, `canto-d6kl` landed
  durable event `TurnID`/`Seq`, and `canto-iq8h`, `canto-uduq`, `canto-dvtd`,
  and `canto-xz1w` are complete.
- Current `canto-iusu` slices: memory-backed prompt retrieval moved out of
  core `prompt` into `memory/memoryprompt`, approval-capable typed tool
  authoring moved out of core `tool` into `tool/typedtool`, and child skill
  validation/scoping/preload moved out of `runtime` into `skill.RuntimeConfig`;
  `prompt` no longer imports `memory/`, `tool` no longer imports the approval
  state machine, and `runtime` no longer imports `skill` or `agentskills`.
  The latest slice moved artifact body-storage helpers out of `session` into
  `artifact.StoreSessionArtifact` and moved approval circuit-breaker prompt
  injection from `governor` into `approval`; `session` no longer imports
  `artifact`, and `governor` no longer imports `approval`.
- `canto-wuev` found a real public-surface mismatch: public harness docs and
  examples should teach native `Submit` / `Turn` as the common path.
- `canto-uduq` landed the first executable contract slice: `RunEvent` now
  carries session id, stable external turn id, monotonic sequence, and
  durability classification. PromptStream tests now cover ordered metadata,
  durable usage before terminal result, yielding post-tool hook settlement
  before tool completion/result, and overflow recovery with stable turn id plus
  one host terminal result.
- `canto-dvtd` replaced `PromptStream` snapshot/watch repair with synchronous
  session event observers: durable session events now emit from `Append` order,
  chunks and terminal results share the same host sequence, and slow stream
  consumers backpressure instead of dropping and replaying live events.
- `canto-xz1w` promoted generic lifecycle projection to Canto stream metadata:
  `RunEvent` carries typed usage/lifecycle state, provider usage deltas,
  active tool snapshots, compaction started/completed status, retry status,
  cancellation state, terminal state, and overflow-recovery retry events.
- Reference posture: P1 stays Pi-level, with Codex app/CLI and Claude Code as
  P1 ergonomics/performance references. AX, DSPy, GEPA, Slate, Droid, richer
  Codex/Claude workflows, and similar systems are Phase 2/Pi+ inputs unless
  they expose a Canto primitive needed for P1 correctness.
- Performance is part of this Canto lane: reduce host-side stream assembly,
  avoid unnecessary polling/flush loops, keep replay/resume bounded, and give
  Ion a low-latency stream it can render without reconstruction.
- `canto-98el` is closed: `Harness.Session(id)` now shares session-scoped
  state across handles, rejects overlapping `Submit` calls with
  `ErrSessionBusy`, exposes `RuntimeEvents`, emits `queue_update`,
  `save_point`, `settled`, and `abort` events, owns `Steer`, `FollowUp`, and
  `NextTurn` queues, exposes `QueuedInput` / `ClearQueuedInput`, and includes
  durable active-branch/model/thinking primitives. Ion has imported the latest
  required Canto P1 revision
  `github.com/nijaru/canto v0.0.0-20260524082550-c4897262a011`. Branch/tree
  navigation policy is not an Ion P1 blocker under current Ion rules; branch
  views stay parked unless explicitly promoted.
- `canto-x8d0` is closed: `runtime.Runner` and the shared child runner now
  default to no whole-turn execution timeout. `WithExecutionTimeout` remains
  opt-in for hosts that intentionally want a cap; normal host turns are bounded
  by caller/provider cancellation and narrower operation timeouts.
- `canto-y88u` is closed: workspace glob patterns now use the same
  absolute/traversal/malformed path error classes as normal rooted file paths,
  and `Root.Glob` supports recursive `**` matching for host tool authors that
  need Pi-like workspace search behavior.
- `canto-9p8k` is closed in Canto `eae32b9`: `llm.ContentPart` now supports
  image data/URL parts, `tool.ContentTool` can return structured parts,
  `runTools` preserves content parts on tool-result messages, tracing preserves
  the content-tool interface, and OpenAI/Anthropic converters emit image
  content for provider-visible messages.
- `canto-dasc` is closed in this slice: `tool.StreamingUpdateTool` adds
  snapshot updates so framework hosts can show live tool output while replacing
  provider-visible final output with a rolling summary. This closes the
  framework side of Ion's Pi-parity bash-output gap.
- `canto-vhjg` is closed in Canto `5f313f6`: `RunEvent` now carries envelope
  metadata plus one typed payload, and Ion imported that exact revision in
  `9ff72a4`.
- `canto-33aq` is closed in Canto `0962930`: typed Go tool authoring landed.
  During the later kernel-reduction pass, it moved from core `tool` to
  `tool/typedtool` so optional approval support does not make the base tool
  registry depend on approval state. `HarnessBuilder.ToolsFromEnvironment`
  wires workspace/executor capability tools from `Environment` on explicit
  request.
- `canto-re2x` is closed in Canto `1be9c57`: root `canto.Session` now exposes
  replay, sequence-bounded events, compaction, projection snapshots, and fork
  methods for normal host maintenance.
- The old "no known P1 framework seam remains" status is superseded by the
  Pi-first migration. The concrete Pi-like steering/follow-up and session
  facade primitives are landed; the remaining Canto P1 work is kernel
  reduction against Ion-proven usage. Branch/tree product policy stays parked
  unless Ion explicitly promotes it.

**Ion pre-v0 design-closure support:**

- Canto M1 docs/release work is selectable again when the user wants release
  posture. It is not the active milestone unless selected.
- `canto-wuev` found a real public-surface mismatch: Canto code had the native
  `Session.Submit` / `Turn` transaction, but README, prompt docs, and examples
  still taught `Prompt` / `PromptStream` as primary. The fix makes `Submit`
  the obvious common path and documents `Prompt` / `PromptStream` as
  convenience wrappers.
- `canto-cvqs` is the current Ion-exposed framework seam: terminal
  `RunUsage` metadata must be delta-consumable by hosts. Canto now records
  provider usage deltas already emitted on the stream and attaches one
  terminal delta correction when the final turn/run cumulative usage exceeds
  those deltas, while keeping later terminal result usage cumulative-only to
  avoid double-counting.
- The `canto-2vxb` harness-facade work shaped the common authoring/runtime path
  around a framework `Harness`, durable session handle, and one ordered
  run-event stream. The review output is captured in
  `ai/design/authoring-surface.md`.
- The Ion import/removal proof is closed: Ion imported Canto session/turn
  revisions, removed generic terminal lifecycle reconstruction, moved runtime
  transitions into its controller, and now maintains bounded typed display
  projections over Canto event sequence.
- Work should continue as clean pre-alpha breaks, not compatibility wrappers.
- Do not create Canto implementation work from Mesa/Archil/OpenHands/DSPy/GEPA
  research alone. Those are roadmap inputs; implementation starts only when M1
  readiness or continued Ion use exposes a concrete framework seam.

**Release/doc gate:**

- `canto-khhl` (p3, deferred) Docs completeness pass for M1 - README, examples, provider doc, and godoc enough for a new user.
- `canto-2if9` (p3, blocked on docs and any future confirmed consumer-framework issues) Publish first-alpha package contract - one page, no compatibility promise beyond the stated alpha scope.

**Deferred research and optional primitives:**

- `canto-pc4b` (p4) Forked subagents from parent session snapshots - only if Ion or runtime validation proves the current child-session model is insufficient.
- `canto-ic25` / `canto-mr13` (p4) SOTA cadence and interrupt generalization - post-M1 unless a concrete blocker appears.

**Design hygiene:**

- `canto-3xay` (p3, deferred) DESIGN.md pillar consolidation follow-through — documentation hygiene, not an Ion-switch blocker.

## Recently landed

- `canto-iusu` kernel-reduction slices — moved `prompt.MemoryPrompt` to
  `memory/memoryprompt.New` as a clean pre-M1 API break, exported
  `prompt.InjectContextBlock` as the generic cache-safe insertion helper, and
  updated the memory example/docs. Then moved typed tool authoring from
  `tool.NewTyped` / `tool.MustTyped` to `tool/typedtool`, and moved the
  approval declaration interface to `approval.RequirementProvider`. The latest
  slice moved child skill validation/scoping/preload out of `runtime` and into
  `skill.RuntimeConfig`, artifact storage helpers out of `session`, and
  approval circuit-breaker prompt injection out of `governor`. The follow-up
  compaction pass kept `governor` as the Pi-like context governance owner and
  fixed `MinKeepTurns` to retain whole user-turn groups for offload and
  summarize, not raw message counts. Focused package tests pass for `prompt`,
  `memory/memoryprompt`, `tool`, `tool/typedtool`, `agent`, `tracing`,
  `service`, `executortool`, `tool/mcp`, `runtime`, `skill`, `session`,
  `artifact`, `governor`, and `approval`; full `go test ./...`, `go vet
  ./...`, and `git diff --check` pass after the compaction fix.
- `canto-sqtc` — framework-owned bounded event reads: `EventQueryStore.EventsAfter`
  is implemented for SQLite and JSONL stores so hosts can update typed
  projections after a durable sequence cutoff without querying store internals.
  Focused session tests, `go test ./session`, `go test ./...`, `go build ./...`,
  and `git diff --check` pass.
- `canto-dvtd` — PromptStream is now driven by an ordered session observer
  rather than snapshot/watch/callback repair. `session.Append` supports
  synchronous non-lossy observers, `runtime.Runner.ObserveEvents` exposes the
  hook, and root stream events now come from one sequenced emitter. Focused
  PromptStream/session observer tests, `go test ./...`, `go vet ./...`,
  `git diff --check`, and focused race tests pass.
- `canto-xz1w` — RunEvent lifecycle metadata landed: typed `RunUsage` and
  `RunLifecycle` cover provider usage deltas, step/turn/run terminal status,
  active tools, tool deltas/completion, retry, compaction start/completion, and
  cancellation. Runtime overflow recovery now emits a retry lifecycle event
  before compacting and retrying. Focused lifecycle/PromptStream/session/
  governor tests, `go test ./...`, `go vet ./...`, `git diff --check`, and
  focused race tests pass.
- `canto-hr9r` closed the Phase 5 audit in green commits. Current checkpoint:
  semantic retrieval enforces namespace/role filters after fusion;
  graph/Redis/tool storage were split by responsibility; task JSON mutations
  preserve unknown fields; file references trim natural trailing punctuation;
  HNSW/SQLite vector search normalize bad limits and share metadata filter
  semantics; OverlayFS now exposes speculative state through its standard
  `fs.FS` view; child runs canceled while waiting for a max-concurrency slot now
  finish promptly and record a durable cancellation; streaming shell execution
  now cancels the subprocess when the host stops consuming deltas; write-through
  session detach now drains accepted events before returning; async memory write
  failures now surface through `Manager.Close`; tracing's streaming tool wrapper
  now starts spans on consumption and cancels wrapped streams when consumers
  stop early; OverlayFS rejects traversal/absolute speculative paths before
  they can enter overlay state; core memory block retrieval now keeps same-name
  blocks from different namespaces distinct through fusion; file-reference
  expansion no longer treats email addresses as `@file` references and handles
  angle-bracketed refs cleanly; approval policy errors now record a terminal
  cancellation event instead of leaving sessions durably waiting with no
  pending HITL request; SmartResolver now accounts for transient provider
  failures that happen during stream consumption instead of marking the provider
  healthy at stream start; macOS sandbox profiles now escape path strings before
  interpolating them into Seatbelt rules; FileStore now rejects path-like
  caller-supplied artifact IDs before using them in the object layout; MCP
  client discovery/call handling now rejects nil external SDK tool/result
  values cleanly; reference examples now close persistent session stores, check
  setup/append/input failures, and the autoresearch example fails closed on
  restore/log write errors; JSONL audit logger close is serialized with logging
  and leaves the logger explicitly closed. Each slice passed focused tests,
  `go test ./...`, `go build ./...`, and relevant race checks.
- Phase 5 checkpoint after the latest slices: `go vet ./...` and broad race
  gate over core plus framework-adjacent packages passed:
  `./agent ./session ./runtime ./prompt ./tool ./workspace ./llm ./governor
  ./memory ./coding ./service ./tracing ./hook ./approval ./artifact ./audit
  ./safety ./tool/mcp`.
- Harness facade first slice landed locally: root `canto.NewAgent`/`App.Send`
  has been replaced with `canto.NewHarness` plus
  `h.Session(id).Prompt/Events`; no compatibility alias was retained. Focused
  package/example tests and `go test ./... -count=1 -timeout 300s` pass.
- Harness stream slice landed locally: `Session.PromptStream` now returns one
  `RunEvent` channel containing model chunks, durable session events, and the
  terminal result/error, so hosts no longer need to merge `SendStream`
  callbacks with `Watch` for the common path. Focused root tests and full
  suite pass.
- Harness environment slice landed locally: `canto.Environment` groups
  workspace, executor, sandbox, secret, and bootstrap capabilities on the
  harness without registering tools or encoding product policy. Focused root
  tests and full suite pass.
- Typed provider reasoning capability metadata landed from Ion `tk-369n`
  feedback: `llm.Capabilities` now carries structured reasoning controls
  (named effort values, disable support, budget ranges), request preparation
  drops unsupported reasoning parameters, OpenAI reasoning models expose named
  efforts, Anthropic thinking models expose budget metadata, and generic
  OpenAI-compatible endpoints default to no reasoning params unless configured.
- C6 non-core quarantine fix landed in `c7f2fa9` and has been imported into Ion: dependency audit found core `agent` imported `x/tracing`, making an extension package load-bearing in every native turn. `x/tracing` was promoted to core `tracing/` with import/docs updates and no compatibility shim. Core package deps no longer include `github.com/nijaru/canto/x/*`; Canto focused/full/race gates and Ion focused/full/race/live-smoke gates are green after import.
- C5 retry/compaction/budget audit landed in `773f2ab` and has been imported into Ion: runtime-level overflow recovery is confirmed as the correct session-backed contract because it retries the whole agent turn and rebuilds the provider request from compacted effective history. Added focused runtime coverage for both a minimal runner agent and the normal `agent.New` provider request path; clarified that `governor.RecoveryProvider` retries an already-built request and is not the native session-backed recovery path. Canto focused/full/race gates and Ion focused/full/race/live-smoke gates are green after import.
- C3 agent/tool lifecycle and scheduler race fixes landed in `d3f8084` and have been imported into Ion: tool-boundary failures such as hook blocks, approval denials, ambiguous replay, and panics now become model-visible tool observations where possible; panics also record durable `ToolCompleted` error data. `LocalScheduler.Schedule` publishes the timer under the task mutex before callbacks can enter `start`/`finish`, closing the race found by the focused runtime race gate. Canto focused/full/race gates and Ion focused/full/race/live-smoke gates are green after import.
- C4 prompt/request validation fix landed in `9ba6120` and has been imported into Ion: provider preparation now validates neutral requests before capability rewriting, `ValidateRequest` rejects invalid roles, empty assistants, and orphan tool results, `Request.Clone` copies structured-output response formats, and retry test counters are race-clean. Canto focused/full/race gates and Ion focused/full/race/live-smoke gates are green after import.
- Runtime coordinator queued-timeout fix landed in `24f2ed9` and has been imported into Ion: `LocalCoordinator.Await` removes a queued ticket when its wait context is canceled or deadlined before lease grant, preventing an abandoned turn from staying at the lane head and blocking later turns.
- Session rebuilder recovery landed in `83c4d30` and has been imported into Ion: `EffectiveEntries` can synthesize a missing provider-visible tool result from durable `ToolCompleted` lifecycle data, and dangling assistant tool calls with no matching or recoverable result are dropped so replay cannot poison a follow-up provider turn.
- Canto/Ion tool replay boundary — `09140f7 feat(session): expose tool lifecycle metadata` added `HistoryEntry.Tool` projection metadata for host replay; Ion imported it in `ec5a548 refactor(storage): use canto tool projection`, removing Ion's raw Canto event scan for tool titles/errors.
- Current C1 toolkit split — Canto no longer exposes the product-shaped `coding/` package. Executor mechanics now live in `executor/`, workspace tools in `workspacetool/`, shell/code tools in `executortool/`, and the old model-visible `multi_edit` surface is folded into `edit` with `edits[]`.
- Current C2 typed prompt split — `llm.Prompt`, `llm.ContentPart`, `canto.TextPrompt`, and runtime text helpers are implemented in the worktree. Root `Session.Submit` and `runtime.Runner.Send` now accept typed prompts; text helpers call through that path; providers and compaction formatting read typed text through `Message.TextContent`.
- Ion feedback tracking cleanup — stale Ion issue notes were consolidated into `ai/review/ion-feedback-tracker-2026-04-28.md`; that file remains the concrete Ion-feedback intake while `core-api-contract-audit-2026-04-30.md` tracks the broader Canto core review.
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
- `canto-gymf` — root `canto.NewHarness` authoring seam, message helpers, and public `llm.FauxProvider`
- `canto-43vh` — buildable Claude Code/Codex/Cursor-class reference coding/service agent
- `canto-umuc` — typed service/API tool helper plus reference-agent validation
- `canto-l2iy` — superseded by C1 capability packages: `executor/`, `workspacetool/`, and `executortool/`
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
