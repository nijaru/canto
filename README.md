# Canto

Canto is a local workspace for reviewing and combining parallel work while
keeping your existing editor, coding agents and source control.

**Experimental.** The first Rust milestone implements headless source
continuity, not the complete review workbench. The previous Go agent framework
has been retired; its history remains in Git. Storage and APIs are unstable.

## Try the source loop

Requires stable Rust and Git. Working-file capture currently supports Unix
(macOS/Linux).

```sh
cargo build --locked
./target/debug/canto --help

# From the root of a disposable Git project:
canto attach .
base=$(canto head main | tr -d '"')
canto fork "$base" task
edits=$(mktemp)
printf '%s\n' '[{"path":"code.txt","start":0,"end":0,"insert":"// task\n"}]' > "$edits"
canto edit task --base "$base" --actor me --edits "$edits" \
  --client demo --request edit-1
task=$(canto head task | tr -d '"')
canto read "$task" code.txt
rm "$edits"
```

Use the built executable's absolute path or add it to `PATH`. The example
assumes the project contains `code.txt`. Each command is a fresh process:
retained source survives restart and can be read without the original checkout.

Attach records Git HEAD plus tracked and nonignored working files without
changing them. The default private store is `.canto/`; `--store DIRECTORY`
selects another location. Review what you attach: tracked secrets and nonignored
private files can be retained. Nothing is uploaded.

Fork and edit change retained source only. They do **not** write checkout files,
create Git branches, commit or publish changes. Read the
[source-continuity behavior and limits](docs/source-continuity.md) before using
this on valuable data.

## Current scope

- Immutable revisions and isolated workstream heads.
- Atomic conditional UTF-8 edits with committed-request retry receipts.
- Explicit independent causal authorship/admission using pinned native Yrs.
- Exact retained text/opaque bytes, symlink targets and executable flags.
- SQLite-backed reconstruction of selected history.

No TUI, task checkout provisioning, proposals, editor integration, application
undo, remote synchronization or crash-recovery qualification yet.
The [native Yrs qualification](docs/engine-qualification.md) records tested
behavior and undo/anchor limitations; it is not a product-readiness claim.

## Development

```sh
make check
# Equivalent: fmt check, Clippy, workspace tests and build, using Cargo.lock.
```

Tests include separate CLI processes authoring/admitting causal changes,
competing conditional writers, reconstruction after checkout deletion, and
no-follow capture under symlink swaps. No account, cloud, editor or agent
harness is required.

## License

Apache-2.0; see [LICENSE](LICENSE).
