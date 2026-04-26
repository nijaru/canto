# Canto Concepts

Canto is built around five primitives that standard LLM libraries usually leave to the host.

## 1. The Append-Only Event Log (Session)

Unlike a message array, a Canto `Session` is an append-only log of `Event` objects. This log records:
- **Assistant/User messages**
- **Tool calls and results**
- **Context mutations** (pruning, summarization, offloading)
- **Runtime events** (Step/Turn starts/completions, usage, cost)

This gives hosts durable replay, checkpoint/restore, and rewind without treating model-visible messages as the source of truth.

## 2. Phased Context Construction (Builder)

Canto separates *reading* state from *modifying* state.
- **Preview phase**: `prompt.Builder` creates an `llm.Request` by applying `RequestProcessor` functions.
- **Commit phase**: after the model responds, `ContextMutator` functions record the interaction and any state changes back to the session.

## 3. Speculative Workspace (OverlayFS)

For agents that touch files, Canto provides a symlink-safe, rooted `WorkspaceFS`.
- **OverlayFS**: A virtual layer that buffers file writes in memory.
- **Plan mode**: agents can run tool calls speculatively. The host can then diff the overlay against the base filesystem before calling `Commit()` or `Discard()`.

## 4. Federated Memory (Ingest & Link)

Canto distinguishes between **episodic memory** (past events) and **semantic memory** (structured knowledge).
- **Ingest**: background processes compile raw notes into structured memory.
- **Link**: related records can point to each other for dense retrieval.

## 5. Capability-First Agent Loop

The `agent.Run` loop is a Go 1.23 iterator (`iter.Seq2`) that yields structured events.
- **Backpressure**: the loop only progresses when the host pulls.
- **Escalation**: tool errors can pass through a configurable recovery path before reaching the user.
- **Withholding**: recoverable errors can stay out of the visible conversation.
