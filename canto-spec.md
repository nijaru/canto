# canto

> A composable, minimal-abstraction Go framework for building LLM agents and agent swarms.
> Designed for optimal developer experience, production reliability, and SOTA research ideas.

---

## Design Philosophy

**Three rules that override everything else:**

1. **Code over configuration** — orchestration is deterministic Go code, not prompts. LLMs decide *what* to do; the framework decides *how* execution flows. Never put flow control in prompts.
2. **Composable over complete** — a small set of well-designed interfaces that compose cleanly beats a large framework with baked-in opinions. Ship without sub-agents, plan mode, or permission gates; let users build those as extensions.
3. **Append-only state** — the session event log is never mutated. Ever. This is load-bearing: KV-cache efficiency, trajectory replay, audit trails, and time-travel debugging all depend on it.

**Inspired by:**
- pi-mono's 4-layer minimalism and "if I don't need it, I won't build it" philosophy
- OpenClaw's operational reliability patterns (lane queues, context guards, skill progressive disclosure)
- Hermes-agent's trajectory recording and RL-readiness
- Google ADK's session/working-context separation and named processor pipelines
- Autoresearch's program.md pattern and append-only experiment log
- OpenAI Agents SDK's minimal primitives (agents, handoffs, guardrails, tracing)
- RLM's insight that long context should be treated as an external environment object, not fed directly into the model

---

## Architecture: 3 Layers + Extensions

```
+--------------------------------------------------------------+
|  Layer 4: Extensions  (graph, swarm, eval, channels, rl...)  |
+--------------------------------------------------------------+
|  Layer 3: Runtime     (session, context, tool, skill,        |
|                        runtime, memory, heartbeat)           |
+--------------------------------------------------------------+
|  Layer 2: Agent Loop  (agent, turn, handoff, step)           |
+--------------------------------------------------------------+
|  Layer 1: LLM         (provider, resolver, stream, cost)     |
+--------------------------------------------------------------+
```

Layers only depend downward. Extensions depend on Layer 3. Nothing in core depends on extensions.

---

## Package Structure

```
canto/
├── llm/                    # Layer 1: Provider-agnostic LLM interface
│   ├── provider.go         # Provider interface + streaming types
│   ├── resolver.go         # Multi-provider resolution, fallback chains, key rotation
│   ├── cost.go             # Token counting, cost tracking, budget enforcement
│   ├── message.go          # Message/Content types (text, tool_call, tool_result, image)
│   └── providers/
│       ├── anthropic/
│       ├── openai/
│       ├── gemini/
│       ├── ollama/
│       └── openrouter/     # Covers 200+ models via single endpoint
│
├── agent/                  # Layer 2: The loop
│   ├── agent.go            # Agent interface + BaseAgent
│   ├── loop.go             # Core agentic cycle: perceive -> decide -> act -> observe
│   ├── turn.go             # Single turn execution + result types
│   └── handoff.go          # Control transfer between agents
│
├── session/                # Layer 3a: Durable state
│   ├── session.go          # Session container (metadata + event log)
│   ├── event.go            # Strongly-typed append-only event types
│   ├── store.go            # Store interface (JSONL / SQLite / pluggable)
│   ├── jsonl.go            # Default JSONL file-backed store
│   ├── sqlite.go           # SQLite store with FTS5 search (opt-in)
│   └── trajectory.go       # Trajectory recording for RL/eval (structured trace export)
│
├── context/                # Layer 3b: Context engineering pipeline
│   ├── builder.go          # Processor chain: select -> transform -> inject
│   ├── processor.go        # ContextProcessor interface
│   ├── guard.go            # Token budget tracking, rot-threshold detection
│   ├── compactor.go        # Reversible compaction (offload tool results to filesystem)
│   ├── summarizer.go       # Lossy LLM summarization (triggered by rot threshold)
│   └── cache.go            # Append-only enforcement + KV-cache helpers
│
├── tool/                   # Layer 3c: Tool execution
│   ├── tool.go             # Tool interface + typed Schema helpers
│   ├── registry.go         # Lazy-loading tool registry
│   └── mcp/
│       ├── client.go       # MCP client (stdio + streamable HTTP)
│       ├── server.go       # MCP server primitives
│       └── validate.go     # Tool description quality checks + injection defense
│
├── skill/                  # Layer 3d: Progressive disclosure capability packages
│   ├── skill.go            # Skill interface (SKILL.md standard)
│   ├── registry.go         # Discovery, on-demand loading, eligibility filtering
│   ├── loader.go           # SKILL.md parser + YAML frontmatter
│   └── tool.go             # read_skill and manage_skill tools
│
├── runtime/                # Layer 3e: Session execution
│   ├── runner.go           # Runs an agent + session, manages the agentic loop
│   ├── lane.go             # Per-session serialization queue (the OpenClaw lesson)
│   └── hitl.go             # Human-in-the-loop primitives
│
└── memory/                 # Layer 3f: Start simple, grow intentionally
    ├── core.go             # Core Memory (Persona/State)
    ├── vector.go           # VectorStore interface + SQLite brute-force
    └── hnsw.go             # HNSW vector search (pure Go)

canto/x/                  # Extension packages (built on core)
├── graph/                  # DAG orchestration + conditional routing
├── swarm/                  # Decentralized multi-agent mesh coordination
├── eval/                   # Evaluation harness + trajectory scoring
├── obs/                    # OpenTelemetry, structured logging, dashboards
└── tools/                  # Standard tools: bash, code executor, search, etc.
```

---

## Core Interfaces

These are the 5 interfaces that everything else composes from. Keep them small.

```go
// Provider is the only thing that touches an LLM API.
// Everything else is pure Go.
type Provider interface {
    Complete(ctx context.Context, req Request) (*Response, error)
    Stream(ctx context.Context, req Request) (<-chan Delta, <-chan error)
    CountTokens(messages []Message) (int, error)
    ModelInfo() ModelInfo
}

// Tool is the unit of action. Every capability the agent has is a Tool.
// Schema returns JSON Schema (use jsonschema package for type safety).
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage
    Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

// Agent is a configured LLM with instructions, tools, and a step function.
// Step runs exactly one turn of the agentic loop.
type Agent interface {
    ID() string
    Instructions(ctx context.Context, session *Session) string // dynamic instructions
    Tools() []Tool
    Step(ctx context.Context, session *Session) (StepResult, error)
}

// ContextProcessor builds the LLM request from the session state.
// Processors are composed in order; each mutates the request.
// Convention: return early if nothing to do. Never mutate session state.
type ContextProcessor interface {
    Name() string
    Process(ctx context.Context, sess *Session, req *LLMRequest) error
}

// Store persists and retrieves sessions.
// The only valid operation on events is Append -- no update, no delete.
type Store interface {
    Get(ctx context.Context, sessionID string) (*Session, error)
    Create(ctx context.Context, session *Session) error
    AppendEvent(ctx context.Context, sessionID string, event Event) error
    Search(ctx context.Context, query string, limit int) ([]Session, error) // FTS
}
```

---

## The Event Log (Append-Only, Always)

This is the most important data structure in the framework. Get it right.

```go
// EventType enumerates all possible event kinds.
type EventType string

const (
    EventUserMessage    EventType = "user_message"
    EventAssistantText  EventType = "assistant_text"
    EventToolCall       EventType = "tool_call"
    EventToolResult     EventType = "tool_result"
    EventHandoff        EventType = "handoff"
    EventCompaction     EventType = "compaction"      // marks where summarization occurred
    EventContextOffload EventType = "context_offload" // marks what was written to filesystem
    EventCostSnapshot   EventType = "cost_snapshot"
)

// Event is a single immutable record in the session log.
// Once appended, an event is never modified.
type Event struct {
    ID        string          `json:"id"`         // ULID for sortability
    SessionID string          `json:"session_id"`
    AgentID   string          `json:"agent_id"`
    Type      EventType       `json:"type"`
    Timestamp time.Time       `json:"ts"`
    Content   json.RawMessage `json:"content"`    // typed by EventType
    Tokens    int             `json:"tokens"`
    Cost      float64         `json:"cost_usd"`
    Metadata  map[string]any  `json:"meta,omitempty"`
}

// Session is the container. Events is a read-only view after load.
// To add events, call Store.AppendEvent -- never mutate the slice directly.
type Session struct {
    ID          string         `json:"id"`
    AgentID     string         `json:"agent_id"`
    WorkspaceID string         `json:"workspace_id,omitempty"`
    CreatedAt   time.Time      `json:"created_at"`
    UpdatedAt   time.Time      `json:"updated_at"`
    Events      []Event        `json:"events"` // append-only, see above
    State       map[string]any `json:"state"`  // structured state (plan, todos, etc)
    TotalCost   float64        `json:"total_cost_usd"`
    TotalTokens int            `json:"total_tokens"`
}
```

**Why append-only is non-negotiable:**
- Any modification to earlier context invalidates the LLM KV cache from that point forward, silently multiplying API costs
- Trajectory replay for RL training requires a faithful record of what actually happened
- Audit logging, debugging, and time-travel inspection all require the full unmodified trace

---

## Context Engineering Pipeline

The pipeline is the bridge between the append-only session log and each LLM request.
Each processor is a pure function: `(session, request) -> error`. No side effects on session.

```go
// DefaultProcessors is the out-of-the-box pipeline.
// Replace or reorder any processor by modifying this slice on your Runner.
var DefaultProcessors = []ContextProcessor{
    &WorkspaceProcessor{},   // inject AGENTS.md / SOUL.md instructions
    &HistoryProcessor{},     // select + transform events -> messages
    &SkillListProcessor{},   // inject skill names/descriptions (not full content)
    &ToolListProcessor{},    // inject available tools (lazy: only if under budget)
    &TokenGuardProcessor{},  // check token budget, trigger compaction if needed
}

// TokenGuardProcessor monitors context budget.
// Compaction hierarchy (never jump levels):
//   1. Offload: write tool results / large payloads to filesystem, keep path in context
//   2. Summarize: LLM-compress the oldest N turns when offloading isn't enough
//   3. Hard stop: refuse to proceed if budget exhausted after both strategies
//
// Pre-rot threshold: 60% of model context window triggers action (not 95%).
// Context rot starts well before the window fills for complex tasks.
type TokenGuardProcessor struct {
    RotThresholdPct float64 // default: 0.60
    CompactStrategy CompactStrategy
}

// LLMRequest is assembled by the processor pipeline and sent to the provider.
// It is ephemeral -- built fresh every turn, never persisted.
type LLMRequest struct {
    Messages    []Message
    Tools       []Tool
    TokenBudget int
    Temperature *float64
    MaxTokens   *int
}
```

### Context Engineering Rules (baked into default processors)

1. **Append-only history**: processor reads events, never writes them
2. **Progressive skill disclosure**: inject skill list (names + 1-line descriptions only); agent reads full SKILL.md on demand via tool
3. **Lazy tool loading**: if > 20 tools available, present a `search_tools` meta-tool first; load definitions on demand
4. **Compaction is reversible first**: offload large tool results to `{workspace}/offload/{event_id}.json`, inject path; agent can re-read if needed
5. **Summarization is lossy and final**: only when offloading doesn't recover enough budget; keep last 3 turns raw to preserve model "rhythm"
6. **KV cache preservation**: system prompt is always the first message; never reorder or modify the message prefix; append only

---

## Provider Resilience: The Model Resolver

The Model Resolver is not optional for production. Key behaviors:

```go
type Resolver struct {
    Primary   ProviderConfig
    Fallbacks []ProviderConfig
    KeyPool   []string // rotate on 429/rate-limit
}

// ResolveProvider returns the next available provider.
// It handles:
//   - Key rotation: when a key hits rate limits, marks it as cooling down
//     and rotates to the next key in the pool
//   - Provider fallback: when all keys for a provider are cooling down,
//     falls back to the next provider in the chain
//   - Exponential backoff: per-key cool-down duration grows with repeated failures
//   - Cost tracking: accumulates cost across all providers in the session
func (r *Resolver) ResolveProvider(ctx context.Context) (Provider, error)

type ProviderConfig struct {
    Provider    string   // "anthropic", "openai", "gemini", "ollama", "openrouter"
    Model       string
    APIKeys     []string // pool; rotated on rate limit
    BaseURL     string   // for custom / self-hosted endpoints
    MaxTokens   int
    CostPerMTok float64  // for budget tracking
}
```

---

## Heartbeat: Autonomous Scheduling in Go (Cross-Platform)

Go's scheduling approach using `robfig/cron` v3 is **fully cross-platform** (Linux, macOS, Windows). It uses `time.AfterFunc` internally -- no OS cron dependency, no system permissions required.

```go
// Heartbeat drives proactive agent execution on a schedule.
// This is what separates an agent from a chatbot.
type Heartbeat struct {
    scheduler *cron.Cron
    runner    *Runner
    store     Store
    entries   []HeartbeatEntry
}

type HeartbeatEntry struct {
    ID        string
    Schedule  string          // "@every 5m", "0 9 * * *", "@daily", etc.
    AgentID   string
    SessionFn func() *Session // factory: create or resume a session
    MaxCost   float64         // budget guard: abort if session cost exceeds this
}

// Start begins all scheduled heartbeats.
// Uses signal.NotifyContext for graceful shutdown on SIGTERM/SIGINT.
func (h *Heartbeat) Start(ctx context.Context) error {
    h.scheduler = cron.New(cron.WithSeconds())
    for _, entry := range h.entries {
        e := entry // capture
        h.scheduler.AddFunc(e.Schedule, func() {
            sess := e.SessionFn()
            if sess.TotalCost >= e.MaxCost {
                return // budget exceeded, skip this tick
            }
            h.runner.Enqueue(ctx, e.AgentID, sess)
        })
    }
    h.scheduler.Start()
    <-ctx.Done()
    stopCtx := h.scheduler.Stop()
    <-stopCtx.Done()
    return nil
}

// RecoverMissedRuns handles process restarts: re-enqueues any missed
// runs within the catch-up window by reading last-run timestamps from the store.
func (h *Heartbeat) RecoverMissedRuns(ctx context.Context, window time.Duration) error
```

**Scheduling options supported:**
- `@every 5m` -- every 5 minutes
- `0 9 * * 1-5` -- 9am weekdays (standard cron expression)
- `@daily`, `@hourly`, `@weekly` -- natural aliases
- Duration-based via `time.Duration` for simple intervals
- Event-triggered via channel for reactive heartbeats (e.g., on new queue message)

Implementation note: `robfig/cron` v3 uses `time.AfterFunc` internally which the Go runtime implements correctly per OS. It does NOT spawn system cron jobs. Goroutines are used for execution -- extremely efficient, can run thousands on a single core.

---

## Lane Queue: Per-Session Serialization

The key insight from OpenClaw: free concurrency across sessions, strict serialization within a session.

```go
// LaneQueue provides per-session serial execution while allowing
// concurrent execution across different sessions.
// This prevents tool conflicts and state corruption within a session
// while maximizing throughput across the system.
type LaneQueue struct {
    mu    sync.Mutex
    lanes map[string]chan work // one buffered channel per session ID
}

type work struct {
    ctx     context.Context
    agentID string
    session *Session
    done    chan<- error
}

// Enqueue adds work to the session's lane. Work within the same session
// is processed serially. Work across different sessions runs concurrently.
func (q *LaneQueue) Enqueue(ctx context.Context, agentID string, session *Session) <-chan error

// Each lane has a dedicated goroutine. Lanes start lazily and shut down when idle.
func (q *LaneQueue) startLane(sessionID string) {
    ch := make(chan work, 64) // buffered: burst tolerance
    q.lanes[sessionID] = ch
    go func() {
        for w := range ch {
            err := q.runner.RunTurn(w.ctx, w.agentID, w.session)
            w.done <- err
        }
    }()
}
```

---

## Workspace-First Configuration

Agents load identity and configuration from the filesystem. This enables version control,
portability, and per-project customization without code changes.

```
{workspace}/
├── AGENTS.md         # Project instructions (cross-agent convention, like CLAUDE.md)
├── SOUL.md           # Agent identity: purpose, personality, constraints
├── TOOLS.md          # Available tool declarations for this workspace
├── HEARTBEAT.md      # Scheduled task definitions
├── skills/           # Local skill packages
│   ├── research/
│   │   └── SKILL.md
│   └── coding/
│       └── SKILL.md
└── offload/          # Context offload directory (tool results too large for context)
    └── {event_id}.json
```

```go
type WorkspaceConfig struct {
    Root          string   // absolute path to workspace directory
    AgentsMD      string   // content of AGENTS.md
    SoulMD        string   // content of SOUL.md
    SkillPaths    []string // discovered SKILL.md paths
    HeartbeatSpec string   // content of HEARTBEAT.md
}

// LoadWorkspace walks: current dir -> parent dirs -> ~/.canto/
// Mirrors how CLAUDE.md / AGENTS.md work in coding agents.
func LoadWorkspace(startDir string) (*WorkspaceConfig, error)
```

---

## Skills System: Progressive Disclosure

Skills are SKILL.md packages injected on demand -- **not** wholesale into every prompt.

```go
type Skill struct {
    Name        string   `yaml:"name"`
    Description string   `yaml:"description"` // one line, injected into skill list
    Tags        []string `yaml:"tags"`
    Path        string   // absolute path to SKILL.md
    FullContent string   // loaded on demand only
}

type SkillRegistry struct {
    skills []Skill
    loaded map[string]bool
}

// ListEligible returns the compact skill list for context injection.
// Only name + description. Never full content. This is what goes into the prompt.
func (r *SkillRegistry) ListEligible(agentContext string) []SkillSummary

// Load reads the full SKILL.md for a named skill.
// Called by the read_skill tool when the agent decides a skill is relevant.
func (r *SkillRegistry) Load(name string) (*Skill, error)

// The read_skill tool is registered automatically by SkillListProcessor:
//   Input:  { "skill_name": "research" }
//   Output: full SKILL.md content
// This is the mechanism for progressive disclosure.
```

---

## Memory Architecture

Start simple. Grow incrementally.

**Phase 1 (ship this):**
- JSONL append-only session store with full event history
- SQLite memory store with FTS5 for cross-session search
- Working memory = processor-managed context window slice

**Phase 2 (when needed):**
- Vector similarity search via OmenDB adapter
- Episodic -> semantic consolidation background process

**Phase 3 (research, not production yet):**
- Zettelkasten-style A-MEM interconnected note graph
- RL-driven memory management (MemRL pattern)

```go
// Memory is the external store interface -- intentionally minimal.
// The session event log handles in-session memory.
// This is for cross-session persistence.
type Memory interface {
    Store(ctx context.Context, entry MemoryEntry) error
    Search(ctx context.Context, query string, limit int) ([]MemoryEntry, error)
    Delete(ctx context.Context, id string) error
}

type MemoryEntry struct {
    ID        string
    SessionID string
    AgentID   string
    Content   string
    Embedding []float32 // nil until vector store is attached
    Tags      []string
    CreatedAt time.Time
    Score     float64 // populated on retrieval
}

// VectorStore interface -- defined now so OmenDB adapter can be plugged in later.
// Default: cosine similarity over SQLite (pure Go, brute force for small datasets).
// Future: CGo FFI to OmenDB (HNSW + ACORN-1 filtered search).
type VectorStore interface {
    Upsert(ctx context.Context, id string, vector []float32, metadata map[string]any) error
    Search(ctx context.Context, query []float32, limit int, filter map[string]any) ([]SearchResult, error)
    Delete(ctx context.Context, id string) error
}
```

---

## OmenDB Integration Plan

Your Rust embedded vector database with HNSW + ACORN-1 filtered search.

**Current state**: No Go bindings. Excellent algorithms, needs a bridge.

**Recommended integration path:**

**Step 1 (now)**: Implement `VectorStore` interface with pure-Go SQLite fallback.
Brute-force cosine similarity is fine for < 10k vectors. Ships immediately.

**Step 2 (parallel track)**: Build OmenDB sidecar adapter.
OmenDB exposes a local Unix socket server; Go client communicates via JSON-RPC.
Zero CGo, works on all platforms, OmenDB handles its own memory management.

```
OmenDB process <--Unix socket/HTTP--> canto OmenDBAdapter implements VectorStore
```

**Step 3 (when bindings are ready)**: CGo FFI.
Add `#[no_mangle] pub extern "C"` API surface to OmenDB Rust library.
The `VectorStore` interface means zero changes to framework consumers.

**ACORN-1 filtered search** is particularly valuable here -- search for memories filtered by agent_id, session_id, or time range alongside similarity. Design metadata schema:

```go
// Example OmenDB search with ACORN-1 filter:
results, _ := vectorStore.Search(ctx, queryEmbedding, 10, map[string]any{
    "agent_id":      "researcher-1",
    "session_id":    session.ID,
    "created_after": time.Now().Add(-24 * time.Hour),
})
```

---

## Trajectory Recording (RL-Readiness)

Every agent run should optionally record a structured trace. This is nearly free to add but impossible to retrofit later.

```go
// TrajectoryWriter writes execution traces alongside the session log.
// Output: {workspace}/trajectories/{session_id}.jsonl
type TrajectoryWriter struct {
    path string
    w    *bufio.Writer
    mu   sync.Mutex
}

type TrajectoryEntry struct {
    StepID     string          `json:"step_id"`
    SessionID  string          `json:"session_id"`
    Turn       int             `json:"turn"`
    Input      string          `json:"input"`       // context sent to model
    Output     string          `json:"output"`      // model response
    ToolCalls  []ToolCallTrace `json:"tool_calls"`
    Tokens     int             `json:"tokens"`
    Cost       float64         `json:"cost_usd"`
    DurationMs int64           `json:"duration_ms"`
    Score      *float64        `json:"score,omitempty"` // set by offline scorer
    Tags       []string        `json:"tags,omitempty"`
}

// Enable on a Runner:
//   runner.WithTrajectoryWriter(trajectory.NewWriter(workspace.Root))
```

---

## RLM Integration: Long Context as Environment

The Recursive Language Model pattern belongs as a first-class execution mode.
When inputs exceed the model's effective context window, RLM outperforms compaction and retrieval.

```go
// RLMAgent wraps a standard agent with an external REPL environment.
// Instead of feeding long inputs into context, the agent writes code
// to inspect/slice/process the input, recursively spawning sub-agents.
//
// Key insight: long inputs are environment objects the agent interacts
// with programmatically -- not tokens to be stuffed into context.
type RLMAgent struct {
    base          Agent
    repl          REPLTool
    maxReplOutput int // truncate REPL output shown to agent (default: 8192 chars)
}

type REPLTool struct {
    InputVar string // "input_data" -- the variable name accessible in REPL
    Input    []byte // actual data (may be 10MB+, never touches context directly)
    SpawnFn  func(prompt string) (string, error) // recursive sub-agent call
}

// Usage:
//   agent := canto.NewRLMAgent(baseAgent, largeInputData)
//   result := runner.Run(ctx, agent, session)
//
// System prompt injected: "input_data is available as a Python variable.
// Use the REPL to inspect and process it. Call spawn_agent(prompt, excerpt)
// to delegate work to a sub-agent on a relevant portion of the data."
```

---

## Multi-Agent Patterns

### Handoff (core)

```go
// Handoff is a tool call -- the LLM decides when to hand off.
// The framework handles mechanics: save state, switch agent, continue loop.
type HandoffResult struct {
    TargetAgentID string
    Reason        string
    Context       string // information to pass to the receiving agent
}

func IsHandoff(result StepResult) (*HandoffResult, bool)
```

### Graph Orchestration (x/graph)

```go
// Graph is a directed acyclic graph of agents with conditional routing.
// Orchestration is ALWAYS deterministic Go code -- never LLM-decided flow.
type Graph struct {
    nodes map[string]Agent
    edges []Edge
    entry string
}

type Edge struct {
    From      string
    To        string
    Condition func(result StepResult) bool // pure Go
}

func (g *Graph) Run(ctx context.Context, session *Session) (StepResult, error)
```

### Swarm (x/swarm)

```go
// Swarm coordinates a decentralized mesh of agents via a shared blackboard.
// No central orchestrator. Agents post observations and claim tasks.
// Coordination emerges from shared state, not explicit messaging.
type Swarm struct {
    agents     []Agent
    blackboard Blackboard
    maxRounds  int
}

type Blackboard interface {
    Post(ctx context.Context, agentID string, key string, value any) error
    Read(ctx context.Context, key string) (any, error)
    ClaimTask(ctx context.Context, agentID string, taskID string) (bool, error)
    ListUnclaimed(ctx context.Context) ([]Task, error)
}

func (s *Swarm) Run(ctx context.Context) (SwarmResult, error)
```

### Ralph Wiggum Loop (x/graph)

```go
// CycleRunner implements the "Ralph Wiggum" pattern:
// run an agent repeatedly with hard context resets until a plan is satisfied.
// Progress communicated via files (plan.md, progress.md), not context.
// For long-horizon tasks exceeding a single context window.
type CycleRunner struct {
    Agent     Agent
    PlanFile  string  // persists goals across context resets
    MaxCycles int
    CheckFn   func(planPath string) (bool, error) // returns true when done
}

func (c *CycleRunner) Run(ctx context.Context) error
```

---

## Autoresearch Pattern: program.md + Experiment Loop

The autoresearch insight (Karpathy, March 2026) generalizes to any optimization loop with a measurable metric.

```go
// ExperimentRunner implements the autoresearch pattern:
//   1. Load program.md (natural language spec: what to optimize and how)
//   2. Agent modifies the target file (code, config, prompt, etc.)
//   3. Run fixed-duration experiment, measure metric
//   4. If improved: commit (git) and continue
//   5. If worsened: revert and continue
//   6. Loop until budget exhausted or convergence
//
// Git is memory: append-only experiment log in {workspace}/autoresearch.jsonl
// Each experiment runs on its own branch; merged on success.
// Fixed time budget makes experiments directly comparable (key design decision).
type ExperimentRunner struct {
    ProgramMD  string                    // path to program.md spec
    TargetFile string                    // file the agent is allowed to modify
    MetricFn   func() (float64, error)   // measure success
    TimeBudget time.Duration             // fixed duration per experiment
    Agent      Agent
    LogPath    string                    // append-only JSONL experiment log
}

func (e *ExperimentRunner) Run(ctx context.Context) (*ExperimentReport, error)
```

---

## The Context Processor DX: Resolved

**Answer: Go's `http.Handler` middleware pattern, with batteries-included defaults.**

```go
// ProcessorChain is a slice of processors applied in order.
// Idiomatic Go: small interfaces + composition, no ceremony.
type ProcessorChain []ContextProcessor

// DefaultChain works out of the box. Override any processor by
// replacing it in the slice.
func DefaultChain(opts ...ChainOption) ProcessorChain {
    return ProcessorChain{
        &WorkspaceProcessor{},
        &HistoryProcessor{MaxEvents: 200},
        &SkillListProcessor{},
        &ToolListProcessor{LazyThreshold: 20},
        &TokenGuardProcessor{RotThresholdPct: 0.60},
    }
}

// Customization is just slice manipulation:
//
//   chain := canto.DefaultChain()
//   chain[2] = &MyCustomSkillProcessor{}  // replace
//   chain = append(chain, &RAGProcessor{retriever: myRetriever}) // add
//   runner := canto.NewRunner(agent, session, canto.WithProcessors(chain))
//
// No magic. No registration. No plugin system.
// Any Go developer understands this immediately.
```

Why this works:
- Default chain is convention -- ignore it entirely and it works
- Any processor is replaceable at the slice position
- Adding a processor is literally `append()`
- The interface is 2 methods (Name + Process) -- easy to implement
- No global state, no init() calls, no framework-specific patterns

---

## MCP Integration

```go
// MCPClient connects to one MCP server and exposes its tools.
type MCPClient struct {
    name      string
    transport Transport // stdio or streamable HTTP
    tools     []Tool    // populated on Connect()
}

// Security: validate all tool descriptions on connect.
// Known injection vectors: instructions embedded in descriptions,
// cross-server tool shadowing, silent definition mutation.
// Refuse tools with:
//   - Instructions directed at the LLM in the description field
//   - Names that shadow existing registered tools
//   - Schemas requesting irreversible operations without explicit annotation
func (c *MCPClient) Connect(ctx context.Context) error

// MCPServer exposes Go tools over the MCP protocol.
// Use to make your agent's capabilities available to other agents (A2A).
type MCPServer struct {
    tools     []Tool
    transport Transport
}
func (s *MCPServer) Serve(ctx context.Context) error
```

---

## Observability

Built into the runner, not a plugin.

```go
// Follows OpenTelemetry semantic conventions for Gen AI (gen_ai.*).
const (
    AttrModel        = "gen_ai.request.model"
    AttrInputTokens  = "gen_ai.usage.input_tokens"
    AttrOutputTokens = "gen_ai.usage.output_tokens"
    AttrCost         = "gen_ai.usage.cost_usd"
    AttrAgentID      = "canto.agent.id"
    AttrSessionID    = "canto.session.id"
    AttrTurn         = "canto.turn"
    AttrCompacted    = "canto.context.compacted"
)

// Spans emitted per turn:
//   canto.turn           -- full turn duration
//     canto.context      -- context pipeline processing
//     gen_ai.chat          -- LLM API call
//     canto.tool.{name}  -- each tool execution
//
// Configure via standard OTEL env vars (OTEL_EXPORTER_OTLP_ENDPOINT, etc.)
```

---

## Channel Adapters

One agent, many channels. Separation of interface from intelligence.

```go
type ChannelAdapter interface {
    Name() string
    Listen(ctx context.Context, out chan<- IncomingMessage) error
    Send(ctx context.Context, sessionID string, msg OutgoingMessage) error
}

type IncomingMessage struct {
    ChannelID string
    SessionID string  // derived from channel-specific identifiers
    UserID    string
    Content   Content // text, image, audio, file
    Raw       any
}

// Built-in adapters (in x/channel):
//   - HTTP: REST API + SSE streaming
//   - CLI: stdin/stdout
//   - Webhook: Slack, Discord, Telegram, etc.
```

---

## Anti-Patterns to Avoid

Document these for contributors:

- **LLM-decided flow control**: never use an LLM to route between agents unless explicitly implementing a router (always have a deterministic fallback)
- **Mutating session events**: if you want to correct something, append a correction event; never modify existing events
- **Loading all tools upfront**: when > 20 tools available, always use lazy loading
- **Aggressive summarization too early**: always try filesystem offload first; summarization is lossy and irreversible
- **Modifying early context for any reason**: invalidates KV cache and silently multiplies costs
- **Shared context across sub-agents without isolation**: "share memory by communicating; don't communicate by sharing memory"
- **Prompt-based orchestration**: "when you're done, send results to the reviewer" will fail unpredictably in production

---

## What to Research Next

Before finalizing implementation, review these:

1. **Letta (MemGPT)** -- `core_memory` / `archival_memory` separation is worth borrowing for the memory package
2. **Beads (Steve Yegge)** -- git-backed cross-session persistent task tracking maps directly to `session/state`
3. **A2A Protocol (Google)** -- Agent-to-Agent communication standard; implement as transport in `x/channel`
4. **ag-ui protocol** -- standardized agent-user interaction events (SSE-based); useful for HTTP channel adapter
5. **Voyager** -- self-expanding skill library via RL; skill library building pattern applies directly to `skill/`
6. **SWE-agent ACI** -- Agent-Computer Interface patterns for software engineering use case
7. **DSPy typed predictors** -- programmatic LLM query construction; useful for context processor pipeline
8. **Cache-to-Cache** (experimental) -- direct KV cache sharing between agents; watch this space
9. **OpenTelemetry Gen AI semantic conventions** -- standardizing span attributes; align before stabilizing obs package
10. **SkillsBench** -- benchmark for SKILL.md packages; reference for testing skill registry

---

## Minimum Viable Implementation Sequence

### Phase 1: Core Loop (1-2 weeks)
1. `llm/provider.go` + `llm/providers/openai/` + `llm/providers/anthropic/`
2. `session/event.go` + `session/session.go` + `session/jsonl.go`
3. `agent/agent.go` + `agent/loop.go`
4. `runtime/runner.go` (basic, no lane queue yet)
5. `tool/tool.go` + `tool/registry.go`

**Test gate**: single agent, single bash tool, session persists to JSONL

### Phase 2: Production Reliability (1-2 weeks)
1. `llm/resolver.go` -- provider fallback + key rotation
2. `runtime/lane.go` -- per-session serialization
3. `context/builder.go` + `context/guard.go` + `context/compactor.go`
4. `llm/cost.go` -- cost tracking + budget enforcement
5. `session/sqlite.go` -- SQLite store with FTS5

**Test gate**: multi-turn agent with context management; rate limit simulation

### Phase 3: Runtime Features (Done)
1. `skill/` -- SKILL.md progressive disclosure + management tools
2. `runtime/hitl.go` -- InputGate and human-in-the-loop primitives
3. `runtime/runner.go` -- Subscribe() and real-time event streaming
4. `tool/mcp/` -- Full JSON-RPC 2.0 client/server support
5. `session/trajectory.go` -- Trajectory recording for eval/RL

**Test gate**: agent that uses a skill to learn a new tool, executes it, and streams progress to a subscriber.

### Phase 4: Multi-Agent & Memory (Done)
1. `agent/handoff.go` -- Structural control transfer
2. `x/graph/` -- Deterministic DAG orchestration
3. `x/swarm/` -- Blackboard mesh coordination
4. `memory/hnsw.go` -- Pure Go vector search with WAL durability
5. `x/eval/` -- Trajectory scoring harness over event logs

---

## Module

```
module github.com/nijaru/canto

go 1.26

require (
    github.com/robfig/cron/v3 v3.0.1     // cross-platform scheduling
    modernc.org/sqlite v1.x               // pure Go SQLite with FTS5
    github.com/oklog/ulid/v2 v2.x         // sortable IDs for events
    go.opentelemetry.io/otel v1.x
    github.com/invopop/jsonschema v0.x    // JSON schema generation from Go types
)
```

SQLite note: `modernc.org/sqlite` (pure Go) is recommended over `mattn/go-sqlite3` (CGo)
for cross-platform simplicity. FTS5 is supported. Performance is comparable for this use case.

---

## Known Tradeoffs and Hard Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Session storage default | JSONL files | Portable, inspectable, no dependencies. SQLite is opt-in. |
| Scheduling | robfig/cron (pure Go) | Cross-platform, no OS permissions, goroutine-based |
| SQLite binding | modernc.org/sqlite (pure Go) | No CGo toolchain requirement; easier cross-compilation |
| Context rot threshold | 60% (not 95%) | Complex tasks degrade earlier; conservative is safer |
| Tool loading | Lazy when > 20 tools | Loading all tool schemas consumes significant context budget |
| Vector DB default | SQLite brute force | OmenDB sidecar when bindings available; interface makes swap trivial |
| Orchestration | Always deterministic Go | LLMs are unreliable routers; flow control belongs in code |
| Compaction order | Offload -> Summarize | Offload is reversible; summarization is not; never skip to summarize |

---

*Spec version: 0.1 — March 2026*
*Reviewed: after Phase 2 implementation, to validate interface decisions against real usage*
