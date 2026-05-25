---
date: 2026-05-23
summary: Canto framework support plan for Ion's reopened Pi-first P1 stabilization
status: active
---

# Ion P1 Stabilization Support

## Trigger

Ion dogfood exposed first-minutes failures after earlier closure claims:

- valid absolute in-workspace file discovery was rejected;
- long exploratory turns failed with `context deadline exceeded`;
- settings and TUI shell-frame behavior still had active-turn regressions.

The direct UI/tool bugs are Ion-owned, but the framework question is broader
than the first two fixes. Any Ion gap in session ownership, ordered events,
provider-visible context, durable replay, tool lifecycle/results,
queue/steer/follow-up, compaction, or timeout/error surfacing is Canto design
evidence until proven Ion-product-specific.

## Canto+Ion Decision

Keep Canto and Ion together, but make Ion the Phase 1 acceptance owner.

Ion should not become fully standalone by default. A standalone rewrite would
avoid current Canto friction for a short time, but it would also duplicate the
runtime/session/tool lifecycle and make later extraction risky. Canto should
instead behave like an unstable embedded kernel: small, breakable, and judged
by whether Ion can pass the Pi-like scenario matrix.

Decision rules:

- reusable runtime/session/tool primitives stay in Canto only when Ion needs
  them and Ion acceptance proves them;
- product UX, local settings, slash commands, pickers, and display projection
  stay in Ion;
- if Canto blocks Ion P1, Ion may implement a clean local path and Canto tracks
  the re-extraction, simplification, or deletion decision;
- no public Canto release posture resumes until Ion P1 primitive acceptance is
  green again or remaining failures are proven product-only.

## Pi-Level Reference

Pi's harness carries cancellation through provider and tool work and uses
narrow operation-level timeouts. The core turn is not killed by a short hidden
global wall-clock deadline.

## Canto Work

- `canto-x8d0`: decide and implement the correct root harness/runtime timeout
  behavior for normal interactive hosts.
- `canto-y88u`: audit workspace/path contracts and document/test the behavior
  host tool authors should rely on.
- Keep `canto-98el` aligned with Ion's queue/steer/follow-up and settled-state
  needs.
- `canto-iusu`: reduce Canto to the Ion-proven P1 kernel before M1; re-extract
  only primitives that Ion has proven through acceptance.
- Reopened primitive audit: classify Ion's ideal-first controller, projection,
  terminal-commit, event-adapter, tool-runtime, timeout, and scenario-gate gaps
  as Canto primitive or Ion product work before M1 docs resume.

## Non-Goals

- Do not implement Ion product UX in Canto.
- Do not resume M1 release docs as the main path while Ion's P1 primitive
  acceptance is reopened.
- Do not promote Pi+ research unless it identifies a concrete P1 primitive gap.
