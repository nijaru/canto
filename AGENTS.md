# Canto

Integration-first local workspace connecting existing editors, agents and Git.
The previous Go agent framework has been retired; its source remains in Git history.

## Scope and boundaries

- Build a modular Rust application/library with one application API and SQLite commit owner.
- Gate A is source continuity and native text/history qualification. Storage is experimental.
- Keep source revisions separate from checkout files, Git commits and harness activity.
- Independent workstreams are not automatically CRDT-unioned into each other.
- Do not reuse a causal writer identity across diverging replicas.
- No Zed implementation dependency, custom editor, VCS or agent harness.
- Preserve Apache-2.0. Do not change Ion or AVC.

## Checks

- `cargo fmt --all -- --check`
- `cargo clippy --workspace --all-targets -- -D warnings`
- `cargo test --workspace`
- `cargo build --workspace`

Use native Rust tests for Yrs claims; JavaScript Yjs results are not qualification.
Do not promise stable codecs, remote synchronization, live editor undo or
filesystem atomicity based on headless source tests.
