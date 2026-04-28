# Ion Framework Issues

This file is retained as a legacy pointer. Active Ion-derived Canto feedback now lives in [review/ion-feedback-tracker-2026-04-28.md](review/ion-feedback-tracker-2026-04-28.md).

Do not add new issues here. Add confirmed framework issues to the tracker and create or update a Canto `tk` task.

## Resolved Legacy Issues

- Effective history now sanitizes empty/no-payload assistant rows, including legacy rows and snapshot-derived rows.
- Assistant write-side validation now prevents future whitespace-only assistant rows while preserving tool-only and reasoning/thinking-only assistant messages.
- Prompt/session boundaries now prevent mid-conversation system/developer messages from entering provider history; durable UI/status events must not become privileged model instructions.
