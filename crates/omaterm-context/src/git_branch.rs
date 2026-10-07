//! Local branch operations over the system git binary (C9.1).
//!
//! Same contract family as [`crate::git`]: argument vectors only (never
//! shell interpolation), M14 env hygiene, bounded waits with kill+reap,
//! stable [`GitError`] codes. No `gpui` dependency.
//!
//! Reads (`list`) use [`GIT_STATUS_TIMEOUT`]; mutations
//! (create/checkout/delete/rename) use [`GIT_MUTATION_TIMEOUT`] since hooks
//! may run user code. Ref validation delegates to
//! `git check-ref-format` (version-proof); start points resolve through
//! `git rev-parse --verify` (no side effects). Checkout refuses a dirty
//! worktree before git ever runs (staged/unstaged changes; untracked files
//! only block when git itself refuses the switch).

use std::path::Path;

use omaterm_core::{GitBranch, GitBranchList, GitBranchTrack};

use crate::git::{GIT_MUTATION_TIMEOUT, GIT_STATUS_TIMEOUT, GitError, git_status, run_git};

/// Most refs listed in one call. Beyond this the parse caps with
/// `truncated` set — never an unbounded interface (blueprint §64).
pub const MAX_BRANCHES: usize = 500;
/// Largest `for-each-ref` stdout kept in memory.
pub const MAX_BRANCH_BYTES: usize = 1024 * 1024;

/// Validate a new branch name through git itself
/// (`check-ref-format --branch`): version-proof, no regex to drift.
fn check_branch_name(root: &Path, name: &str) -> Result<(), GitError> {
    if name.is_empty() || name.len() > 255 || name.chars().any(|c| c.is_control()) {
        return Err(GitError::GitFailed(format!(
            "invalid branch name: {name:?}"
        )));
    }
    let output = run_git(
        root,
        &[],
        &["check-ref-format", "--branch", name],
        GIT_STATUS_TIMEOUT,
        1024,
    );
    match output {
        Ok(_) => Ok(()),
        Err(GitError::GitFailed(_)) => Err(GitError::GitFailed(format!(
            "invalid branch name: {name:?}"
        ))),
        Err(error) => Err(error),
    }
}

/// Resolve a create/checkout start point without side effects
/// (`rev-parse --verify <rev>^{commit}`). Rejects empty input, control
/// characters, and anything git cannot resolve to a commit.
fn check_start_point(root: &Path, start: &str) -> Result<(), GitError> {
    if start.is_empty() || start.chars().any(|c| c.is_control()) {
        return Err(GitError::GitFailed(format!(
            "invalid start point: {start:?}"
        )));
    }
    let rev = format!("{start}^{{commit}}");
    match run_git(
        root,
        &[],
        &["rev-parse", "--verify", "--quiet", &rev],
        GIT_STATUS_TIMEOUT,
        1024,
    ) {
        Ok(_) => Ok(()),
        Err(GitError::GitFailed(_)) => Err(GitError::GitFailed(format!(
            "unknown start point: {start:?}"
        ))),
        Err(error) => Err(error),
    }
}

/// Current HEAD branch name, or `None` when detached.
fn head_branch(root: &Path) -> Result<Option<String>, GitError> {
    match run_git(
        root,
        &[],
        &["symbolic-ref", "--quiet", "--short", "HEAD"],
        GIT_STATUS_TIMEOUT,
        1024,
    ) {
        Ok(output) => Ok(Some(
            String::from_utf8_lossy(&output.stdout).trim().to_owned(),
        )),
        Err(GitError::GitFailed(_)) => Ok(None),
        Err(error) => Err(error),
    }
}

/// Short oid of HEAD (detached display and delete guards).
fn head_oid_short(root: &Path) -> Result<String, GitError> {
    let output = run_git(
        root,
        &[],
        &["rev-parse", "--short", "HEAD"],
        GIT_STATUS_TIMEOUT,
        1024,
    )?;
    Ok(String::from_utf8_lossy(&output.stdout).trim().to_owned())
}

fn parse_track(field: &str) -> GitBranchTrack {
    match field {
        "=" => GitBranchTrack::UpToDate,
        ">" => GitBranchTrack::Ahead(0),
        "<" => GitBranchTrack::Behind(0),
        "<>" => GitBranchTrack::Diverged {
            ahead: 0,
            behind: 0,
        },
        _ => GitBranchTrack::NoUpstream,
    }
}

/// Local branch list with HEAD identity and upstream tracking symbols.
/// Counts (ahead/behind numbers) are intentionally absent here: exact
/// counts need one `rev-list` per tracked branch, so the UI reads them
/// on demand for the current branch via [`git_ahead_behind`].
pub fn git_branch_list(root: &Path) -> Result<GitBranchList, GitError> {
    let head = head_branch(root)?;
    let detached_oid = if head.is_none() {
        Some(head_oid_short(root)?)
    } else {
        None
    };
    let output = run_git(
        root,
        &[],
        &[
            "--no-optional-locks",
            "for-each-ref",
            "--sort=refname",
            "--format=%(refname:short)%00%(upstream:short)%00%(upstream:trackshort)",
            "refs/heads",
        ],
        GIT_STATUS_TIMEOUT,
        MAX_BRANCH_BYTES,
    )?;
    let text = String::from_utf8_lossy(&output.stdout);
    let mut branches = Vec::new();
    let mut truncated = output.stdout_capped;
    for record in text.split('\n') {
        if record.is_empty() {
            continue;
        }
        if branches.len() >= MAX_BRANCHES {
            truncated = true;
            break;
        }
        let mut fields = record.split('\0');
        let name = fields.next().unwrap_or("").to_owned();
        if name.is_empty() {
            continue;
        }
        let upstream = fields.next().unwrap_or("");
        let track = fields.next().unwrap_or("");
        branches.push(GitBranch {
            is_head: Some(name.as_str()) == head.as_deref(),
            name,
            upstream: (!upstream.is_empty()).then(|| upstream.to_owned()),
            track: parse_track(track),
        });
    }
    Ok(GitBranchList {
        head,
        detached_oid,
        branches,
        truncated,
    })
}

/// Exact ahead/behind counts of HEAD against its upstream, or `None`
/// without an upstream (or outside a repository).
pub fn git_ahead_behind(root: &Path) -> Result<Option<(usize, usize)>, GitError> {
    let output = match run_git(
        root,
        &[],
        &[
            "--no-optional-locks",
            "rev-list",
            "--left-right",
            "--count",
            "HEAD...@{upstream}",
        ],
        GIT_STATUS_TIMEOUT,
        1024,
    ) {
        Ok(output) => output,
        Err(GitError::GitFailed(_)) => return Ok(None),
        Err(error) => return Err(error),
    };
    let text = String::from_utf8_lossy(&output.stdout);
    let mut parts = text.split_whitespace();
    match (parts.next(), parts.next()) {
        (Some(ahead), Some(behind)) => match (ahead.parse(), behind.parse()) {
            (Ok(ahead), Ok(behind)) => Ok(Some((ahead, behind))),
            _ => Err(GitError::GitFailed(format!(
                "unparseable ahead/behind counts: {text:?}"
            ))),
        },
        _ => Err(GitError::GitFailed(format!(
            "unparseable ahead/behind counts: {text:?}"
        ))),
    }
}

/// Create a local branch (no checkout). `start` resolves through
/// `rev-parse --verify` and defaults to HEAD when absent.
pub fn git_branch_create(root: &Path, name: &str, start: Option<&str>) -> Result<(), GitError> {
    check_branch_name(root, name)?;
    let mut args = vec!["branch".to_owned(), "--".to_owned(), name.to_owned()];
    if let Some(start) = start {
        check_start_point(root, start)?;
        args.push(start.to_owned());
    }
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    run_git(root, &[], &arg_refs, GIT_MUTATION_TIMEOUT, 64 * 1024)?;
    Ok(())
}

/// Checkout a local branch. Refuses upfront on a dirty worktree
/// (staged/unstaged changes) with [`GitError::DirtyWorktree`] instead of
/// letting git half-switch; untracked files only block when git itself
/// refuses, surfacing as `GitFailed` with git's own message.
pub fn git_branch_checkout(root: &Path, name: &str) -> Result<(), GitError> {
    check_branch_name(root, name)?;
    let status = git_status(root, usize::MAX)?;
    if !status.staged.is_empty() || !status.unstaged.is_empty() {
        return Err(GitError::DirtyWorktree);
    }
    // `switch`, not `checkout --`: the `--` would force pathspec mode and
    // refuse every branch name. Requires git ≥ 2.23 (already the floor
    // for `restore --source`).
    run_git(
        root,
        &[],
        &["switch", "--", name],
        GIT_MUTATION_TIMEOUT,
        64 * 1024,
    )?;
    Ok(())
}

/// Delete a local branch. The checked-out branch is refused upfront with
/// [`GitError::CurrentBranch`]; unmerged branches fail honestly through
/// `git branch -d` unless `force` selects `-D`.
pub fn git_branch_delete(root: &Path, name: &str, force: bool) -> Result<(), GitError> {
    check_branch_name(root, name)?;
    if head_branch(root)?.as_deref() == Some(name) {
        return Err(GitError::CurrentBranch);
    }
    let flag = if force { "-D" } else { "-d" };
    run_git(
        root,
        &[],
        &["branch", flag, "--", name],
        GIT_MUTATION_TIMEOUT,
        64 * 1024,
    )?;
    Ok(())
}

/// Rename a branch (`old` → `new`), including the checked-out one.
/// Name collisions fail honestly through git.
pub fn git_branch_rename(root: &Path, old: &str, new: &str) -> Result<(), GitError> {
    check_branch_name(root, old)?;
    check_branch_name(root, new)?;
    run_git(
        root,
        &[],
        &["branch", "-m", "--", old, new],
        GIT_MUTATION_TIMEOUT,
        64 * 1024,
    )?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::time::Duration;

    struct TestRepo {
        path: std::path::PathBuf,
    }

    impl TestRepo {
        fn new() -> Self {
            static NEXT: AtomicUsize = AtomicUsize::new(0);
            let path = std::env::temp_dir().join(format!(
                "omaterm-git-branch-{}-{}",
                std::process::id(),
                NEXT.fetch_add(1, Ordering::Relaxed)
            ));
            let _ = std::fs::remove_dir_all(&path);
            std::fs::create_dir_all(&path).unwrap();
            let repo = Self { path };
            repo.git(&["init", "--quiet", "-b", "main"]);
            repo.git(&["config", "user.name", "OmaTerm test"]);
            repo.git(&["config", "user.email", "test@example.invalid"]);
            repo
        }

        fn git(&self, args: &[&str]) {
            let status = std::process::Command::new("git")
                .args(args)
                .current_dir(&self.path)
                .env("GIT_TERMINAL_PROMPT", "0")
                .status()
                .expect("git binary runs");
            assert!(status.success(), "git {args:?} in test repo");
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

    #[test]
    fn branch_list_reports_head_upstream_and_detached() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        let list = git_branch_list(repo.path()).unwrap();
        assert_eq!(list.head.as_deref(), Some("main"));
        assert!(list.detached_oid.is_none());
        assert_eq!(list.branches.len(), 1);
        assert!(list.branches[0].is_head);
        assert_eq!(list.branches[0].track, GitBranchTrack::NoUpstream);

        git_branch_create(repo.path(), "feature", None).unwrap();
        let list = git_branch_list(repo.path()).unwrap();
        assert_eq!(list.branches.len(), 2);
        assert!(list.branches.iter().any(|b| b.name == "feature"));
        assert!(!list.truncated);

        // Detached HEAD reports the oid instead of a branch name.
        repo.git(&["checkout", "--quiet", "--detach", "HEAD"]);
        let list = git_branch_list(repo.path()).unwrap();
        assert!(list.head.is_none());
        assert!(
            list.detached_oid
                .as_ref()
                .is_some_and(|oid| !oid.is_empty())
        );
        assert!(list.branches.iter().all(|b| !b.is_head));
    }

    #[test]
    fn branch_create_validates_names_and_start_points() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        assert!(git_branch_create(repo.path(), "", None).is_err());
        assert!(git_branch_create(repo.path(), "has space..x", None).is_err());
        assert!(git_branch_create(repo.path(), "ok/name", Some("nope-missing")).is_err());
        git_branch_create(repo.path(), "ok/name", Some("HEAD")).unwrap();
        let list = git_branch_list(repo.path()).unwrap();
        assert!(list.branches.iter().any(|b| b.name == "ok/name"));
    }

    #[test]
    fn checkout_moves_head_and_refuses_dirty_worktrees() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        git_branch_create(repo.path(), "feature", None).unwrap();
        git_branch_checkout(repo.path(), "feature").unwrap();
        assert_eq!(
            git_branch_list(repo.path()).unwrap().head.as_deref(),
            Some("feature")
        );

        std::fs::write(repo.path().join("a.txt"), "dirty\n").unwrap();
        assert!(matches!(
            git_branch_checkout(repo.path(), "main"),
            Err(GitError::DirtyWorktree)
        ));
        // Still on feature: the refusal happened before git ran.
        assert_eq!(
            git_branch_list(repo.path()).unwrap().head.as_deref(),
            Some("feature")
        );
    }

    #[test]
    fn delete_refuses_head_and_unmerged_without_force() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        git_branch_create(repo.path(), "feature", None).unwrap();
        git_branch_checkout(repo.path(), "feature").unwrap();
        repo.commit("b.txt", "b\n");
        assert!(matches!(
            git_branch_delete(repo.path(), "feature", false),
            Err(GitError::CurrentBranch)
        ));
        git_branch_checkout(repo.path(), "main").unwrap();
        // Unmerged without force fails honestly through git.
        assert!(matches!(
            git_branch_delete(repo.path(), "feature", false),
            Err(GitError::GitFailed(_))
        ));
        git_branch_delete(repo.path(), "feature", true).unwrap();
        let list = git_branch_list(repo.path()).unwrap();
        assert!(list.branches.iter().all(|b| b.name != "feature"));
    }

    #[test]
    fn rename_moves_branches_including_head() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        git_branch_create(repo.path(), "old", None).unwrap();
        git_branch_rename(repo.path(), "old", "new").unwrap();
        git_branch_rename(repo.path(), "main", "trunk").unwrap();
        let list = git_branch_list(repo.path()).unwrap();
        assert_eq!(list.head.as_deref(), Some("trunk"));
        assert!(list.branches.iter().any(|b| b.name == "new"));
    }

    #[test]
    fn ahead_behind_is_none_without_upstream() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        assert_eq!(git_ahead_behind(repo.path()).unwrap(), None);
        // Non-repositories surface explicitly, not as zero counts.
        assert!(matches!(
            git_ahead_behind(std::env::temp_dir().as_path()),
            Err(GitError::NotARepo) | Ok(None)
        ));
    }

    #[test]
    fn branch_timeouts_stay_bounded() {
        // `git branch` against /tmp is NotARepo fast; the assertion is
        // that even adversarial inputs return rather than hang. The
        // subprocess deadlines above carry the real guarantee.
        let start = std::time::Instant::now();
        let _ = git_branch_list(std::env::temp_dir().as_path());
        assert!(
            start.elapsed() < Duration::from_secs(15),
            "branch list must stay bounded"
        );
    }
}
