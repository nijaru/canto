# Native Yrs qualification

**Result (2026-10-31): 14 tests pass**, including assertions of engine limitations.
This qualifies the tested native text/history behaviors, not Canto's application,
storage, editor integration or a stable persistence protocol.

## Reproduce

Runtime: native **yrs =0.28.0**, default Cargo features; **rustc 1.99.1
(58cf8f9c3 2026-10-13)**, **cargo 1.99.1 (5f94df478 2026-08-27)**,
Clippy 0.1.99, `aarch64-apple-darwin`, Darwin 25.6.0. Dependencies are resolved by
`Cargo.lock`. No JavaScript engine or editor dependency participates.

From the repository root:

```sh
cargo fmt --all -- --check
cargo clippy --workspace --all-targets --locked -- -D warnings
cargo test --locked --test yrs_qualification -- --nocapture
cargo test --workspace --locked
cargo build --workspace --locked
```

All checks above passed. Focused result:

```text
test result: ok. 14 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out
```

[The tests](../tests/yrs_qualification.rs) call Yrs directly, without application
helpers. Every document uses `skip_gc: true` (GC=false), byte offsets and V1
updates. Diverging writers have distinct client IDs; imports retain original
item IDs and use an excluded `remote` undo origin. Grouping uses a fixed clock,
not sleeps. Reload means encoding state, then decoding into a fresh native
`Doc`; several cases also write/read temporary files. It is not a process-crash
or filesystem-durability test.

## Observations

| Case | Native result |
| --- | --- |
| Independent insertion/deletion | Two writers editing `abcd` converge to `aXYd`. All 24 delivery orders of four updates, followed by duplicate delivery, produce equal text and snapshot descriptors with no pending data. |
| Replay and causal identity | Delete/reinsert of `x` yields equal bytes but different item IDs and snapshot descriptors. Full-state reload preserves IDs; an earlier pin reconstructs the original `x`, including its original ID. A fresh writer can continue the reloaded document. |
| Missing prerequisites | Out-of-order insertions and deletions remain pending. Full-state export/reload retains them; delivery of the prerequisite produces the source's text and snapshot descriptor. |
| Forks and selection | Separate documents retain independent results. Importing only a causally complete selected prefix excludes later and other-workstream edits, including after reload. A later same-writer update alone remains pending until its prefix arrives. This is not arbitrary hunk selection. |
| Scoped undo/redo | Two origin-scoped managers preserve each other's text, imported text and out-of-scope metadata. Undo/redo of local `abc` around remote interior `X` gives `aXbc → X → aXbc` on both replicas. |
| Grouped replacement | Grouped insertion of `x` then replacement by `y` undoes to empty and redoes to `y`; remote metadata survives. The metadata writer first observes `x`. This tests that causal observation, not an encoded cross-type dependency: the unrelated map operation has no structural reference to `x`. |
| Double deletion | Both writers delete original `b` from `abc`. Undoing A restores `abc` on both replicas with a fresh A-authored `b`; the original ID stays deleted. Redo returns both to `ac`. It does **not** keep B's independent deletion intent effective against the new item. |
| Ordinary anchors | Serialized `Assoc::After` anchors around `fox` shift from `[2,5]` to `[3,6]` after a prefix insertion and retain those positions on reload. They do not resolve in an unrelated document. |
| Deleted-range anchors | Deleting `fox` collapses endpoints to `[2,2]`. Undo restores text everywhere, but old anchors resolve to `[2,5]` locally and `[5,5]` on the synchronized peer and full-state reload. Reconstructing the original pinned history resolves them to `[2,5]`. Local undo repair is not durable shared-range identity. |
| UndoManager recreation | Document state survives; undo/redo stacks do not return when constructing a new manager, either on the same document or after reload. No separate stack-persistence mechanism is qualified. |
| Input decoding | Every strict prefix of the fixture update, two malformed fixtures and an invalid-UTF-8 text payload return decode errors. A valid update with appended garbage is accepted. These bounded cases do not establish arbitrary-corruption detection or hostile-input resource safety. |
| Exact text bytes | BOM, NUL, combining marks, multibyte characters, tabs and CRLF survive native replication, a byte-indexed edit, full-state file reload and historical reconstruction without normalization. Non-UTF-8 files and invalid coordinate boundaries are not qualified. |

## Application boundaries

- Allocate independent writer epochs for diverging replicas; preserve imported IDs.
  Equal source bytes are not equal history.
- Keep workstream admission separate from CRDT union. The tested selection is a
  complete causal prefix, not a general task-integration or cherry-pick algorithm.
  Retain observed causal parents in application batches when required; an
  unrelated map update does not encode its author's read dependency on text.
- Retain full state/history alongside snapshot descriptors. Use
  `encode_state_as_update_v1` for the tested pending-data roundtrip; do not assume
  every diff/snapshot API preserves pending custody. GC-enabled retention is untested.
- Give editing sessions explicit undo origins and group boundaries. Advertise
  restorative deletion undo, not identity-preserving or independent-delete undo.
  Do not advertise restart-durable undo from document persistence alone.
- Keep historical review references pinned to document/revision/range. A live
  resolved anchor alone cannot recover the tested deleted range after reload.
- Validate byte coordinates at the application boundary. Treat non-UTF-8 content
  separately. Add a bounded, versioned, integrity-checked envelope and enforce
  complete framing before applying decoded updates; Yrs decoding is not a checksum.

No failing baseline assertion remains. The double-deletion, anchor-repair,
UndoManager-restart and trailing-byte behaviors are explicit limitations, not
ignored tests or convergence failures. No performance, pruning, codec-upgrade,
remote-sync protocol, live-editor undo or filesystem atomicity claim follows.
