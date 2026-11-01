# Experimental source continuity

This documents the implemented headless milestone, not a stable protocol.
The CLI and Rust library use the same `Workspace` application operations.

## Attach and retain

`attach` requires a Git repository root. It retains two immutable revisions:
the declared HEAD tree (empty for an unborn branch), then observed working
files. `main` starts at the observed revision.

Capture includes tracked paths and nonignored untracked files. It excludes Git
internals, `.canto` directories and the selected store directory, including a
custom store inside the project. Ignored untracked files and unknown editors'
unsaved buffers are not captured. The Git index/staging distinction is not
retained separately.

Git blobs and working files are separate representations: no clean/smudge
filters or EOL conversions are run. Capture neither executes Git hooks or
fsmonitor nor resets files, modifies the index or writes refs. A detected HEAD
transition rejects attachment. A multi-file scan is **not** an atomic snapshot
under uncontrolled writes.

Regular files preserve bytes and their executable flag. Symlinks preserve
target bytes, including non-UTF-8 targets, without following them. On Unix,
descriptor-relative traversal enforces no-follow at every path component and
checks opened file types. Non-UTF-8 regular content is opaque retained data;
only UTF-8 regular files are editable through the text API.

Current limits: 16 MiB per file, 10,000 files and 32 MiB of raw capture per
manifest, plus a 64 MiB encoded-record ceiling. Encoding overhead can reject a
smaller capture. Submodules, special files, non-UTF-8 paths and backslash paths
are explicitly unsupported. Names and symlink targets are not normalized.
Ownership, extended attributes, hardlink relationships and full permission
bits are not retained.

Document IDs persist across the initial same-path working import. A changed
text import preserves the unchanged prefix/suffix and authors a replacement
for the middle; it does not infer an unseen edit sequence or rename lineage.
Metadata-only changes do not reauthor text.

## Workstreams and edits

`fork REVISION NAME` pins a source head only; it provisions no checkout.
Reading a revision replays only its inclusion history, never every batch in
the database. Sibling workstreams remain independent.

`edit WORKSTREAM --base REVISION ...` accepts an array of
`{path,start,end,insert}` edits. Coordinates are UTF-8 **byte** offsets, checked
at character boundaries. Edits are sequential within the array. The entire
batch commits only if the workstream still equals the expected revision.
An invalid operation or stale base leaves all source and heads unchanged.

A successful request records its revision, workstream head and receipt in one
transaction. Repeating the same client/request identity and input returns the
committed receipt even if the head has advanced. Different input with an
already committed identity fails. Rejected calls do not consume a request ID.

`author REVISION --actor ACTOR --edits FILE` produces an immutable local causal
batch without changing source. Independent processes get separate writer
epochs and engine client IDs. `admit WORKSTREAM BATCH_FILE --client CLIENT
--request ID` explicitly admits collaborative edits based on an already
included revision, even if the head has advanced.

Admission retains the writer's original base separately from the current
source-inclusion parent. Native payloads are regenerated from declared edits,
writer and original base and compared exactly before application. Altered
payloads, cross-workspace inputs, unresolved bases and reused writers are
rejected. Retry the original request identity after an uncertain success;
a new request identity does not reuse an admitted writer.

This is collaboration within a selected workstream, **not** reviewed
integration of independent task results. There are no proposal semantics,
arbitrary-history joins or hunk cherry-picks yet. Actor names are attribution,
not authenticated identities. No network endpoint is exposed.

## Persistence and restart

One SQLite store contains source records, workstream heads, writer reservations
and committed request receipts. Immediate transactions serialize commits
across local clients; WAL and synchronous FULL are enabled. There is no
coordinator daemon yet.

Records bind workspace, experimental codec, writer, inclusion parent, original
causal base and native payloads. SHA-256 revision identities cover the encoded
records. Equal bytes do not imply equal revision or causal identity. Source
records are append-only; heads are projections. Unknown schema/codec versions
and mismatched source hashes are rejected rather than silently migrated.

Text uses **Yrs 0.28.0**, V1 updates, byte coordinates and GC disabled. Retained
native history is replayed, not reauthored from current text. Admission rejects
pending engine dependencies; Canto does not acknowledge custody of an
incomplete remote history. The engine's pending-data behavior is tested
separately.

Restart/process reconstruction is tested, including after removing the
original checkout. Power-loss durability, database backup/migration/pruning,
long-history performance and crash recovery between physical file installs
are not qualified. An interrupted initial store creation can leave an
incomplete store; it is not automatically overwritten or repaired.

The store contains private retained source, including deleted content. It is
not encrypted and must not be committed or shared casually. Prefer a private
external store if your tools do not ignore `.canto/`. No application operation
materializes files or publishes to Git.
