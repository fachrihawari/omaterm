//! Real-repository integration for M14 git status + mutations (blueprint
//! §32, system git only).
//!
//! Requires the system git binary; a missing binary fails loudly rather
//! than passing vacuously. Repos live under `/tmp` and are removed after
//! each test.

use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};

use omaterm_context::{GitError, git_commit, git_discard, git_stage, git_status, git_unstage};

fn git_is_available() -> bool {
    Command::new("git")
        .arg("--version")
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .is_ok_and(|status| status.success())
}

fn git(repo: &Path, args: &[&str]) {
    let status = Command::new("git")
        .args(args)
        .current_dir(repo)
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .expect("git must spawn");
    assert!(status.success(), "git {args:?} in {}", repo.display());
}

fn fixture(name: &str) -> PathBuf {
    assert!(git_is_available(), "system git is required for this test");
    let repo: PathBuf =
        std::env::temp_dir().join(format!("omaterm-m14-git-{name}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&repo);
    std::fs::create_dir_all(&repo).unwrap();
    git(&repo, &["init"]);
    git(&repo, &["config", "user.email", "m14@test"]);
    git(&repo, &["config", "user.name", "m14"]);
    git(&repo, &["config", "commit.gpgsign", "false"]);
    repo
}

fn commit_all(repo: &Path, message: &str) {
    git(repo, &["add", "-A"]);
    git(repo, &["commit", "-qm", message]);
}

fn branch_of(repo: &Path) -> Option<String> {
    git_status(repo, 5000).ok()?.branch
}

#[test]
fn clean_repo_reports_branch_and_empty_groups() {
    let repo = fixture("clean");
    std::fs::write(repo.join("file.txt"), b"hi\n").unwrap();
    commit_all(&repo, "init");
    let status = git_status(&repo, 5000).unwrap();
    assert!(!status.truncated);
    assert!(status.staged.is_empty() && status.unstaged.is_empty() && status.untracked.is_empty());
    // Branch name is environment-dependent (master/main); assert presence.
    assert!(status.branch.is_some(), "branch must parse: {status:?}");
    assert_eq!(branch_of(&repo), status.branch);
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn dirty_repo_groups_and_unicode_spaces_survive() {
    let repo = fixture("dirty");
    std::fs::write(repo.join("tracked.txt"), b"v1\n").unwrap();
    std::fs::write(repo.join("sp ace ün.txt"), b"v1\n").unwrap();
    commit_all(&repo, "init");
    // Staged modification, unstaged modification, untracked file + dir.
    std::fs::write(repo.join("tracked.txt"), b"v2\n").unwrap();
    git(&repo, &["add", "tracked.txt"]);
    std::fs::write(repo.join("sp ace ün.txt"), b"v2\n").unwrap();
    std::fs::write(repo.join("new.txt"), b"new\n").unwrap();
    std::fs::create_dir_all(repo.join("newdir")).unwrap();
    std::fs::write(repo.join("newdir").join("in.txt"), b"in\n").unwrap();

    let status = git_status(&repo, 5000).unwrap();
    assert!(
        status
            .staged
            .iter()
            .any(|e| e.path.as_path() == Path::new("tracked.txt"))
    );
    let spaced = status
        .unstaged
        .iter()
        .find(|e| e.path.as_path() == Path::new("sp ace ün.txt"))
        .expect("unicode spaced path must parse");
    assert_eq!((spaced.x, spaced.y), ('.', 'M'));
    assert!(
        status
            .untracked
            .iter()
            .any(|e| e.path.as_path() == Path::new("new.txt"))
    );
    assert!(
        status
            .untracked
            .iter()
            .any(|e| e.path.as_path() == Path::new("newdir/"))
    );
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn stage_status_unstage_round_trip() {
    let repo = fixture("roundtrip");
    std::fs::write(repo.join("a.txt"), b"v1\n").unwrap();
    commit_all(&repo, "init");
    std::fs::write(repo.join("a.txt"), b"v2\n").unwrap();

    let before = git_status(&repo, 5000).unwrap();
    assert!(before.staged.is_empty());
    assert_eq!(before.unstaged.len(), 1);

    git_stage(&repo, &[PathBuf::from("a.txt")]).unwrap();
    let staged = git_status(&repo, 5000).unwrap();
    assert_eq!(staged.staged.len(), 1);
    assert!(staged.unstaged.is_empty());

    git_unstage(&repo, &[PathBuf::from("a.txt")]).unwrap();
    let after = git_status(&repo, 5000).unwrap();
    assert!(after.staged.is_empty());
    assert_eq!(after.unstaged.len(), 1);
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn discard_restores_tracked_and_deletes_untracked() {
    let repo = fixture("discard");
    std::fs::write(repo.join("keep.txt"), b"v1\n").unwrap();
    commit_all(&repo, "init");
    std::fs::write(repo.join("keep.txt"), b"v2\n").unwrap();
    std::fs::write(repo.join("scratch.txt"), b"drop me\n").unwrap();

    git_discard(
        &repo,
        &[PathBuf::from("keep.txt"), PathBuf::from("scratch.txt")],
    )
    .unwrap();
    assert_eq!(std::fs::read(repo.join("keep.txt")).unwrap(), b"v1\n");
    assert!(!repo.join("scratch.txt").exists());
    let status = git_status(&repo, 5000).unwrap();
    assert!(status.is_empty());

    // Discarding an already-absent path is success, not an error.
    git_discard(&repo, &[PathBuf::from("scratch.txt")]).unwrap();
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn discard_rejects_traversal_before_git_runs() {
    let repo = fixture("traversal");
    std::fs::write(repo.join("a.txt"), b"v1\n").unwrap();
    commit_all(&repo, "init");
    for evil in [
        PathBuf::from("../outside.txt"),
        PathBuf::from("sub/../../outside.txt"),
    ] {
        for op in [
            git_stage(&repo, std::slice::from_ref(&evil)),
            git_unstage(&repo, std::slice::from_ref(&evil)),
            git_discard(&repo, std::slice::from_ref(&evil)),
        ] {
            assert!(
                matches!(op, Err(GitError::PathOutsideRoot)),
                "traversal must be rejected: {evil:?}"
            );
        }
    }
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn non_repo_and_locked_index_have_explicit_states() {
    let plain: PathBuf =
        std::env::temp_dir().join(format!("omaterm-m14-plain-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&plain);
    std::fs::create_dir_all(&plain).unwrap();
    assert!(matches!(git_status(&plain, 100), Err(GitError::NotARepo)));
    let _ = std::fs::remove_dir_all(&plain);

    // Locked index: a held `.git/index.lock` makes mutations fail loudly
    // with bounded stderr instead of hanging or silently succeeding.
    let repo = fixture("locked");
    std::fs::write(repo.join("a.txt"), b"v1\n").unwrap();
    commit_all(&repo, "init");
    std::fs::write(repo.join("a.txt"), b"v2\n").unwrap();
    std::fs::write(repo.join(".git").join("index.lock"), b"held").unwrap();
    let staged = git_stage(&repo, &[PathBuf::from("a.txt")]);
    assert!(
        matches!(staged, Err(GitError::GitFailed(_))),
        "locked index must surface git_failed, got {staged:?}"
    );
    // Status itself does not take the index lock and still works.
    assert!(git_status(&repo, 5000).is_ok());
    let _ = std::fs::remove_dir_all(&repo);
}

fn git_log_message(repo: &Path) -> String {
    let output = Command::new("git")
        .args(["log", "-1", "--format=%s"])
        .current_dir(repo)
        .stdin(Stdio::null())
        .stdout(std::process::Stdio::piped())
        .stderr(Stdio::null())
        .output()
        .expect("git log must spawn");
    assert!(output.status.success());
    String::from_utf8_lossy(&output.stdout).trim().to_owned()
}

#[test]
fn commit_writes_head_and_nothing_staged_fails_loudly() {
    let repo = fixture("commit");
    std::fs::write(repo.join("a.txt"), b"v1\n").unwrap();
    commit_all(&repo, "init");
    std::fs::write(repo.join("b.txt"), b"new\n").unwrap();
    git(&repo, &["add", "b.txt"]);

    let oid = git_commit(&repo, "add b").expect("commit must succeed");
    assert!(!oid.is_empty());
    assert_eq!(git_log_message(&repo), "add b");
    assert!(git_status(&repo, 5000).unwrap().is_empty());

    // Nothing staged is a loud failure, never an empty commit.
    let failed = git_commit(&repo, "empty");
    assert!(
        matches!(failed, Err(GitError::GitFailed(_))),
        "empty stage must fail loudly, got {failed:?}"
    );
    assert_eq!(git_log_message(&repo), "add b");
    let _ = std::fs::remove_dir_all(&repo);
}

#[test]
fn staged_rename_carries_both_paths_live() {
    let repo = fixture("rename");
    std::fs::write(repo.join("before.txt"), b"data\n").unwrap();
    commit_all(&repo, "init");
    git(&repo, &["mv", "before.txt", "after.txt"]);
    let status = git_status(&repo, 5000).unwrap();
    let renamed = status
        .staged
        .iter()
        .find(|e| e.path.as_path() == Path::new("after.txt"))
        .expect("staged rename must parse live");
    assert_eq!(
        renamed.renamed_from.as_deref(),
        Some(Path::new("before.txt"))
    );
    assert_eq!(renamed.x, 'R');
    let _ = std::fs::remove_dir_all(&repo);
}
