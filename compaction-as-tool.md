# Expose Compaction as a Tool

**Date:** 2026-03-29
**Status:** Idea

## The Feature

Give the agent a `compact` tool so it can self-initiate context compaction at semantic boundaries (topic shifts, task completions) — rather than waiting for token limits or a user-initiated `/compact`.

```
compact(reason: string, preserve: []string)
```

The agent decides when stale context hurts more than the compaction costs. This is the inverse of how every current agent works — they treat compaction as infrastructure, not a model judgment.

## Arguments For Putting It in Canto

1. **Reusability** — Any app built on canto gets this for free. ion, pi, OpenCode, etc.
2. **The mechanism lives there** — `Summarize`, context truncation, session summarization are all canto internals.
3. **Cleaner ion** — ion just registers the tool and handles guardrails/transcript rendering.

## Arguments Against

1. **Policy is ion** — guardrails (min turns, min tokens, never mid-tool-call), user visibility, and transcript presentation are ion UX.
2. **System prompt injection** — the guidance for *when* to compact is ion-specific, injected at the tool-description level.
3. **Config ownership** — guardrail thresholds are ion config.

## Proposal

Split the difference:

| Layer | Responsibility |
|-------|---------------|
| **Canto** | `CompactTool` — validates session is summarizable, calls `runner.Summarize()`, returns structured summary. |
| **Ion** | Registers the tool, injects system prompt guidance, enforces guardrails, renders `♻ Compacted: <reason>` in transcript. |

This keeps the mechanism in the framework and the policy in the app. The tool definition itself could live in canto as a primitive, with ion contributing the trigger guidance and guardrails.

## Open Questions

- Does the agent need token usage visibility to make good decisions?
- Should `preserve` hints influence summarization or just the next user-facing message?
- Session-level opt-out: `ION_NO_AUTO_COMPACT`?
