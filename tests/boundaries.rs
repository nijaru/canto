mod common;
use canto::{CausalBatch, Edit, Error, FileKind, Request, Workspace};
use common::{git, project};
use std::fs;

fn edit(insert: &str) -> Vec<Edit> {
    vec![Edit {
        path: "code.txt".into(),
        start: 0,
        end: 0,
        insert: insert.into(),
    }]
}
fn request(id: &str) -> Request {
    Request {
        client: "test".into(),
        id: id.into(),
    }
}

#[test]
fn admission_rejects_tampering_duplicate_writers_and_unselected_bases() {
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    let (mut ws, attached) = Workspace::attach(repo.path(), store.path()).unwrap();
    ws.fork(&attached.retained, "sibling").unwrap();
    let batch = ws.author(&attached.retained, "alice", edit("A")).unwrap();
    let original = serde_json::to_value(&batch).unwrap();
    for field in [
        "trailing-engine-bytes",
        "writer-overflow",
        "other-workspace",
        "missing-basis",
    ] {
        let mut changed = original.clone();
        match field {
            "trailing-engine-bytes" => changed["updates"]["code.txt"]
                .as_array_mut()
                .unwrap()
                .push(255.into()),
            "writer-overflow" => changed["writer"]["client"] = (1u64 << 53).into(),
            "other-workspace" => changed["workspace"] = "not-this-workspace".into(),
            "missing-basis" => changed["base"] = "0".repeat(64).into(),
            _ => unreachable!(),
        }
        let changed: CausalBatch = serde_json::from_value(changed).unwrap();
        assert!(
            ws.admit("main", changed, request(field)).is_err(),
            "{field}"
        );
        assert_eq!(ws.head("main").unwrap(), attached.retained);
    }
    let first = ws.admit("main", batch.clone(), request("a")).unwrap();
    assert!(matches!(
        ws.admit("main", batch, request("a-again")),
        Err(Error::WriterConflict)
    ));
    let based_on_main = ws.author(&first.revision, "bob", edit("B")).unwrap();
    assert!(
        ws.admit("sibling", based_on_main, request("wrong-stream"))
            .is_err()
    );
    assert_eq!(ws.head("sibling").unwrap(), attached.retained);
    assert_eq!(ws.head("main").unwrap(), first.revision);
}

#[test]
fn same_bytes_keep_distinct_causal_revisions_and_continuation_after_restart() {
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    let (mut ws, attached) = Workspace::attach(repo.path(), store.path()).unwrap();
    let same_bytes = ws
        .apply(
            "main",
            &attached.retained,
            "alice",
            vec![Edit {
                path: "code.txt".into(),
                start: 0,
                end: 5,
                insert: "hello".into(),
            }],
            request("replace"),
        )
        .unwrap();
    assert_ne!(same_bytes.revision, attached.retained);
    assert_eq!(
        ws.read(&same_bytes.revision).unwrap(),
        ws.read(&attached.retained).unwrap()
    );
    drop(ws);
    let mut ws = Workspace::open(store.path()).unwrap();
    let next = ws
        .apply(
            "main",
            &same_bytes.revision,
            "new-session",
            edit("after-restart:"),
            request("continue"),
        )
        .unwrap();
    assert_eq!(
        ws.read(&next.revision).unwrap().files["code.txt"].bytes,
        b"after-restart:hello\r\nworld\n"
    );
    let history = ws.history(&next.revision).unwrap();
    assert_ne!(
        history[history.len() - 1].writer_epoch,
        history[history.len() - 2].writer_epoch
    );
}

#[test]
fn corrupt_revision_and_unknown_schema_are_rejected_without_reinitializing() {
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    let (ws, attached) = Workspace::attach(repo.path(), store.path()).unwrap();
    drop(ws);
    let path = store.path().join("workspace.sqlite3");
    let conn = rusqlite::Connection::open(&path).unwrap();
    let mut payload: Vec<u8> = conn
        .query_row(
            "SELECT payload FROM revisions WHERE id=?1",
            [&attached.retained],
            |r| r.get(0),
        )
        .unwrap();
    payload.push(b' ');
    conn.execute(
        "UPDATE revisions SET payload=?1 WHERE id=?2",
        rusqlite::params![payload, attached.retained],
    )
    .unwrap();
    let ws = Workspace::open(store.path()).unwrap();
    assert!(matches!(
        ws.read(&attached.retained),
        Err(Error::Corrupt(_))
    ));
    assert_eq!(
        ws.read(&attached.base).unwrap().files["code.txt"].bytes,
        b"hello\r\nworld\n"
    );
    drop(ws);
    conn.pragma_update(None, "user_version", 999).unwrap();
    drop(conn);
    assert!(matches!(
        Workspace::open(store.path()),
        Err(Error::Corrupt(_))
    ));
    assert!(Workspace::attach(repo.path(), store.path()).is_err());
    let conn = rusqlite::Connection::open(path).unwrap();
    assert_eq!(
        conn.pragma_query_value(None, "user_version", |r| r.get::<_, i32>(0))
            .unwrap(),
        999
    );
}

#[test]
fn capture_handles_unborn_repository_deletions_and_custom_private_store() {
    let repo = tempfile::tempdir().unwrap();
    git(repo.path(), &["init", "-q"]);
    fs::write(repo.path().join("new.txt"), b"new").unwrap();
    let directory = repo.path().join("private-state");
    fs::create_dir(&directory).unwrap();
    fs::write(directory.join("private-note"), b"must not be source").unwrap();
    let (ws, attached) = Workspace::attach(repo.path(), &directory).unwrap();
    assert!(attached.git_head.is_none());
    assert!(ws.read(&attached.base).unwrap().files.is_empty());
    let retained = ws.read(&attached.retained).unwrap();
    assert_eq!(retained.files.len(), 1);
    assert_eq!(retained.files["new.txt"].bytes, b"new");
    assert!(Workspace::attach(repo.path(), &directory).is_err());

    let repo = project();
    let store = tempfile::tempdir().unwrap();
    fs::remove_file(repo.path().join("code.txt")).unwrap();
    fs::write(repo.path().join("binary"), [255, 254, 0]).unwrap();
    let (ws, attached) = Workspace::attach(repo.path(), store.path()).unwrap();
    assert!(
        ws.read(&attached.base)
            .unwrap()
            .files
            .contains_key("code.txt")
    );
    let retained = ws.read(&attached.retained).unwrap();
    assert!(!retained.files.contains_key("code.txt"));
    assert_eq!(retained.files["binary"].bytes, [255, 254, 0]);
}

#[test]
fn git_reads_never_execute_configured_filters_hooks_or_fsmonitor() {
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    let sentinel = repo.path().join("EXECUTED");
    let command = format!("touch '{}'; cat", sentinel.display());
    git(repo.path(), &["config", "filter.untrusted.clean", &command]);
    git(
        repo.path(),
        &["config", "filter.untrusted.smudge", &command],
    );
    git(repo.path(), &["config", "core.fsmonitor", &command]);
    git(repo.path(), &["config", "core.autocrlf", "true"]);
    fs::write(
        repo.path().join(".gitattributes"),
        "code.txt filter=untrusted\n",
    )
    .unwrap();
    fs::write(repo.path().join("code.txt"), b"dirty\r\nexact\r\n").unwrap();
    let index_before = fs::read(repo.path().join(".git/index")).unwrap();
    let (ws, attached) = Workspace::attach(repo.path(), store.path()).unwrap();
    assert_eq!(
        ws.read(&attached.base).unwrap().files["code.txt"].bytes,
        b"hello\r\nworld\n"
    );
    assert_eq!(
        ws.read(&attached.retained).unwrap().files["code.txt"].bytes,
        b"dirty\r\nexact\r\n"
    );
    assert!(!sentinel.exists());
    assert_eq!(
        fs::read(repo.path().join(".git/index")).unwrap(),
        index_before
    );
}

#[cfg(unix)]
#[test]
fn symlink_targets_modes_and_non_utf8_bytes_are_retained_without_following() {
    use std::os::unix::{
        ffi::OsStrExt,
        fs::{PermissionsExt, symlink},
    };
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    let outside = tempfile::tempdir().unwrap();
    fs::write(outside.path().join("secret"), b"never imported").unwrap();
    symlink(outside.path(), repo.path().join("outside-link")).unwrap();
    let target = std::ffi::OsStr::from_bytes(b"opaque-\xff");
    symlink(target, repo.path().join("opaque-link")).unwrap();
    fs::set_permissions(
        repo.path().join("code.txt"),
        fs::Permissions::from_mode(0o755),
    )
    .unwrap();
    let (ws, attached) = Workspace::attach(repo.path(), store.path()).unwrap();
    let retained = ws.read(&attached.retained).unwrap();
    assert_eq!(retained.files["outside-link"].kind, FileKind::Symlink);
    assert_eq!(
        retained.files["outside-link"].bytes,
        outside.path().as_os_str().as_bytes()
    );
    assert_eq!(retained.files["opaque-link"].bytes, b"opaque-\xff");
    assert!(retained.files["code.txt"].executable);
    assert!(!retained.files.keys().any(|p| p.ends_with("secret")));
}

#[cfg(unix)]
#[test]
fn symlink_ancestor_and_unrepresentable_names_are_explicitly_unsupported() {
    use std::{
        io::Write,
        os::unix::fs::symlink,
        process::{Command, Stdio},
    };
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    fs::create_dir(repo.path().join("dir")).unwrap();
    fs::write(repo.path().join("dir/tracked"), b"tracked").unwrap();
    git(repo.path(), &["add", "dir"]);
    git(repo.path(), &["commit", "-qm", "directory"]);
    fs::remove_dir_all(repo.path().join("dir")).unwrap();
    let outside = tempfile::tempdir().unwrap();
    fs::write(outside.path().join("tracked"), b"foreign").unwrap();
    symlink(outside.path(), repo.path().join("dir")).unwrap();
    assert!(matches!(
        Workspace::attach(repo.path(), store.path()),
        Err(Error::Unsupported(_))
    ));
    assert!(!store.path().join("workspace.sqlite3").exists());
    fs::remove_file(repo.path().join("dir")).unwrap();
    // APFS rejects invalid-UTF-8 filenames. Construct the Git tree directly so
    // the same unsupported-name boundary is exercised on macOS and Linux.
    let blob = String::from_utf8(git(repo.path(), &["rev-parse", "HEAD:binary"])).unwrap();
    let mut tree_input = format!("100644 blob {}\tbad-", blob.trim()).into_bytes();
    tree_input.extend_from_slice(b"\xff\0");
    let mut process = Command::new("git")
        .current_dir(repo.path())
        .args(["mktree", "-z"])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    process
        .stdin
        .take()
        .unwrap()
        .write_all(&tree_input)
        .unwrap();
    let tree = process.wait_with_output().unwrap();
    assert!(tree.status.success());
    let tree = String::from_utf8(tree.stdout).unwrap();
    let commit = String::from_utf8(git(
        repo.path(),
        &["commit-tree", tree.trim(), "-m", "unsupported name"],
    ))
    .unwrap();
    git(repo.path(), &["update-ref", "HEAD", commit.trim()]);
    assert!(matches!(
        Workspace::attach(repo.path(), store.path()),
        Err(Error::Unsupported(_))
    ));
}
