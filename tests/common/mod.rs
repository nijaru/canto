use std::{fs, path::Path, process::Command};
use tempfile::TempDir;

pub fn git(root: &Path, args: &[&str]) -> Vec<u8> {
    let out = Command::new("git")
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .current_dir(root)
        .args(args)
        .output()
        .unwrap();
    assert!(
        out.status.success(),
        "{}",
        String::from_utf8_lossy(&out.stderr)
    );
    out.stdout
}

pub fn project() -> TempDir {
    let dir = tempfile::tempdir().unwrap();
    git(dir.path(), &["init", "-q"]);
    git(
        dir.path(),
        &["config", "user.email", "test@example.invalid"],
    );
    git(dir.path(), &["config", "user.name", "Canto test"]);
    fs::write(dir.path().join("code.txt"), "hello\r\nworld\n").unwrap();
    fs::write(dir.path().join("binary"), [0, 255, 1]).unwrap();
    fs::write(dir.path().join(".gitignore"), "ignored\n").unwrap();
    git(dir.path(), &["add", "."]);
    git(dir.path(), &["commit", "-qm", "base"]);
    dir
}
