mod common;
use canto::{Edit, Request, Workspace};
use common::project;
use std::fs;

#[test]
fn observed_import_keeps_unchanged_text_identity_for_later_causal_edits() {
    for current in ["hello\r\nworld\n", "hello\r\nw🌍rld\n"] {
        let repo = project();
        let store = tempfile::tempdir().unwrap();
        fs::write(repo.path().join("code.txt"), current).unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(
                repo.path().join("code.txt"),
                fs::Permissions::from_mode(0o755),
            )
            .unwrap();
        }
        let (mut ws, attached) = Workspace::attach(repo.path(), store.path()).unwrap();
        // This writer saw Git base, not the dirty observed result. Its deletion
        // must still target the unchanged original prefix after import.
        let deletion = ws
            .author(
                &attached.base,
                "independent",
                vec![Edit {
                    path: "code.txt".into(),
                    start: 0,
                    end: 5,
                    insert: String::new(),
                }],
            )
            .unwrap();
        let receipt = ws
            .admit(
                "main",
                deletion,
                Request {
                    client: "test".into(),
                    id: "delete-original-prefix".into(),
                },
            )
            .unwrap();
        assert_eq!(
            ws.read(&receipt.revision).unwrap().files["code.txt"].bytes,
            current.strip_prefix("hello").unwrap().as_bytes()
        );
        let suffix = ws
            .author(
                &attached.base,
                "other",
                vec![Edit {
                    path: "code.txt".into(),
                    start: 9,
                    end: 13,
                    insert: String::new(),
                }],
            )
            .unwrap();
        let receipt = ws
            .admit(
                "main",
                suffix,
                Request {
                    client: "test".into(),
                    id: "delete-original-suffix".into(),
                },
            )
            .unwrap();
        let expected = if current.contains('🌍') {
            "\r\nw🌍"
        } else {
            "\r\nwo"
        };
        assert_eq!(
            ws.read(&receipt.revision).unwrap().files["code.txt"].bytes,
            expected.as_bytes()
        );
    }
}
