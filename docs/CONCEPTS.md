# Canto Concepts

Canto is built around five core primitives that distinguish it from standard LLM libraries.

## 1. The Append-Only Event Log (Session)

Unlike simple message arrays, a Canto `Session` is a durable, append-only log of `Event` objects. This log records:
- **Assistant/User messages**
- **Tool calls and results**
- **Context mutations** (Pruning, Summarization, Offloading)
- **Runtime events** (Step/Turn starts/completions, usage, cost)

This ensures perfect session durability and enables **Checkpoint/Restore** or **Rewind** capabilities.

## 2. Phased Context Construction (Builder)

Canto separates *reading* state from *modifying* state.
- **Preview Phase**: The `context.Builder` creates an `llm.Request` by applying a chain of `RequestProcessor` functions.
- **Commit Phase**: After the model responds, `ContextMutator` functions record the interaction and any state changes back to the session.

## 3. Speculative Workspace (OverlayFS)

For coding and system-level agents, Canto provides a symlink-safe, rooted `WorkspaceFS`.
- **OverlayFS**: A virtual layer that buffers file writes in memory.
- **Plan Mode**: Agents can "execute" tool calls speculatively. The host can then diff the overlay against the base filesystem before calling `Commit()` or `Discard()`.

## 4. Federated Memory (Ingest & Link)

Canto distinguishes between **Episodic Memory** (past events) and **Semantic Memory** (structured knowledge).
- **Ingest**: Background processes compile raw research into a structured Markdown Wiki.
- **Link**: Entities are interconnected via a Zettelkasten-style network for high-density context retrieval.

## 5. Capability-First Agent Loop

The `agent.Run` loop is a Go 1.23 iterator (`iter.Seq2`) that yields structured events.
- **Backpressure**: The loop only progresses when the consumer (UI/TUI) pulls.
- **Escalation**: Tool errors trigger a configurable escalation ladder before reaching the user.
- **Withholding**: Recoverable errors are handled internally to keep the user context clean.
