//! Real-repository integration for M12 git fallback (blueprint §32).
//!
//! Requires the system git binary (Omarchy records its version in
//! `docs/dependencies.md`); a missing binary fails loudly rather than
//! passing vacuously.

use std::path::PathBuf;
use std::process::Command;

use omaterm_context::{git_toplevel_of, resolve_root};

fn git_is_available() -> bool {
    Command::new("git")
        .arg("--version")
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .is_ok_and(|status| status.success())
}

fn init_repo(path: &std::path::Path) {
    let status = Command::new("git")
        .arg("init")
        .arg(path)
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .expect("git init must spawn");
    assert!(status.success(), "git init {}", path.display());
}

#[test]
fn real_repos_resolve_to_the_nearest_toplevel() {
    assert!(git_is_available(), "system git is required for this test");
    let base: PathBuf =
        std::env::temp_dir().join(format!("omaterm-m12-git-roots-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&base);
    let outer = base.join("outer");
    let sub = outer.join("sub");
    let inner = outer.join("inner");
    let plain = base.join("plain");
    std::fs::create_dir_all(&sub).unwrap();
    std::fs::create_dir_all(&plain).unwrap();
    init_repo(&outer);
    init_repo(&inner);

    assert_eq!(git_toplevel_of(&sub), Some(outer.clone()));
    assert_eq!(git_toplevel_of(&inner), Some(inner.clone()));
    assert_eq!(git_toplevel_of(&plain), None);

    // End to end through resolution: no pin falls back to git.
    let info = resolve_root(None, Some(&sub));
    assert_eq!(info.root, Some(outer.clone()));
    assert_eq!(info.source, omaterm_core::RootSource::Git);

    let _ = std::fs::remove_dir_all(&base);
}
