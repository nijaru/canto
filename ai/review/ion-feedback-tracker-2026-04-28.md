---
date: 2026-04-28
summary: Confirmed Ion-derived Canto framework feedback.
status: active
---

# Ion Feedback Tracker

Use this as the single active place for Ion findings that require Canto framework work. Ion product tasks, TUI polish, provider config, ACP polish, and feature planning stay in the Ion repo.

## Handoff Rule

- Log an item here only after Ion identifies a concrete Canto primitive, contract, or API issue.
- Create or update a Canto `tk` task for any open item.
- Do not mirror Ion's roadmap, core-loop task list, or P2/P3 feature plans in Canto.
- Resolved items stay here only as a compact history so future agents do not reopen stale issues.

## Open Confirmed Framework Issues

None as of 2026-04-28. Ion's active core-loop review is currently looking for more, but the known framework-owned failures below are resolved.

## Resolved Ion-Derived Fixes

| Area | Resolution | Notes |
| --- | --- | --- |
| Empty/no-payload assistant rows in effective history | Canto effective history filters invalid assistant rows from raw history, snapshots, and appended events. | Projection sanitation is a legacy/corrupt-history defense. |
| Future whitespace-only assistant writes | Canto write-side assistant payload validation rejects content/reasoning that trims to empty while preserving tool-only and reasoning/thinking-only assistant payloads. | Prevents future invalid provider history at the source. |
| Mid-conversation privileged messages | Prompt/session boundary now separates transcript, model-visible context, and hidden events; provider request validation rejects privileged messages after transcript messages. | Fixed the Fedora/local-api `System message must be at the beginning` failure class without promoting UI notices into system prompts. |
| Canceled turns missing terminal durability | Streaming and non-streaming canceled turns persist terminal `TurnCompleted` events. | Lets Ion resume canceled sessions without relying on app-local state. |
| Failed tool result text | Tool completion events carry structured error text. | Ion can render/replay failed tool results without parsing display strings. |
| Queue wait timeout leaking into execution | Canto local serial queue separates wait timeout from execution context. | Prevents queued-turn wait deadlines from canceling active or later model turns. |
| Retry-until-cancel primitive | Retry provider supports transport-only retry until context cancellation with retry callbacks. | Ion owns user-facing retry status; Canto owns retry mechanics. |

## Watch List

- Provider-history shape after Ion's next lifecycle pass: only reopen here if Canto can still construct invalid provider requests from valid session state.
- Compaction/context primitives after Ion's next resume/continue smoke: only reopen here if a Canto projection or prompt-boundary contract is wrong.
- Retry classification after the next live provider smoke: only reopen here if Canto misclassifies transport/provider failures in a way Ion cannot fix at the product layer.
