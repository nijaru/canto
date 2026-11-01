use canto::{Request, Result, Workspace};
use serde::{Serialize, de::DeserializeOwned};
use std::{
    fs,
    io::{Read, Write},
    path::{Path, PathBuf},
    process::ExitCode,
};
use usage_rs::{Args, Cli, Subcommands};

/// An experimental local source workspace.
#[derive(Cli)]
#[usage(bin = "canto", version)]
struct Cli {
    /// Private workspace store directory.
    #[usage(long, global, default = ".canto")]
    store: PathBuf,
    #[usage(subcommand)]
    command: Command,
}

#[derive(Subcommands)]
enum Command {
    Attach(Attach),
    Info(Info),
    Head(Head),
    Fork(Fork),
    Read(ReadSource),
    History(History),
    Edit(EditSource),
    Author(Author),
    Admit(Admit),
}

/// Retain Git HEAD and working files without changing the checkout.
#[derive(Args)]
struct Attach {
    #[usage(default = ".")]
    repository: PathBuf,
}

/// Show the original attachment receipt.
#[derive(Args)]
struct Info {}

/// Resolve a workstream's current retained revision.
#[derive(Args)]
struct Head {
    #[usage(default = "main")]
    workstream: String,
}

/// Fork source history (does not provision a checkout).
#[derive(Args)]
struct Fork {
    revision: String,
    name: String,
}

/// Read a retained manifest as JSON, or one file as exact raw bytes.
#[derive(Args)]
struct ReadSource {
    revision: String,
    path: Option<String>,
}

/// Show selected source inclusion and original causal bases.
#[derive(Args)]
struct History {
    revision: String,
}

#[derive(Args)]
struct Identity {
    /// Stable caller namespace for retry deduplication.
    #[usage(long)]
    client: String,
    /// Caller-generated request identity; reuse only with identical input.
    #[usage(long)]
    request: String,
}
impl From<Identity> for Request {
    fn from(value: Identity) -> Self {
        Self {
            client: value.client,
            id: value.request,
        }
    }
}

/// Apply a whole-head conditional batch of UTF-8 byte-range edits.
#[derive(Args)]
struct EditSource {
    workstream: String,
    #[usage(long)]
    base: String,
    #[usage(long)]
    actor: String,
    /// JSON array of {path,start,end,insert}; offsets follow preceding edits.
    #[usage(long)]
    edits: PathBuf,
    #[usage(flatten)]
    identity: Identity,
}

/// Author an independent causal batch at exactly the pinned base; does not commit.
#[derive(Args)]
struct Author {
    base: String,
    #[usage(long)]
    actor: String,
    #[usage(long)]
    edits: PathBuf,
}

/// Admit an explicitly collaborative batch; not proposal integration.
#[derive(Args)]
struct Admit {
    workstream: String,
    batch: PathBuf,
    #[usage(flatten)]
    identity: Identity,
}

fn json(value: &impl Serialize) -> Result<()> {
    let stdout = std::io::stdout();
    let mut out = stdout.lock();
    serde_json::to_writer(&mut out, value)?;
    out.write_all(b"\n")?;
    Ok(())
}

fn input<T: DeserializeOwned>(path: &Path) -> Result<T> {
    let mut bytes = Vec::new();
    fs::File::open(path)?
        .take(64 * 1024 * 1024 + 1)
        .read_to_end(&mut bytes)?;
    if bytes.len() > 64 * 1024 * 1024 {
        return Err(canto::Error::Invalid("JSON input exceeds 64 MiB".into()));
    }
    Ok(serde_json::from_slice(&bytes)?)
}

fn run(cli: Cli) -> Result<()> {
    if let Command::Attach(args) = cli.command {
        let (_, receipt) = Workspace::attach(&args.repository, &cli.store)?;
        return json(&receipt);
    }
    let mut workspace = Workspace::open(&cli.store)?;
    match cli.command {
        Command::Attach(_) => unreachable!("attachment handled before opening a store"),
        Command::Info(_) => json(&workspace.attachment()?),
        Command::Head(args) => json(&workspace.head(&args.workstream)?),
        Command::Fork(args) => json(&workspace.fork(&args.revision, &args.name)?),
        Command::History(args) => json(&workspace.history(&args.revision)?),
        Command::Read(args) => {
            let manifest = workspace.read(&args.revision)?;
            if let Some(path) = args.path {
                let file = manifest
                    .files
                    .get(&path)
                    .ok_or_else(|| canto::Error::Missing(format!("path {path}")))?;
                std::io::stdout().lock().write_all(&file.bytes)?;
                Ok(())
            } else {
                json(&manifest)
            }
        }
        Command::Edit(args) => json(&workspace.apply(
            &args.workstream,
            &args.base,
            &args.actor,
            input(&args.edits)?,
            args.identity.into(),
        )?),
        Command::Author(args) => {
            json(&workspace.author(&args.base, &args.actor, input(&args.edits)?)?)
        }
        Command::Admit(args) => {
            json(&workspace.admit(&args.workstream, input(&args.batch)?, args.identity.into())?)
        }
    }
}

fn main() -> ExitCode {
    match run(Cli::parse()) {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("{error}");
            ExitCode::FAILURE
        }
    }
}
