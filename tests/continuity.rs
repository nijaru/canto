mod common;
use canto::{Edit, Error, Request, Workspace};
use common::{git, project};
use std::fs;

fn req(id: &str) -> Request {
    Request {
        client: "test".into(),
        id: id.into(),
    }
}

fn edit(path: &str, start: u32, end: u32, insert: &str) -> Edit {
    Edit {
        path: path.into(),
        start,
        end,
        insert: insert.into(),
    }
}

#[test]
fn dirty_attach_fork_conditional_edit_and_restart_preserve_selected_source() {
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    fs::write(repo.path().join("code.txt"), "hello\r\ndirty 🌍\n").unwrap();
    fs::write(repo.path().join("untracked"), b"unsaved-on-disk").unwrap();
    fs::write(repo.path().join("ignored"), b"excluded").unwrap();
    let status = git(repo.path(), &["status", "--porcelain=v1", "-z"]);
    let (mut ws, attached) = Workspace::attach(repo.path(), store.path()).unwrap();
    let base = ws.read(&attached.base).unwrap();
    assert_eq!(base.files["code.txt"].bytes, b"hello\r\nworld\n");
    assert_eq!(base.files["binary"].bytes, [0, 255, 1]);
    let retained = ws.read(&attached.retained).unwrap();
    assert_eq!(
        retained.files["code.txt"].bytes,
        "hello\r\ndirty 🌍\n".as_bytes()
    );
    assert!(retained.files.contains_key("untracked"));
    assert!(!retained.files.contains_key("ignored"));
    assert_eq!(
        base.files["code.txt"].document,
        retained.files["code.txt"].document
    );
    ws.fork(&attached.retained, "task").unwrap();
    let change = vec![edit("code.txt", 0, 5, "goodbye")];
    let receipt = ws
        .apply(
            "task",
            &attached.retained,
            "agent",
            change.clone(),
            req("edit"),
        )
        .unwrap();
    assert_eq!(
        receipt,
        ws.apply("task", &attached.retained, "agent", change, req("edit"))
            .unwrap()
    );
    assert!(matches!(
        ws.apply("task", &attached.retained, "agent", vec![], req("edit")),
        Err(Error::RequestConflict)
    ));
    assert!(matches!(
        ws.apply(
            "task",
            &attached.retained,
            "agent",
            vec![edit("code.txt", 0, 1, "X")],
            req("stale")
        ),
        Err(Error::Stale { .. })
    ));
    let task = ws.read(&receipt.revision).unwrap();
    assert_eq!(
        task.files["code.txt"].bytes,
        "goodbye\r\ndirty 🌍\n".as_bytes()
    );
    assert_eq!(ws.head("main").unwrap(), attached.retained);
    assert_eq!(
        git(repo.path(), &["status", "--porcelain=v1", "-z"]),
        status
    );
    assert_eq!(
        fs::read(repo.path().join("code.txt")).unwrap(),
        "hello\r\ndirty 🌍\n".as_bytes()
    );
    drop(ws);
    fs::remove_dir_all(repo.path()).unwrap();
    let ws = Workspace::open(store.path()).unwrap();
    assert_eq!(ws.attachment().unwrap(), attached);
    assert_eq!(ws.read(&receipt.revision).unwrap(), task);
    assert_eq!(ws.read(&attached.retained).unwrap(), retained);
    assert_eq!(ws.read(&attached.base).unwrap(), base);
}

#[test]
fn independent_causal_authors_do_not_fabricate_observation_or_mix_siblings() {
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    let (mut ws, a) = Workspace::attach(repo.path(), store.path()).unwrap();
    ws.fork(&a.retained, "other").unwrap();
    let author_a = Workspace::open(store.path()).unwrap();
    let author_b = Workspace::open(store.path()).unwrap();
    let left = author_a
        .author(&a.retained, "alice", vec![edit("code.txt", 0, 0, "A")])
        .unwrap();
    let right = author_b
        .author(&a.retained, "bob", vec![edit("code.txt", 13, 13, "B")])
        .unwrap();
    assert_ne!(left.writer_epoch(), right.writer_epoch());
    let first = ws.admit("main", left, req("a")).unwrap();
    let both = ws.admit("main", right.clone(), req("b")).unwrap();
    assert_eq!(both, ws.admit("main", right, req("b")).unwrap());
    assert_eq!(
        ws.read(&both.revision).unwrap().files["code.txt"].bytes,
        b"Ahello\r\nworld\nB"
    );
    let history = ws.history(&both.revision).unwrap();
    let last = history.last().unwrap();
    assert_eq!(last.parent.as_deref(), Some(first.revision.as_str()));
    assert_eq!(last.authored_base.as_deref(), Some(a.retained.as_str()));
    assert_eq!(
        ws.read(&ws.head("other").unwrap()).unwrap(),
        ws.read(&a.retained).unwrap()
    );
    drop(ws);
    let ws = Workspace::open(store.path()).unwrap();
    assert_eq!(
        ws.read(&both.revision).unwrap().files["code.txt"].bytes,
        b"Ahello\r\nworld\nB"
    );
    assert_eq!(
        ws.read(&first.revision).unwrap().files["code.txt"].bytes,
        b"Ahello\r\nworld\n"
    );
}

#[test]
fn invalid_batch_is_atomic_and_byte_coordinates_are_checked() {
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    fs::write(repo.path().join("unicode"), "a🌍b").unwrap();
    let (mut ws, a) = Workspace::attach(repo.path(), store.path()).unwrap();
    for edits in [
        vec![edit("code.txt", 0, 1, "X"), edit("unicode", 2, 3, "bad")],
        vec![edit("binary", 0, 1, "bad")],
        vec![edit("../escape", 0, 0, "bad")],
    ] {
        assert!(
            ws.apply("main", &a.retained, "actor", edits, req("invalid"))
                .is_err()
        );
        assert_eq!(ws.head("main").unwrap(), a.retained);
    }
    let ok = ws
        .apply(
            "main",
            &a.retained,
            "actor",
            vec![edit("unicode", 1, 5, "é")],
            req("valid"),
        )
        .unwrap();
    assert_eq!(
        ws.read(&ok.revision).unwrap().files["unicode"].bytes,
        "aéb".as_bytes()
    );
}
