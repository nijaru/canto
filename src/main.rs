use usage_rs::Cli;

/// An experimental local source workspace.
#[derive(Cli)]
#[usage(bin = "canto", version)]
struct Cli {}

fn main() {
    Cli::parse();
}
