# AI Context Index

## Root

- [STATUS.md](STATUS.md) — current phase, next tasks, backlog, blockers
- [DESIGN.md](DESIGN.md) — canonical 6-pillar framework architecture and package boundaries
- [DECISIONS.md](DECISIONS.md) — stable principles plus recent architecture and planning decisions
- [PLAN.md](PLAN.md) — sprint history, completed tranche, and current frontier

## Completed Sprints

All six sprints are complete. See [PLAN.md](PLAN.md) for full history.

- [01-core-loop-and-tool-runtime](sprints/01-core-loop-and-tool-runtime.md) — hardened loop, turn stop states, hooks, metadata, and tool batching
- [02-durable-sessions-and-graphs](sprints/02-durable-sessions-and-graphs.md) — replay, idempotency, checkpoints, loop nodes, and wait-state recovery
- [03-subagents-and-isolation](sprints/03-subagents-and-isolation.md) — spawning lifecycle, delegation APIs, isolation modes, and worktrees
- [04-context-workspace-and-security](sprints/04-context-workspace-and-security.md) — budget gates, masking, rebuilds, validation, sandboxing, and dedup
- [05-memory-skills-and-retrieval](sprints/05-memory-skills-and-retrieval.md) — memory index/retrieval plus scalable skill loading
- [06-eval-observability-and-alpha](sprints/06-eval-observability-and-alpha.md) — eval harnesses, telemetry, and alpha contract

## Key Research

- [claude-code-review-2026-04.md](research/claude-code-review-2026-04.md) — consolidated Claude Code audit: adopted patterns, remaining gaps, reference catalog
- [agent-systems-landscape-2026-04.md](research/agent-systems-landscape-2026-04.md) — shared fact base across Claude Code, Pi, Droid, Codex, opencode, Letta, Slate, and others
- [message-boundary-research-2026-04.md](research/message-boundary-research-2026-04.md) — provider/framework evidence for transcript/context/hidden event separation
- [model-context-contracts-2026-04.md](research/model-context-contracts-2026-04.md) — fresh SDK audit for neutral context, provider prep, tools, reasoning, and cache
- [agent-loop-orchestration-sota-2026-04.md](research/agent-loop-orchestration-sota-2026-04.md) — loop generator, turn stop states, escalation, and withholding
- [tool-execution-orchestration-sota-2026-04-04.md](research/tool-execution-orchestration-sota-2026-04-04.md) — tool metadata, hooks, batching, deferred loading, and MCP direction
- [session-durability-sota-2026-04.md](research/session-durability-sota-2026-04.md) — replay, idempotency, snapshots, and replay-or-fork semantics
- [graph-workflow-sota-2026-04.md](research/graph-workflow-sota-2026-04.md) — loop nodes, fan-out, checkpointing, and graph pause/resume
- [subagent-multi-agent-sota-2026-04.md](research/subagent-multi-agent-sota-2026-04.md) — spawn lifecycles, delegation modes, and isolation models
- [context-engineering-sota-2026-04.md](research/context-engineering-sota-2026-04.md) — budget guards, non-blocking compaction, masking, and rebuilds
- [workspace-filesystem-sota-2026-04.md](research/workspace-filesystem-sota-2026-04.md) — path validation, VFS direction, sandboxing, and indexing
- [security-guardrails-sota-2026-04.md](research/security-guardrails-sota-2026-04.md) — pre/post hooks, audit logging, secret injection, and sandbox layers
- [skills-progressive-disclosure-sota-2026-04.md](research/skills-progressive-disclosure-sota-2026-04.md) — registry, routing, preloading, and skill security
- [agent-memory-context-architecture-2026-04-04.md](research/agent-memory-context-architecture-2026-04-04.md) — memory manager, index, retrieval, consolidation, and sleep-time compute
- [evaluation-benchmarking-sota-2026-04.md](research/evaluation-benchmarking-sota-2026-04.md) — eval runners, trajectory scorers, and reliability metrics

## Supporting Design And Review (active)

- [review/ion-feedback-tracker-2026-04-28.md](review/ion-feedback-tracker-2026-04-28.md) — single active tracker for confirmed Ion-derived Canto framework issues
- [design/authoring-surface.md](design/authoring-surface.md) — Phase 5 authoring design plus landed root builder and service-tool seams
- [design/api-surface-review-canto-3p5m.md](design/api-surface-review-canto-3p5m.md) — API surface DX findings and friction points (Phase 5)
- [design/ion-friction-intake.md](design/ion-friction-intake.md) — historical intake pattern for turning consumer findings into concrete Canto issues
- [design/model-context-contract-2026-04.md](design/model-context-contract-2026-04.md) — canonical neutral context/provider-prep contract and audit gates
- [review/canto-ion-roadmap-2026-04.md](review/canto-ion-roadmap-2026-04.md) — Canto/Ion boundary review and M1 stabilization sequence
- [review/test-quality-and-rewrite-gap-2026-04.md](review/test-quality-and-rewrite-gap-2026-04.md) — qualitative test assessment and from-scratch design gap review before Ion
- [review/load-bearing-coverage-audit-2026-04.md](review/load-bearing-coverage-audit-2026-04.md) — M1 coverage audit for agent, session, workspace, tools, prompt, governor, approval, runtime
- [review/tool-surface-audit-2026-04.md](review/tool-surface-audit-2026-04.md) — Canto tool primitives, no presets, no built-in glob/grep, configurable shell
- [review/framework-readiness-2026-04-20.md](review/framework-readiness-2026-04-20.md) — M1 readiness audit: DX, `x/` boundary, examples, and alpha blockers
- [design/identity-first-workspace-and-projections-2026-04.md](design/identity-first-workspace-and-projections-2026-04.md) — Phase 4 architecture correction (complete)
- [design/memory-direction-2026.md](design/memory-direction-2026.md) — current memory direction and transition map
- [design/codex-shell-env-2026-04.md](design/codex-shell-env-2026-04.md) — Codex zsh stale GOROOT fix
- [review/first-alpha-release-gate-2026-04-01.md](review/first-alpha-release-gate-2026-04-01.md) — alpha release expectations and missing gates

## Legacy Pointers

- [ion-framework-issues.md](ion-framework-issues.md) — retained only to redirect older references to the active Ion feedback tracker

## Supporting Research (secondary)

- [research/dspy-authoring-insights-2026-04.md](research/dspy-authoring-insights-2026-04.md) — DSPy signature/module/adapter/optimizer lessons for Canto and Ion
- [research/gepa-reflective-optimization-2026-04.md](research/gepa-reflective-optimization-2026-04.md) — GEPA implications for Canto eval traces and optimizer artifacts
- [research/frameworks/comparison-summary.md](research/frameworks/comparison-summary.md) — framework crosswalk for LangGraph, PydanticAI, Vercel, AutoGen, CrewAI, Bee
- [research/academic-sota-2026-04.md](research/academic-sota-2026-04.md) — academic papers organized by theme
- [research/canto-go-strategy-2026.md](research/canto-go-strategy-2026.md) — Go 1.26+ competitive advantage analysis
- [research/ecosystem-comparison-2026.md](research/ecosystem-comparison-2026.md) — Canto vs adjacent SDKs and frameworks
- [research/harness-optimization-sota-2026-04.md](research/harness-optimization-sota-2026-04.md) — Meta-Harness, LLM-Wiki, Async Tool Feedback patterns
- [research/hitl-approval-sota-2026-04.md](research/hitl-approval-sota-2026-04.md) — tiered permissions, LLM classifiers, durable interruptions
- [research/observability-tracing-sota-2026-04.md](research/observability-tracing-sota-2026-04.md) — OTel GenAI conventions, traces, cost metrics
- [research/scion.md](research/scion.md) — Google Scion multi-agent orchestration analysis
- [research/streaming-cost-sota-2026-04.md](research/streaming-cost-sota-2026-04.md) — token economics, streaming performance
- [research/codedb-rtk-sota-2026-04.md](research/codedb-rtk-sota-2026-04.md) — CodeDB/RTK architectures for agentic code search
- [research/workspace-filesystem-patterns.md](research/workspace-filesystem-patterns.md) — sandboxing, path traversal, checkpointing, rollback, indexing
- [research/RESEARCH-GUIDE.md](research/RESEARCH-GUIDE.md) — instructions for executing SOTA research across topics
- [research/SOTA-RESEARCH-PLAN.md](research/SOTA-RESEARCH-PLAN.md) — master research plan

## Historical Design (landed/proposed, pre-Phase 4)

These early design docs informed sprints 01-06 and the Phase 4 tranche. Kept for reference, not active guidance. Not all are listed individually — see `design/` for full set.

- [design/overflow-recovery-design-2026-03-31.md](design/overflow-recovery-design-2026-03-31.md) — overflow recovery (landed in governor/)
- [design/framework-boundary-review-2026-03-28.md](design/framework-boundary-review-2026-03-28.md) + [roadmap](design/framework-boundary-roadmap-2026-03-28.md) — boundary audit
- [design/compaction-as-tool.md](design/compaction-as-tool.md) — idea for exposing compaction as a tool
- [design/cross-pollination.md](design/cross-pollination.md) — pi → canto patterns

## tmp/

Session-scratch workspace. Delete files older than the current sprint. Do not commit permanent docs here.

## Historical Research (consolidated into main SOTA files)

Sources merged into the SOTA syntheses above, or pre-SOTA early research. Kept for reference. Not all listed — see `research/` for full set.

- [research/agent-framework-landscape-2026.md](research/agent-framework-landscape-2026.md) — consolidated into ecosystem comparison
- [research/migration-and-parity-2026-03.md](research/migration-and-parity-2026-03.md) — consolidated into ecosystem comparison
- [research/state-context-and-storage-patterns-2026.md](research/state-context-and-storage-patterns-2026.md) — consolidated into session/context SOTA
- [research/agent-deployment-and-runtime-patterns-2026-03-19.md](research/agent-deployment-and-runtime-patterns-2026-03-19.md) — landed
- [research/hermes-agentskills-2026.md](research/hermes-agentskills-2026.md) — pre-SOTA research
- [research/pi-architecture.md](research/pi-architecture.md) — pi-mono analysis
- [research/model-routing-for-subagents.md](research/model-routing-for-subagents.md) — model selection strategy
- [research/prompt-caching-providers-2026.md](research/prompt-caching-providers-2026.md) — provider caching survey
