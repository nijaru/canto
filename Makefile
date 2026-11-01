.PHONY: fmt test build check
fmt:
	cargo fmt --all
test:
	cargo test --locked --workspace
build:
	cargo build --locked --workspace
check:
	cargo fmt --all -- --check
	cargo clippy --locked --workspace --all-targets -- -D warnings
	cargo test --locked --workspace
	cargo build --locked --workspace
