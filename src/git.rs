//! Read-only Git/working-file observation. Never invokes hooks, filters or checkout.
use crate::{
    Error, Result,
    source::{File, FileKind, MAX_FILE, MAX_RECORD, Manifest, validate_path},
};
use std::{
    path::{Path, PathBuf},
    process::{Command, Output},
};
use uuid::Uuid;

pub(crate) struct Observation {
    pub root: PathBuf,
    pub head: Option<String>,
    pub base: Manifest,
    pub working: Manifest,
}

fn command(root: &Path, args: &[&str]) -> Result<Output> {
    Ok(Command::new("git")
        .arg("--no-optional-locks")
        .args(["-c", "core.fsmonitor=false"])
        .args(["-c", "core.untrackedCache=false"])
        .current_dir(root)
        .args(args)
        .env_remove("GIT_DIR")
        .env_remove("GIT_WORK_TREE")
        .env_remove("GIT_INDEX_FILE")
        .output()?)
}

fn git(root: &Path, args: &[&str]) -> Result<Vec<u8>> {
    let output = command(root, args)?;
    if !output.status.success() {
        return Err(Error::Git(
            String::from_utf8_lossy(&output.stderr).trim().into(),
        ));
    }
    Ok(output.stdout)
}

fn utf8(bytes: &[u8]) -> Result<&str> {
    std::str::from_utf8(bytes)
        .map_err(|_| Error::Unsupported("non-UTF-8 repository path/metadata".into()))
}

fn private(path: &str, excluded: Option<&Path>) -> bool {
    path.split('/')
        .any(|p| p.eq_ignore_ascii_case(".git") || p.eq_ignore_ascii_case(".canto"))
        || excluded.is_some_and(|prefix| Path::new(path).starts_with(prefix))
}

fn insert(
    manifest: &mut Manifest,
    path: &str,
    kind: FileKind,
    executable: bool,
    bytes: Vec<u8>,
) -> Result<()> {
    validate_path(path)?;
    if bytes.len() > MAX_FILE {
        return Err(Error::Unsupported(format!("{path} exceeds 16 MiB")));
    }
    manifest.files.insert(
        path.into(),
        File {
            document: Uuid::new_v4().to_string(),
            kind,
            executable,
            bytes,
        },
    );
    if manifest.files.len() > 10000
        || manifest
            .files
            .values()
            .map(|f| f.bytes.len())
            .sum::<usize>()
            > MAX_RECORD / 2
    {
        return Err(Error::Unsupported(
            "capture exceeds 10,000 files or 32 MiB".into(),
        ));
    }
    Ok(())
}

pub(crate) fn observe(repository: &Path, store: &Path) -> Result<Observation> {
    let root = repository.canonicalize()?;
    let top = git(&root, &["rev-parse", "--show-toplevel"])?;
    let top = utf8(&top)?.trim_end_matches('\n');
    if Path::new(top).canonicalize()? != root {
        return Err(Error::Invalid("attach requires the repository root".into()));
    }
    if root == store {
        return Err(Error::Invalid("store cannot be the repository root".into()));
    }
    let excluded = store.strip_prefix(&root).ok();
    let head_output = command(&root, &["rev-parse", "--verify", "HEAD"])?;
    let head = if head_output.status.success() {
        Some(utf8(&head_output.stdout)?.trim().to_owned())
    } else {
        // Only an unborn branch is allowed to lack HEAD. Other failures remain errors.
        let symbolic = git(&root, &["symbolic-ref", "-q", "HEAD"])?;
        let reference = utf8(&symbolic)?.trim();
        let exists = command(&root, &["show-ref", "--verify", "--quiet", reference])?;
        if exists.status.code() != Some(1) {
            return Err(Error::Git("cannot resolve HEAD".into()));
        }
        None
    };
    let mut base = Manifest::default();
    if let Some(head) = &head {
        let entries = git(&root, &["ls-tree", "-rz", "--full-tree", head])?;
        for raw in entries.split(|b| *b == 0).filter(|s| !s.is_empty()) {
            let (metadata, path) = raw.split_at(
                raw.iter()
                    .position(|b| *b == b'\t')
                    .ok_or_else(|| Error::Git("invalid tree entry".into()))?,
            );
            let path = utf8(&path[1..])?;
            if private(path, excluded) {
                continue;
            }
            let parts: Vec<_> = utf8(metadata)?.split(' ').collect();
            if parts.len() != 3 {
                return Err(Error::Git("invalid tree metadata".into()));
            }
            let (kind, executable) = match parts[0] {
                "100644" => (FileKind::Regular, false),
                "100755" => (FileKind::Regular, true),
                "120000" => (FileKind::Symlink, false),
                other => return Err(Error::Unsupported(format!("Git mode {other} at {path}"))),
            };
            let size = git(&root, &["cat-file", "-s", parts[2]])?;
            if utf8(&size)?
                .trim()
                .parse::<usize>()
                .map_err(|_| Error::Git("invalid blob size".into()))?
                > MAX_FILE
            {
                return Err(Error::Unsupported(format!("{path} exceeds 16 MiB")));
            }
            let bytes = git(&root, &["cat-file", "blob", parts[2]])?;
            insert(&mut base, path, kind, executable, bytes)?;
        }
    }
    let files = crate::checkout::Files::open(&root)?;
    let mut working = Manifest::default();
    let paths = git(
        &root,
        &[
            "ls-files",
            "-z",
            "--cached",
            "--others",
            "--exclude-standard",
        ],
    )?;
    for raw in paths.split(|b| *b == 0).filter(|s| !s.is_empty()) {
        let path = utf8(raw)?;
        if private(path, excluded) {
            continue;
        }
        validate_path(path)?;
        if let Some(file) = files.read(path)? {
            insert(&mut working, path, file.kind, file.executable, file.bytes)?;
        }
    }
    // The scan is not an atomic filesystem snapshot. At least reject a detected VCS transition.
    let after = command(&root, &["rev-parse", "--verify", "HEAD"])?;
    let after = if after.status.success() {
        Some(utf8(&after.stdout)?.trim().to_owned())
    } else {
        None
    };
    if after != head {
        return Err(Error::Invalid(
            "repository HEAD changed during attach; retry".into(),
        ));
    }
    Ok(Observation {
        root,
        head,
        base,
        working,
    })
}
