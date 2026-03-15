# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.0.1] - 2026-03-15

### Added

**Core Loop**

- Provider-agnostic LLM interface with OpenAI and Anthropic providers
- Agentic loop: perceive → decide → act → observe (`agent/`)
- Append-only session event log with JSONL file store (`session/`)
- Tool interface, registry, and `BashTool` (`tool/`)

**Production Reliability**

- Provider fallback and API key rotation (`llm/resolver.go`)
- Per-session lane queue for serialized execution (`runtime/lane.go`)
- Context engineering pipeline: builder, token guard, budget guard (`context/`)
- Reversible context compaction: offload to filesystem before summarize
- Cost tracking and budget enforcement (`llm/cost.go`)
- SQLite session store with FTS5 full-text search

**Runtime Features**

- Workspace config loader supporting AGENTS.md and SOUL.md (`runtime/workspace.go`)
- Skill interface and SKILL.md progressive disclosure standard (`skill/`)
- Scheduled execution via robfig/cron (`runtime/heartbeat.go`)
- HTTP channel adapter with SSE streaming (`x/channel/http.go`)
- MCP client: stdio and streamable HTTP transports (`tool/mcp/client.go`)
- Trajectory recording for eval and RL (`session/trajectory.go`)

**Multi-Agent**

- Agent handoffs: `HandoffTool`, `StepResult`, `RecordHandoff` (`agent/handoff.go`)
- DAG orchestration with conditional routing and `CycleRunner` (`x/graph/`)
- Blackboard-based decentralized swarm (`x/swarm/`)
- `VectorStore` interface with SQLite brute-force adapter (`memory/`)
- Pure Go HNSW archival memory (no CGo) and semantic ACI tools (`memory/hnsw.go`, `tool/memory.go`)
- Evaluation harness over trajectory store (`x/eval/`)

**Memory**

- Core persona memory store with `CoreMemoryProcessor` (`memory/core.go`)
- SHA256 content hash deduplication for archival memory
- OpenTelemetry `gen_ai.*` metrics for token counts and cost (`llm/cost.go`)

### Changed

- HNSW scores standardized to similarity (1 - distance) for consistent ordering
- SQLite stores use WAL mode and busy timeout across all backends

### Fixed

- Heartbeat and runner concurrency and shutdown correctness
- Leaky heartbeat goroutine in tests
- Memory package issues identified in review: score normalization, filter correctness, Delete semantics
