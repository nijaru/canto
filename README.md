# Canto

Canto is becoming a local workspace for reviewing and combining parallel work
while keeping your existing editor, coding agents and source control.

**Experimental restart.** The old Go agent framework has been retired. Its
history remains available in Git. The Rust application is under construction;
there is no stable storage format or release yet.

The first milestone is a headless source-continuity loop: attach a dirty Git
project without changing its files, retain source, fork a workstream, apply a
checked edit, and reconstruct the selected history after restart. Review UI,
task checkouts and editor integrations come later.

## Development

Install stable Rust, then run:

```sh
cargo build --locked
cargo test --locked
cargo fmt --all -- --check
cargo clippy --workspace --all-targets -- -D warnings
cargo run -- --help
```

No account, hosted service, editor or agent harness is required.

## License

Apache-2.0; see [LICENSE](LICENSE).
