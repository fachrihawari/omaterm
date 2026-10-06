//! Bounded commit-history reads over the system Git binary.
//!
//! This is deliberately separate from terminal history recovery. It exposes
//! immutable commit graph metadata only; changed-file and patch queries are
//! added after the shared command/IPC boundary exists.

use std::path::Path;

use omaterm_core::{
    GitCommitSummary, GitHistoryPage, GitHistoryScope, GitObjectId, GitRef, GitTimestamp,
};

use crate::git::{GIT_STATUS_TIMEOUT, GitError, run_git};

/// Maximum commits returned from one low-level read. The router's snapshot
/// cursor will page these immutable rows without turning the socket into an
/// unbounded graph traversal.
pub const MAX_GIT_HISTORY_COMMITS: usize = 100;
/// Maximum raw `git log` output retained for a single history read.
pub const MAX_GIT_HISTORY_BYTES: usize = 1024 * 1024;
/// Maximum author/subject display fields retained in a graph row.
pub const MAX_GIT_HISTORY_AUTHOR_BYTES: usize = 256;
pub const MAX_GIT_HISTORY_SUBJECT_BYTES: usize = 1024;

const FIELDS_PER_COMMIT: usize = 7;

/// Read the first page of commit graph metadata. The extra record establishes
/// `has_more` without a second subprocess. No path limiting is used because it
/// would simplify the graph and hide real parent edges.
pub fn git_history(
    root: &Path,
    scope: GitHistoryScope,
    limit: usize,
) -> Result<GitHistoryPage, GitError> {
    let limit = limit.clamp(1, MAX_GIT_HISTORY_COMMITS);
    let count = limit.saturating_add(1).to_string();
    let mut args = vec![
        "--no-optional-locks".to_owned(),
        "-c".to_owned(),
        "log.showSignature=false".to_owned(),
        "log".to_owned(),
        "--topo-order".to_owned(),
        "--date-order".to_owned(),
        "--no-patch".to_owned(),
        "-z".to_owned(),
        format!("--max-count={count}"),
        "--format=%H%x00%P%x00%an%x00%ae%x00%at%x00%aI%x00%s".to_owned(),
    ];
    match scope {
        GitHistoryScope::CurrentHead => args.push("HEAD".to_owned()),
        GitHistoryScope::AllLocalBranches => args.push("--branches".to_owned()),
    }
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    let output = match run_git(
        root,
        &[("LC_ALL", "C")],
        &arg_refs,
        GIT_STATUS_TIMEOUT,
        MAX_GIT_HISTORY_BYTES,
    ) {
        Ok(output) => output,
        // An unborn repository is a valid empty history state. Keep this
        // narrow and locale-stable by forcing LC_ALL=C above.
        Err(GitError::GitFailed(message))
            if scope == GitHistoryScope::CurrentHead
                && (message.contains("ambiguous argument 'HEAD'")
                    || message.contains("unknown revision or path")) =>
        {
            return Ok(GitHistoryPage {
                commits: Vec::new(),
                has_more: false,
                truncated: false,
            });
        }
        Err(error) => return Err(error),
    };
    Ok(parse_history_log(
        &output.stdout,
        output.stdout_capped,
        limit,
    ))
}

/// Parse the NUL-framed format emitted by [`git_history`]. Git commit subjects
/// cannot contain NUL, so an incomplete capped tail is safely discarded rather
/// than guessed. A malformed complete record marks the envelope truncated too.
pub fn parse_history_log(bytes: &[u8], output_capped: bool, limit: usize) -> GitHistoryPage {
    let limit = limit.clamp(1, MAX_GIT_HISTORY_COMMITS);
    let fields: Vec<&[u8]> = bytes.split(|byte| *byte == 0).collect();
    let complete_fields = if bytes.last() == Some(&0) {
        fields.len().saturating_sub(1)
    } else {
        fields.len() / FIELDS_PER_COMMIT * FIELDS_PER_COMMIT
    };
    let mut truncated = output_capped || complete_fields != fields.len().saturating_sub(1);
    let mut commits = Vec::with_capacity(limit.saturating_add(1));
    let (records, remainder) = fields[..complete_fields].as_chunks::<FIELDS_PER_COMMIT>();
    debug_assert!(remainder.is_empty());
    for record in records {
        let Some(commit) = parse_record(record) else {
            truncated = true;
            break;
        };
        commits.push(commit);
    }
    let has_more = commits.len() > limit;
    commits.truncate(limit);
    GitHistoryPage {
        commits,
        has_more,
        truncated,
    }
}

fn parse_record(record: &[&[u8]]) -> Option<GitCommitSummary> {
    let id = GitObjectId::parse(std::str::from_utf8(record[0]).ok()?).ok()?;
    let parents = if record[1].is_empty() {
        Vec::new()
    } else {
        std::str::from_utf8(record[1])
            .ok()?
            .split_ascii_whitespace()
            .map(GitObjectId::parse)
            .collect::<Result<Vec<_>, _>>()
            .ok()?
    };
    let author_time = std::str::from_utf8(record[4]).ok()?.parse().ok()?;
    let offset_minutes = parse_iso_offset(std::str::from_utf8(record[5]).ok()?)?;
    Some(GitCommitSummary {
        id,
        parents,
        author_name: truncate_utf8(record[2], MAX_GIT_HISTORY_AUTHOR_BYTES),
        author_email: truncate_utf8(record[3], MAX_GIT_HISTORY_AUTHOR_BYTES),
        author_time: GitTimestamp {
            unix_seconds: author_time,
            offset_minutes,
        },
        subject: truncate_utf8(record[6], MAX_GIT_HISTORY_SUBJECT_BYTES),
        refs: Vec::<GitRef>::new(),
        shallow_boundary: false,
    })
}

fn parse_iso_offset(value: &str) -> Option<i16> {
    let offset = value.rsplit_once(['+', '-'])?.1;
    let sign = if value
        .as_bytes()
        .get(value.len().saturating_sub(offset.len() + 1))?
        == &b'+'
    {
        1
    } else {
        -1
    };
    let (hours, minutes) = offset.split_once(':')?;
    let hours: i16 = hours.parse().ok()?;
    let minutes: i16 = minutes.parse().ok()?;
    (hours <= 23 && minutes <= 59).then_some(sign * (hours * 60 + minutes))
}

fn truncate_utf8(bytes: &[u8], max_bytes: usize) -> String {
    let text = String::from_utf8_lossy(bytes);
    if text.len() <= max_bytes {
        return text.into_owned();
    }
    let mut end = max_bytes;
    while end > 0 && !text.is_char_boundary(end) {
        end -= 1;
    }
    text[..end].to_owned()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    const SHA1_A: &str = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    const SHA1_B: &str = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";

    fn record(id: &str, parents: &str, subject: &str) -> Vec<u8> {
        [
            id,
            parents,
            "Ada",
            "ada@example.test",
            "10",
            "1970-01-01T00:00:10+05:30",
            subject,
        ]
        .join("\0")
        .into_bytes()
        .into_iter()
        .chain(std::iter::once(0))
        .collect()
    }

    #[test]
    fn parses_complete_nul_framed_history_and_detects_next_page() {
        let mut bytes = record(SHA1_A, SHA1_B, "first");
        bytes.extend(record(SHA1_B, "", "second"));
        let page = parse_history_log(&bytes, false, 1);

        assert!(page.has_more);
        assert!(!page.truncated);
        assert_eq!(page.commits.len(), 1);
        assert_eq!(page.commits[0].id.as_str(), SHA1_A);
        assert_eq!(page.commits[0].parents[0].as_str(), SHA1_B);
        assert_eq!(page.commits[0].author_time.offset_minutes, 330);
    }

    #[test]
    fn drops_partial_or_invalid_tail_without_fabricating_a_commit() {
        let mut bytes = record(SHA1_A, "", "first");
        bytes.extend_from_slice(SHA1_B.as_bytes());
        bytes.extend_from_slice(b"\0bad");
        let page = parse_history_log(&bytes, true, 10);

        assert!(page.truncated);
        assert_eq!(page.commits.len(), 1);
        assert_eq!(page.commits[0].subject, "first");
    }

    #[test]
    fn invalid_complete_record_is_reported_as_truncation() {
        let mut bytes = b"not-an-oid\0\0Ada\0a@b\0".to_vec();
        bytes.extend([b'0', 0]);
        bytes.extend(b"1970-01-01T00:00:00+00:00\0x\0");
        let page = parse_history_log(&bytes, false, 10);

        assert!(page.truncated);
        assert!(page.commits.is_empty());
    }

    #[test]
    fn git_history_reads_empty_and_linear_repositories() {
        let repo = TestRepo::new();
        assert!(
            git_history(repo.path(), GitHistoryScope::CurrentHead, 50)
                .unwrap()
                .commits
                .is_empty()
        );

        repo.commit("first", "a.txt", "a");
        repo.commit("second", "a.txt", "b");
        let page = git_history(repo.path(), GitHistoryScope::CurrentHead, 1).unwrap();
        assert_eq!(page.commits.len(), 1);
        assert_eq!(page.commits[0].subject, "second");
        assert!(page.has_more);
        assert_eq!(page.commits[0].parents.len(), 1);
    }

    struct TestRepo {
        path: std::path::PathBuf,
    }

    impl TestRepo {
        fn new() -> Self {
            static NEXT: AtomicUsize = AtomicUsize::new(0);
            let path = std::env::temp_dir().join(format!(
                "omaterm-git-history-{}-{}",
                std::process::id(),
                NEXT.fetch_add(1, Ordering::Relaxed)
            ));
            std::fs::create_dir_all(&path).unwrap();
            let repo = Self { path };
            repo.git(&["init", "--quiet"]);
            repo.git(&["config", "user.name", "OmaTerm test"]);
            repo.git(&["config", "user.email", "test@example.invalid"]);
            repo
        }

        fn path(&self) -> &Path {
            &self.path
        }

        fn commit(&self, message: &str, path: &str, contents: &str) {
            std::fs::write(self.path.join(path), contents).unwrap();
            self.git(&["add", "--", path]);
            self.git(&["commit", "--quiet", "-m", message]);
        }

        fn git(&self, args: &[&str]) {
            let status = std::process::Command::new("git")
                .args(args)
                .current_dir(&self.path)
                .status()
                .unwrap();
            assert!(status.success(), "git {args:?}");
        }
    }

    impl Drop for TestRepo {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.path);
        }
    }
}
