//! Per-line blame over the system git binary (C9.4a).
//!
//! Same contract family as [`crate::git`]: argument vectors only, M14 env
//! hygiene, bounded waits with kill+reap. Reads ride
//! [`GIT_STATUS_TIMEOUT`]; the caller (a diff worker) owns threading and
//! cancellation. Output is parsed from `git blame --porcelain` (NUL-safe
//! line framing): one commit header per hunk plus author/time/summary.
//!
//! Bounds: at most [`MAX_BLAME_LINES`] rows per file, [`MAX_BLAME_BYTES`]
//! stdout — tail rows drop with `truncated` set, never an unbounded
//! interface (blueprint §64). Uncommitted lines surface with the all-zero
//! oid and `GitBlameEntry::uncommitted` set (VSCode's "Not Committed Yet").
//! Binary files fail honestly through git; paths resolve under the root
//! like every other reader.
//!
//! No `gpui` dependency.

use std::path::{Path, PathBuf};

use crate::git::{GIT_STATUS_TIMEOUT, GitError, join_under_root, run_git};

/// Most blame rows per file. Matches the diff row budget family; the tail
/// drops with `truncated` set.
pub const MAX_BLAME_LINES: usize = 5_000;
/// Largest blame stdout kept in memory.
pub const MAX_BLAME_BYTES: usize = 4 * 1024 * 1024;

/// One blamed line: 1-based final line number, short commit oid, author,
/// commit time, subject, and whether the line is uncommitted worktree text.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitBlameLine {
    pub line: usize,
    pub commit: String,
    pub author: String,
    pub author_time: i64,
    pub subject: String,
    pub uncommitted: bool,
}

/// Bounded per-file blame, final-line order.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitBlame {
    pub path: PathBuf,
    pub lines: Vec<GitBlameLine>,
    pub truncated: bool,
}

/// Blame one root-relative path (`git blame --porcelain -- <path>`).
/// `path` boundary-checks like stage/unstage paths. Binary files and
/// missing paths fail honestly through git (`GitFailed`).
pub fn git_blame(root: &Path, path: &Path) -> Result<GitBlame, GitError> {
    let joined = join_under_root(root, path)?;
    let display = joined.to_string_lossy().into_owned();
    if display.is_empty() || display.len() > 4096 || display.chars().any(char::is_control) {
        return Err(GitError::GitFailed(format!("invalid blame path: {path:?}")));
    }
    let output = run_git(
        root,
        &[],
        &[
            "--no-optional-locks",
            "blame",
            "--porcelain",
            "--",
            &display,
        ],
        GIT_STATUS_TIMEOUT,
        MAX_BLAME_BYTES,
    )?;
    let text = String::from_utf8_lossy(&output.stdout);
    let mut packed: Vec<(usize, String)> = Vec::new();
    let mut truncated = output.stdout_capped;
    let mut commit = String::new();
    let mut author = String::new();
    let mut author_time = 0i64;
    let mut subject = String::new();
    // Fully-read headers by commit oid: repeated hunks of one commit
    // (`boundary` commits especially) restate only oid + line numbers,
    // so content lines without a fresh author block inherit from the
    // commit's earlier hunk.
    let mut headers: std::collections::HashMap<String, (String, i64, String)> =
        std::collections::HashMap::new();
    for record in text.lines() {
        if record.is_empty() {
            continue;
        }
        // Boundary commits prefix the oid with `^` (root-commit hunks):
        // strip it before matching so those lines parse, BEFORE the 40-hex
        // check sees a 41-char token and skips the whole hunk.
        let bare = record.strip_prefix('^').unwrap_or(record);
        if let Some(hex) = bare
            .split_whitespace()
            .next()
            .filter(|hex| hex.len() == 40 && hex.bytes().all(|b| b.is_ascii_hexdigit()))
            && let Some(final_line) = bare
                .strip_prefix(hex)
                .and_then(|rest| rest.split_whitespace().next())
                .and_then(|token| token.parse::<usize>().ok())
        {
            commit = hex.to_owned();
            author.clear();
            author_time = 0;
            subject.clear();
            if packed.len() >= MAX_BLAME_LINES {
                truncated = true;
                break;
            }
            // Stash the commit; author/subject fill in over the next
            // records; the `\t` content line commits the row.
            packed.push((final_line, commit.clone()));
            continue;
        }
        if let Some(value) = record.strip_prefix("author ") {
            author = value.trim().to_owned();
        } else if let Some(value) = record.strip_prefix("author-time ") {
            author_time = value.trim().parse().unwrap_or(0);
        } else if let Some(value) = record.strip_prefix("summary ") {
            subject = value.trim().to_owned();
            headers.insert(
                commit.clone(),
                (author.clone(), author_time, subject.clone()),
            );
        } else if record.starts_with('\t') {
            // Content line: commit the pending row with the current
            // header, or the last fully-read header when this hunk
            // restated only oid + line numbers.
            if let Some((final_line, pending_commit)) = packed.pop() {
                // Inherit from this commit's earlier hunk when the
                // current hunk restated only oid + line numbers (no
                // author block). Keyed by oid, so an unrelated previous
                // hunk can never leak across.
                let (resolved_author, resolved_time, resolved_subject) =
                    if author.is_empty() && subject.is_empty() {
                        match headers.get(&pending_commit) {
                            Some((a, t, s)) => (a.clone(), *t, s.clone()),
                            None => (author.clone(), author_time, subject.clone()),
                        }
                    } else {
                        (author.clone(), author_time, subject.clone())
                    };
                let resolved_commit = pending_commit.clone();
                let _ = pending_commit;
                packed.push((
                    final_line,
                    format!(
                        "{resolved_commit}|{resolved_author}|{resolved_time}|{resolved_subject}"
                    ),
                ));
            }
        }
    }
    // Second pass: expand the packed rows into entries, dropping the
    // `\t` content (the diff panel already owns the text).
    let mut out = Vec::with_capacity(packed.len().min(MAX_BLAME_LINES));
    for (line, packed) in packed {
        if out.len() >= MAX_BLAME_LINES {
            truncated = true;
            break;
        }
        let mut parts = packed.splitn(4, '|');
        let commit = parts.next().unwrap_or("").to_owned();
        let author = parts.next().unwrap_or("").to_owned();
        let author_time = parts.next().unwrap_or("0").parse().unwrap_or(0);
        let subject = parts.next().unwrap_or("").to_owned();
        let uncommitted = commit.bytes().all(|b| b == b'0');
        out.push(GitBlameLine {
            line,
            commit: commit.chars().take(8).collect(),
            author,
            author_time,
            subject,
            uncommitted,
        });
    }
    out.sort_by_key(|entry| entry.line);
    Ok(GitBlame {
        path: joined,
        lines: out,
        truncated,
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
        fn new() -> Self {
            static NEXT: AtomicUsize = AtomicUsize::new(0);
            let path = std::env::temp_dir().join(format!(
                "omaterm-git-blame-{}-{}",
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
    fn blame_attributes_lines_to_commits() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "one\ntwo\nthree\n");
        std::fs::write(repo.path().join("a.txt"), "one\nTWO\nthree\n").unwrap();
        repo.git(&["add", "-A"]);
        repo.git(&["commit", "--quiet", "-m", "second"]);
        let blame = git_blame(repo.path(), Path::new("a.txt")).unwrap();
        assert_eq!(blame.lines.len(), 3);
        assert_eq!(blame.lines[0].line, 1);
        assert_eq!(blame.lines[1].line, 2);
        assert_eq!(blame.lines[1].subject, "second");
        assert_ne!(blame.lines[0].commit, blame.lines[1].commit);
        assert_eq!(blame.lines[2].subject, blame.lines[0].subject);
        assert!(!blame.truncated);
        assert!(blame.lines.iter().all(|line| !line.uncommitted));
    }

    #[test]
    fn uncommitted_lines_are_flagged_not_hidden() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "one\n");
        std::fs::write(repo.path().join("a.txt"), "one\nTWO\n").unwrap();
        let blame = git_blame(repo.path(), Path::new("a.txt")).unwrap();
        assert_eq!(blame.lines.len(), 2);
        assert!(!blame.lines[0].uncommitted);
        assert!(blame.lines[1].uncommitted);
        assert_eq!(blame.lines[1].author, "Not Committed Yet");
    }

    #[test]
    fn missing_paths_and_escapes_fail_honestly() {
        let repo = TestRepo::new();
        repo.commit("a.txt", "a\n");
        assert!(matches!(
            git_blame(repo.path(), Path::new("missing.txt")),
            Err(GitError::GitFailed(_))
        ));
        assert!(matches!(
            git_blame(repo.path(), Path::new("../escape.txt")),
            Err(GitError::PathOutsideRoot) | Err(GitError::GitFailed(_))
        ));
    }
}
