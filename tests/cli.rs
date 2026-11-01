mod common;
use canto::{Attachment, Receipt, RevisionInfo, Workspace};
use common::{git, project};
use serde_json::Value;
use std::{
    fs,
    path::Path,
    process::{Child, Command, Output, Stdio},
};

fn command(store: &Path) -> Command {
    let mut command = Command::new(env!("CARGO_BIN_EXE_canto"));
    command.arg("--store").arg(store);
    command
}
fn output(mut cmd: Command) -> Output {
    cmd.output().unwrap()
}
fn success(out: Output) -> Vec<u8> {
    assert!(
        out.status.success(),
        "{}",
        String::from_utf8_lossy(&out.stderr)
    );
    out.stdout
}
fn json<T: serde::de::DeserializeOwned>(out: Output) -> T {
    serde_json::from_slice(&success(out)).unwrap()
}
fn spawn(mut cmd: Command) -> Child {
    cmd.stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap()
}

#[test]
fn independent_cli_processes_author_admit_and_reconstruct_without_a_checkout() {
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    let files = tempfile::tempdir().unwrap();
    fs::write(repo.path().join("code.txt"), "dirty\r\nworld\n").unwrap();
    let before = git(repo.path(), &["status", "--porcelain=v1", "-z"]);
    let mut attach = command(store.path());
    attach.arg("attach").arg(repo.path());
    let attached: Attachment = json(output(attach));
    let mut fork = command(store.path());
    fork.args(["fork", &attached.retained, "task"]);
    let forked: Receipt = json(output(fork));
    assert_eq!(forked.revision, attached.retained);
    let a_edits = files.path().join("a-edits.json");
    let b_edits = files.path().join("b-edits.json");
    fs::write(
        &a_edits,
        r#"[{"path":"code.txt","start":0,"end":0,"insert":"A"}]"#,
    )
    .unwrap();
    fs::write(
        &b_edits,
        r#"[{"path":"code.txt","start":13,"end":13,"insert":"B"}]"#,
    )
    .unwrap();
    let mut a = command(store.path());
    a.args(["author", &attached.retained, "--actor", "alice", "--edits"])
        .arg(&a_edits);
    let mut b = command(store.path());
    b.args(["author", &attached.retained, "--actor", "bob", "--edits"])
        .arg(&b_edits);
    let a = spawn(a);
    let b = spawn(b);
    let a_bytes = success(a.wait_with_output().unwrap());
    let b_bytes = success(b.wait_with_output().unwrap());
    let a_value: Value = serde_json::from_slice(&a_bytes).unwrap();
    let b_value: Value = serde_json::from_slice(&b_bytes).unwrap();
    assert_ne!(a_value["writer"]["epoch"], b_value["writer"]["epoch"]);
    assert_ne!(a_value["writer"]["client"], b_value["writer"]["client"]);
    let a_path = files.path().join("a.json");
    let b_path = files.path().join("b.json");
    fs::write(&a_path, &a_bytes).unwrap();
    fs::write(&b_path, &b_bytes).unwrap();
    let mut a = command(store.path());
    a.args(["admit", "task"])
        .arg(&a_path)
        .args(["--client", "alice", "--request", "1"]);
    let mut b = command(store.path());
    b.args(["admit", "task"])
        .arg(&b_path)
        .args(["--client", "bob", "--request", "1"]);
    let a = spawn(a);
    let b = spawn(b);
    let _: Receipt = json(a.wait_with_output().unwrap());
    let b_receipt: Receipt = json(b.wait_with_output().unwrap());
    let mut retry = command(store.path());
    retry
        .args(["admit", "task"])
        .arg(&b_path)
        .args(["--client", "bob", "--request", "1"]);
    assert_eq!(json::<Receipt>(output(retry)), b_receipt);
    let mut head = command(store.path());
    head.args(["head", "task"]);
    let head: String = json(output(head));
    let mut history = command(store.path());
    history.args(["history", &head]);
    let history: Vec<RevisionInfo> = json(output(history));
    for item in &history[history.len() - 2..] {
        assert_eq!(
            item.authored_base.as_deref(),
            Some(attached.retained.as_str())
        );
    }
    assert_eq!(
        git(repo.path(), &["status", "--porcelain=v1", "-z"]),
        before
    );
    fs::remove_dir_all(repo.path()).unwrap();
    let mut read = command(store.path());
    read.args(["read", &head, "code.txt"]);
    assert_eq!(success(output(read)), b"Adirty\r\nworld\nB");
    let mut main = command(store.path());
    main.args(["head", "main"]);
    assert_eq!(json::<String>(output(main)), attached.retained);
    let mut info = command(store.path());
    info.arg("info");
    assert_eq!(json::<Attachment>(output(info)), attached);
    let ws = Workspace::open(store.path()).unwrap();
    assert_eq!(
        ws.read(&attached.base).unwrap().files["code.txt"].bytes,
        b"hello\r\nworld\n"
    );
}

#[test]
fn concurrent_conditional_cli_edits_have_one_winner_and_durable_retry() {
    let repo = project();
    let store = tempfile::tempdir().unwrap();
    let files = tempfile::tempdir().unwrap();
    let (_, attached) = Workspace::attach(repo.path(), store.path()).unwrap();
    let edits = files.path().join("edits.json");
    fs::write(
        &edits,
        r#"[{"path":"code.txt","start":0,"end":5,"insert":"checked"}]"#,
    )
    .unwrap();
    let build = |id: &str| {
        let mut cmd = command(store.path());
        cmd.args([
            "edit",
            "main",
            "--base",
            &attached.retained,
            "--actor",
            "agent",
            "--client",
            "cli",
            "--request",
            id,
            "--edits",
        ])
        .arg(&edits);
        cmd
    };
    let a = spawn(build("a"));
    let b = spawn(build("b"));
    let a = a.wait_with_output().unwrap();
    let b = b.wait_with_output().unwrap();
    assert_ne!(a.status.success(), b.status.success());
    let (winner, losing, id) = if a.status.success() {
        (a, b, "a")
    } else {
        (b, a, "b")
    };
    assert!(losing.stdout.is_empty());
    let receipt: Receipt = json(winner);
    assert_eq!(receipt, json(output(build(id))));
    let mut read = command(store.path());
    read.args(["read", &receipt.revision, "code.txt"]);
    assert_eq!(success(output(read)), b"checked\r\nworld\n");
}
