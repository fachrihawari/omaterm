//! Network sync over the system git binary (C9.2): fetch, pull (ff-only),
//! push (with optional set-upstream).
//!
//! Same contract family as [`crate::git`] and [`crate::git_branch`]:
//! argument vectors only, M14 env hygiene, bounded waits with kill+reap.
//! Network calls use [`GIT_SYNC_TIMEOUT`] (hooks aside, remotes hang more
//! than local mutations). Auth flows reuse the user's ssh-agent / credential
//! helpers; `GIT_TERMINAL_PROMPT=0` (already set by the runner) means an
//! auth that needs interaction fails fast as [`GitError::AuthFailed`]
//! instead of hanging. No tokens are ever stored.
//!
//! Diverged / non-fast-forward states are detected before dispatching:
//! pull refuses a diverged HEAD with [`GitError::Diverged`], push refuses a
//! non-fast-forward with [`GitError::NonFastForward`] — never an implicit
//! merge or force. `push --set-upstream` is explicit only.
//!
//! No `gpui` dependency.

use std::path::Path;

use crate::git::{GIT_MUTATION_TIMEOUT, GitError, run_git};
use crate::git_branch::{git_ahead_behind, git_branch_list};

/// Bounded network wait. Longer than local mutations: remotes hang more
/// than hooks do. Still bounded — a wedged remote costs at most this.
pub const GIT_SYNC_TIMEOUT: Duration = Duration::from_secs(60);
/// Largest sync stdout/stderr kept (fetch/pull/push print summaries only).
pub const MAX_SYNC_BYTES: usize = 64 * 1024;

use std::time::Duration;

/// Sync outcome summary for toasts: what moved, in which direction.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitSyncReport {
    pub operation: &'static str,
    pub detail: String,
}

/// Failure classification from bounded stderr, checked in order:
/// auth (credential/interactive), offline (network/DNS), diverged merge
/// state, non-fast-forward push. Anything else stays `GitFailed` with the
/// raw stderr, and callers key off the stable code, never the text.
fn classify_sync_error(stderr: &str) -> GitError {
    let lower = stderr.to_lowercase();
    if lower.contains("could not read username")
        || lower.contains("authentication failed")
        || lower.contains("permission denied (publickey)")
        || lower.contains("invalid username or password")
        || lower.contains("terminal prompts disabled")
    {
        GitError::AuthFailed
    } else if lower.contains("could not resolve host")
        || lower.contains("connection timed out")
        || lower.contains("connection refused")
        || lower.contains("network is unreachable")
        || lower.contains("unable to connect")
        || lower.contains("failed to connect")
    {
        GitError::Offline
    } else {
        GitError::GitFailed(stderr.to_owned())
    }
}

fn run_sync(root: &Path, args: &[&str]) -> Result<String, GitError> {
    let arg_refs: Vec<&str> = args.to_vec();
    match run_git(root, &[], &arg_refs, GIT_SYNC_TIMEOUT, MAX_SYNC_BYTES) {
        Ok(output) => Ok(String::from_utf8_lossy(&output.stdout).trim().to_owned()),
        Err(GitError::GitFailed(stderr)) => Err(classify_sync_error(&stderr)),
        Err(error) => Err(error),
    }
}

/// `git fetch` for the default remote (or `remote` when given). Prunes
/// stale remote-tracking refs by default. Never touches the worktree.
pub fn git_fetch(root: &Path, remote: Option<&str>) -> Result<GitSyncReport, GitError> {
    let mut args = vec!["fetch", "--prune"];
    if let Some(remote) = remote {
        if remote.is_empty()
            || remote.len() > 255
            || remote.starts_with('-')
            || remote.chars().any(|c| c.is_control())
        {
            return Err(GitError::GitFailed(format!("invalid remote: {remote:?}")));
        }
        args.push(remote);
    }
    let detail = run_sync(root, &args)?;
    Ok(GitSyncReport {
        operation: "fetch",
        detail,
    })
}

/// Local HEAD oid, short form, for pull/push reports.
fn head_short(root: &Path) -> Result<String, GitError> {
    let output = run_git(
        root,
        &[],
        &["rev-parse", "--short", "HEAD"],
        GIT_MUTATION_TIMEOUT,
        1024,
    )?;
    Ok(String::from_utf8_lossy(&output.stdout).trim().to_owned())
}

/// `git pull --ff-only` of the upstream into the current branch.
/// Refuses upfront when HEAD is diverged ([`GitError::Diverged`]), without
/// an upstream ([`GitError::NoUpstream`]), or on a dirty worktree
/// ([`GitError::DirtyWorktree`] — merge machinery must not run over local
/// edits). Never rebases, never merges diverged histories.
pub fn git_pull(root: &Path, remote: Option<&str>) -> Result<GitSyncReport, GitError> {
    if let Some((ahead, behind)) = git_ahead_behind(root)? {
        if ahead > 0 && behind > 0 {
            return Err(GitError::Diverged);
        }
        if ahead > 0 || behind == 0 {
            // Ahead-only: nothing to fetch into us. Behind-zero with no
            // divergence is already current; report instead of a no-op pull.
            let _ = ahead;
        }
    } else {
        return Err(GitError::NoUpstream);
    }
    let status = crate::git::git_status(root, usize::MAX)?;
    if !status.staged.is_empty() || !status.unstaged.is_empty() {
        return Err(GitError::DirtyWorktree);
    }
    let before = head_short(root)?;
    let mut args = vec!["pull", "--ff-only", "--no-rebase"];
    if let Some(remote) = remote {
        if remote.is_empty()
            || remote.len() > 255
            || remote.starts_with('-')
            || remote.chars().any(|c| c.is_control())
        {
            return Err(GitError::GitFailed(format!("invalid remote: {remote:?}")));
        }
        args.push(remote);
    }
    run_sync(root, &args)?;
    let after = head_short(root)?;
    let detail = if before == after {
        "already up to date".to_string()
    } else {
        format!("{before}..{after}")
    };
    Ok(GitSyncReport {
        operation: "pull",
        detail,
    })
}

/// `git push` of the current branch. Refuses upfront on non-fast-forward
/// ([`GitError::NonFastForward`]) — pass `set_upstream` to publish a new
/// branch (`push -u origin HEAD`), never force. With an upstream and no
/// local divergence the push is a plain fast-forward.
pub fn git_push(root: &Path, set_upstream: bool) -> Result<GitSyncReport, GitError> {
    let head = git_branch_list(root)?.head;
    let Some(branch) = head else {
        return Err(GitError::GitFailed(
            "cannot push a detached HEAD".to_string(),
        ));
    };
    if set_upstream {
        run_sync(root, &["push", "-u", "origin", "HEAD"])?;
        return Ok(GitSyncReport {
            operation: "push",
            detail: format!("published {branch} to origin"),
        });
    }
    match git_ahead_behind(root)? {
        None => {
            return Err(GitError::NoUpstream);
        }
        Some((_, behind)) if behind > 0 => {
            return Err(GitError::NonFastForward);
        }
        _ => {}
    }
    run_sync(root, &["push"])?;
    Ok(GitSyncReport {
        operation: "push",
        detail: format!("pushed {branch}"),
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    struct TestRepo {
        path: std::path::PathBuf,
    }

    impl TestRepo {
        fn new(name: &str) -> Self {
            static NEXT: AtomicUsize = AtomicUsize::new(0);
            let path = std::env::temp_dir().join(format!(
                "omaterm-git-sync-{}-{}-{}",
                name,
                std::process::id(),
                NEXT.fetch_add(1, Ordering::Relaxed)
            ));
            let _ = std::fs::remove_dir_all(&path);
            std::fs::create_dir_all(&path).unwrap();
            let repo = Self { path };
            repo.git(&["init", "--quiet", "-b", "main"]);
            repo.git(&["config", "user.name", "OmaTerm test"]);
            repo.git(&["config", "user.email", "test@example.invalid"]);
            repo.git(&["config", "commit.gpgsign", "false"]);
            repo
        }

        fn git(&self, args: &[&str]) {
            let status = std::process::Command::new("git")
                .args(args)
                .current_dir(&self.path)
                .env("GIT_TERMINAL_PROMPT", "0")
                .status()
                .expect("git binary runs");
            assert!(status.success(), "git {args:?} in {}", self.path.display());
        }

        fn commit(&self, name: &str, contents: &str) {
            std::fs::write(self.path.join(name), contents).unwrap();
            self.git(&["add", "-A"]);
            self.git(&["commit", "--quiet", "-m", name]);
        }

        fn path(&self) -> &Path {
            &self.path
        }
    }

    impl Drop for TestRepo {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.path);
        }
    }

    /// Bare remote + two clones: the offline-capable sync fixture.
    fn paired() -> (std::path::PathBuf, TestRepo, TestRepo) {
        static NEXT: AtomicUsize = AtomicUsize::new(0);
        let base = std::env::temp_dir().join(format!(
            "omaterm-git-sync-remote-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        let _ = std::fs::remove_dir_all(&base);
        std::fs::create_dir_all(&base).unwrap();
        let remote = base.join("remote.git");
        let status = std::process::Command::new("git")
            .args(["init", "--quiet", "--bare", "-b", "main"])
            .arg(&remote)
            .status()
            .unwrap();
        assert!(status.success());
        let left = TestRepo::new("left");
        left.git(&["remote", "add", "origin", remote.to_str().unwrap()]);
        left.commit("a.txt", "a\n");
        left.git(&["push", "--quiet", "-u", "origin", "main"]);
        let right_path = base.join("right");
        let status = std::process::Command::new("git")
            .args([
                "clone",
                "--quiet",
                remote.to_str().unwrap(),
                right_path.to_str().unwrap(),
            ])
            .status()
            .unwrap();
        assert!(status.success());
        let right = TestRepo { path: right_path };
        right.git(&["config", "user.name", "OmaTerm test"]);
        right.git(&["config", "user.email", "test@example.invalid"]);
        right.git(&["config", "commit.gpgsign", "false"]);
        (base, left, right)
    }

    #[test]
    fn fetch_and_pull_move_across_clones() {
        let (_base, left, right) = paired();
        left.commit("b.txt", "b\n");
        left.git(&["push", "--quiet", "origin", "main"]);
        let report = git_fetch(right.path(), None).unwrap();
        assert_eq!(report.operation, "fetch");
        let report = git_pull(right.path(), None).unwrap();
        assert_eq!(report.operation, "pull");
        assert!(
            report.detail.contains("..") || report.detail == "already up to date",
            "{}",
            report.detail
        );
        assert!(right.path().join("b.txt").exists());
    }

    #[test]
    fn pull_refuses_dirty_worktrees_before_running() {
        let (_base, left, right) = paired();
        left.commit("b.txt", "b\n");
        left.git(&["push", "--quiet", "origin", "main"]);
        git_fetch(right.path(), None).unwrap();
        std::fs::write(right.path().join("a.txt"), "dirty\n").unwrap();
        assert!(matches!(
            git_pull(right.path(), None),
            Err(GitError::DirtyWorktree)
        ));
    }

    #[test]
    fn diverged_pull_and_behind_push_are_refused() {
        let (_base, left, right) = paired();
        left.commit("l.txt", "l\n");
        right.commit("r.txt", "r\n");
        left.git(&["push", "--quiet", "origin", "main"]);
        git_fetch(right.path(), None).unwrap();
        assert!(matches!(
            git_pull(right.path(), None),
            Err(GitError::Diverged)
        ));
        // Right is ahead of the remote too, but behind as well: push would
        // be non-fast-forward.
        assert!(matches!(
            git_push(right.path(), false),
            Err(GitError::NonFastForward)
        ));
    }

    #[test]
    fn push_without_upstream_is_an_explicit_error() {
        let repo = TestRepo::new("lonely");
        repo.commit("a.txt", "a\n");
        assert!(matches!(
            git_push(repo.path(), false),
            Err(GitError::NoUpstream)
        ));
    }

    #[test]
    fn push_set_upstream_publishes_new_branches() {
        let base =
            std::env::temp_dir().join(format!("omaterm-git-sync-pub-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&base);
        std::fs::create_dir_all(&base).unwrap();
        let remote = base.join("remote.git");
        let status = std::process::Command::new("git")
            .args(["init", "--quiet", "--bare", "-b", "main"])
            .arg(&remote)
            .status()
            .unwrap();
        assert!(status.success());
        let repo = TestRepo::new("pub");
        repo.git(&["remote", "add", "origin", remote.to_str().unwrap()]);
        repo.commit("a.txt", "a\n");
        repo.git(&["checkout", "--quiet", "-b", "topic"]);
        repo.commit("t.txt", "t\n");
        let report = git_push(repo.path(), true).unwrap();
        assert_eq!(report.operation, "push");
        assert!(report.detail.contains("topic"), "{}", report.detail);
        let _ = std::fs::remove_dir_all(&base);
    }

    #[test]
    fn unreachable_remotes_map_to_offline_not_git_failed() {
        let repo = TestRepo::new("off");
        repo.commit("a.txt", "a\n");
        repo.git(&[
            "remote",
            "add",
            "origin",
            "https://nonexistent.invalid.example/repo.git",
        ]);
        assert!(matches!(
            git_fetch(repo.path(), None),
            Err(GitError::Offline) | Err(GitError::AuthFailed)
        ));
    }

    #[test]
    fn error_codes_are_stable() {
        assert_eq!(GitError::AuthFailed.code(), "auth_failed");
        assert_eq!(GitError::Offline.code(), "offline");
        assert_eq!(GitError::Diverged.code(), "diverged");
        assert_eq!(GitError::NonFastForward.code(), "non_fast_forward");
        assert_eq!(GitError::NoUpstream.code(), "no_upstream");
    }
}
