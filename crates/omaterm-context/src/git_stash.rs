//! Stash operations over the system git binary (C9.3): push, list, show,
//! apply, pop, drop.
//!
//! Same contract family as [`crate::git`] and [`crate::git_branch`]:
//! argument vectors only, M14 env hygiene, bounded waits with kill+reap.
//! Mutations ride [`GIT_MUTATION_TIMEOUT`]; list/show ride
//! [`GIT_STATUS_TIMEOUT`]. Auth never applies (stash is fully local).
//!
//! Reflog identity: every entry exposes its stable `stash@{n}` name; the
//! commit oid is read-only metadata (never accepted as input — callers
//! address entries by index only, so a dropped-and-recreated stash cannot
//! be confused with its predecessor).
//!
//! No `gpui` dependency.

use std::path::Path;

use crate::git::{GIT_MUTATION_TIMEOUT, GIT_STATUS_TIMEOUT, GitError, run_git};

/// Most stash entries listed in one call. The reflog is short in
/// practice; beyond this the parse caps with `truncated` set.
pub const MAX_STASHES: usize = 100;
/// Largest stash-list/show stdout kept in memory.
pub const MAX_STASH_BYTES: usize = 1024 * 1024;

/// One stash entry: stable reflog index, description, and commit oid.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitStash {
    pub index: usize,
    pub name: String,
    pub subject: String,
    pub oid: String,
}

/// Bounded stash listing, newest first.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitStashList {
    pub stashes: Vec<GitStash>,
    pub truncated: bool,
}

/// Validate a stash message: non-empty, 4096 bytes max, no control
/// characters (mirrors the commit-message contract minus newline/tab —
/// reflog subjects stay single-line).
fn check_message(message: &str) -> Result<(), GitError> {
    if message.is_empty() || message.len() > 4096 || message.chars().any(char::is_control) {
        return Err(GitError::GitFailed(
            "stash message must be 1 to 4096 bytes with no control characters".to_string(),
        ));
    }
    Ok(())
}

/// Stash tracked changes (worktree + index) with `message`. `untracked`
/// also stashes untracked files (`-u`); ignored files are never stashed.
/// Nothing-to-stash fails honestly through git (`GitFailed`).
pub fn git_stash_push(root: &Path, message: &str, untracked: bool) -> Result<GitStash, GitError> {
    check_message(message)?;
    let mut args = vec!["stash", "push", "-m", message];
    if untracked {
        args.push("-u");
    }
    run_git(root, &[], &args, GIT_MUTATION_TIMEOUT, 64 * 1024)?;
    // The new entry is stash@{0} by construction; read it back.
    let list = git_stash_list(root)?;
    list.stashes.into_iter().next().ok_or_else(|| {
        GitError::GitFailed("stash push succeeded but no entry was created".to_string())
    })
}

/// Bounded stash listing, newest first, with stable reflog indices.
pub fn git_stash_list(root: &Path) -> Result<GitStashList, GitError> {
    let output = run_git(
        root,
        &[],
        &[
            "--no-optional-locks",
            "stash",
            "list",
            &format!("--max-count={}", MAX_STASHES + 1),
        ],
        GIT_STATUS_TIMEOUT,
        MAX_STASH_BYTES,
    )?;
    let text = String::from_utf8_lossy(&output.stdout);
    let mut stashes = Vec::new();
    let mut truncated = output.stdout_capped;
    for record in text.lines() {
        if record.is_empty() {
            continue;
        }
        if stashes.len() >= MAX_STASHES {
            truncated = true;
            break;
        }
        // `stash@{0}: On main: subject` — index from the braces, subject
        // after the second colon.
        let Some(rest) = record.strip_prefix("stash@{") else {
            continue;
        };
        let Some((index, rest)) = rest.split_once('}') else {
            continue;
        };
        let Ok(index) = index.parse::<usize>() else {
            continue;
        };
        let subject = rest.splitn(2, ": ").nth(1).unwrap_or("").trim().to_owned();
        stashes.push(GitStash {
            index,
            name: format!("stash@{{{index}}}"),
            subject,
            oid: String::new(),
        });
    }
    // Oids enrich the listing (one rev-parse per entry, bounded count).
    for stash in stashes.iter_mut() {
        if let Ok(output) = run_git(
            root,
            &[],
            &["rev-parse", "--short", &stash.name],
            GIT_STATUS_TIMEOUT,
            64,
        ) {
            stash.oid = String::from_utf8_lossy(&output.stdout).trim().to_owned();
        }
    }
    Ok(GitStashList { stashes, truncated })
}

/// Changed paths of one stash entry (`show --name-only`), newest-first
/// index addressing. Read-only: never touches the worktree.
pub fn git_stash_show(root: &Path, index: usize) -> Result<Vec<String>, GitError> {
    if index > MAX_STASHES {
        return Err(GitError::GitFailed(format!(
            "stash index out of range: {index}"
        )));
    }
    let name = format!("stash@{{{index}}}");
    let output = run_git(
        root,
        &[],
        &["stash", "show", "--name-only", &name],
        GIT_STATUS_TIMEOUT,
        MAX_STASH_BYTES,
    )
    .map_err(|error| match error {
        GitError::GitFailed(stderr) => GitError::GitFailed(stderr),
        error => error,
    })?;
    Ok(String::from_utf8_lossy(&output.stdout)
        .lines()
        .filter(|line| !line.is_empty())
        .map(str::to_owned)
        .collect())
}

/// Re-apply an entry without dropping it. Conflicts fail honestly through
/// git (`GitFailed`); the entry stays. `--index` is not exposed in v1
/// (worktree-only restore, like checkout semantics).
pub fn git_stash_apply(root: &Path, index: usize) -> Result<(), GitError> {
    if index > MAX_STASHES {
        return Err(GitError::GitFailed(format!(
            "stash index out of range: {index}"
        )));
    }
    let name = format!("stash@{{{index}}}");
    run_git(
        root,
        &[],
        &["stash", "apply", &name],
        GIT_MUTATION_TIMEOUT,
        64 * 1024,
    )?;
    Ok(())
}

/// Re-apply an entry and drop it on success. A conflicted pop keeps the
/// entry (git drops only on success); failure surfaces as `GitFailed`.
pub fn git_stash_pop(root: &Path, index: usize) -> Result<(), GitError> {
    if index > MAX_STASHES {
        return Err(GitError::GitFailed(format!(
            "stash index out of range: {index}"
        )));
    }
    let name = format!("stash@{{{index}}}");
    run_git(
        root,
        &[],
        &["stash", "pop", &name],
        GIT_MUTATION_TIMEOUT,
        64 * 1024,
    )?;
    Ok(())
}

/// Drop an entry (destructive; the desktop arms two-step before dispatch).
pub fn git_stash_drop(root: &Path, index: usize) -> Result<(), GitError> {
    if index > MAX_STASHES {
        return Err(GitError::GitFailed(format!(
            "stash index out of range: {index}"
        )));
    }
    let name = format!("stash@{{{index}}}");
    run_git(
        root,
        &[],
        &["stash", "drop", &name],
        GIT_MUTATION_TIMEOUT,
        64 * 1024,
    )?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    struct TestRepo {
        path: std::path::PathBuf,
    }

    impl TestRepo {
        fn new() -> Self {
            static NEXT: AtomicUsize = AtomicUsize::new(0);
            let path = std::env::temp_dir().join(format!(
                "omaterm-git-stash-{}-{}",
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

    #[test]
    fn push_list_and_drop_round_trip() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        std::fs::write(repo.path().join("a.txt"), "dirty\n").unwrap();
        let pushed = git_stash_push(repo.path(), "work", false).unwrap();
        assert_eq!(pushed.index, 0);
        assert!(pushed.name == "stash@{0}");
        assert!(pushed.subject.contains("work"));
        assert!(!pushed.oid.is_empty());
        assert_eq!(
            std::fs::read_to_string(repo.path().join("a.txt")).unwrap(),
            "a\n"
        );

        let files = git_stash_show(repo.path(), 0).unwrap();
        assert_eq!(files, vec!["a.txt".to_string()]);

        git_stash_drop(repo.path(), 0).unwrap();
        let list = git_stash_list(repo.path()).unwrap();
        assert!(list.stashes.is_empty());
        assert!(!list.truncated);
    }

    #[test]
    fn push_validates_messages_and_honors_untracked() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        assert!(git_stash_push(repo.path(), "", false).is_err());
        std::fs::write(repo.path().join("new.txt"), "new\n").unwrap();
        let pushed = git_stash_push(repo.path(), "with-untracked", true).unwrap();
        assert_eq!(pushed.index, 0);
        assert!(!repo.path().join("new.txt").exists());
    }

    #[test]
    fn apply_keeps_pop_drops_and_conflicts_are_honest() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        std::fs::write(repo.path().join("a.txt"), "dirty\n").unwrap();
        git_stash_push(repo.path(), "work", false).unwrap();

        git_stash_apply(repo.path(), 0).unwrap();
        assert_eq!(git_stash_list(repo.path()).unwrap().stashes.len(), 1);
        // Restore the clean tree: apply leaves worktree edits behind.
        repo.git(&["checkout", "--quiet", "--", "a.txt"]);

        // Conflicting pop: a diverging commit blocks re-application, the
        // entry stays, and the error is GitFailed (not a silent drop).
        repo.commit("other.txt", "other\n");
        std::fs::write(repo.path().join("a.txt"), "conflict\n").unwrap();
        assert!(matches!(
            git_stash_pop(repo.path(), 0),
            Err(GitError::GitFailed(_))
        ));
        assert_eq!(git_stash_list(repo.path()).unwrap().stashes.len(), 1);

        // Clean pop drops the entry.
        repo.git(&["checkout", "--quiet", "--", "a.txt"]);
        git_stash_pop(repo.path(), 0).unwrap();
        assert!(git_stash_list(repo.path()).unwrap().stashes.is_empty());
    }

    #[test]
    fn bad_indices_fail_before_git_runs() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        for op in [
            git_stash_show(repo.path(), MAX_STASHES + 1).map(|_| ()),
            git_stash_apply(repo.path(), MAX_STASHES + 1),
            git_stash_pop(repo.path(), MAX_STASHES + 1),
            git_stash_drop(repo.path(), MAX_STASHES + 1),
        ] {
            assert!(matches!(op, Err(GitError::GitFailed(_))));
        }
        // Unknown index inside range fails honestly through git.
        assert!(matches!(
            git_stash_show(repo.path(), 7),
            Err(GitError::GitFailed(_))
        ));
    }
}
