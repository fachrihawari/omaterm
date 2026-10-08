//! Bounded commit-history reads over the system Git binary.
//!
//! This is deliberately separate from terminal history recovery. It exposes
//! immutable commit graph metadata only; changed-file and patch queries are
//! added after the shared command/IPC boundary exists.

use std::path::{Path, PathBuf};

#[cfg(unix)]
use std::os::unix::ffi::OsStringExt;

use omaterm_core::{
    DiffInfo, GitCommitFile, GitCommitFileKind, GitCommitFiles, GitCommitSummary,
    GitComparisonBase, GitHistoryPage, GitHistoryScope, GitObjectId, GitRef, GitRefKind,
    GitTimestamp,
};

use crate::diff::{MAX_DIFF_BYTES, MAX_DIFF_CONTEXT_LINES, parse_diff};
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
/// Largest full commit body kept per history row (C9.4). Subjects stay
/// capped separately; parsing truncates bodies at this bound.
pub const MAX_GIT_COMMIT_BODY_BYTES: usize = 8 * 1024;
/// Maximum changed-file records retained for one expanded commit.
pub const MAX_GIT_COMMIT_FILES: usize = 500;
/// Maximum raw `git diff --raw` output retained for one commit file listing.
pub const MAX_GIT_COMMIT_FILES_BYTES: usize = 1024 * 1024;

const FIELDS_PER_COMMIT: usize = 9;

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
        // %D is the decoration list (`HEAD -> main, origin/main, tag: v1`);
        // parsed into GitRef entries for badges, never shown raw.
        "--format=%H%x00%P%x00%an%x00%ae%x00%at%x00%aI%x00%s%x00%D%x00%B".to_owned(),
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

/// Read the ordered parent object IDs of `commit`. Used to validate a requested
/// comparison parent against the commit's real parents (a nonparent would
/// produce a diff between two unrelated trees).
pub fn git_commit_parents(root: &Path, commit: &GitObjectId) -> Result<Vec<GitObjectId>, GitError> {
    let output = run_git(
        root,
        &[("LC_ALL", "C")],
        &["rev-list", "--parents", "-n", "1", commit.as_str()],
        GIT_STATUS_TIMEOUT,
        4096,
    )?;
    let text = String::from_utf8_lossy(&output.stdout);
    let mut fields = text.split_ascii_whitespace();
    let Some(first) = fields.next() else {
        return Err(GitError::GitFailed("commit not found".into()));
    };
    if first != commit.as_str() {
        return Err(GitError::GitFailed("commit not found".into()));
    }
    fields
        .map(GitObjectId::parse)
        .collect::<Result<Vec<_>, _>>()
        .map_err(|_| GitError::GitFailed("git returned an invalid parent object ID".into()))
}

/// List the changed files of `commit` against one parent. `parent` must be one
/// of the commit's actual parents; a root commit uses [`GitComparisonBase::EmptyTree`]
/// (resolved here for the repository's object format). Uses raw NUL-separated
/// records so rename pairs and mode bits survive arbitrary path bytes.
pub fn git_commit_files(
    root: &Path,
    commit: &GitObjectId,
    base: &GitComparisonBase,
) -> Result<GitCommitFiles, GitError> {
    validate_commit_base(root, commit, base)?;
    let empty_tree;
    let base_arg = match base {
        GitComparisonBase::Parent(parent) => parent.as_str(),
        GitComparisonBase::EmptyTree => {
            empty_tree = empty_tree_oid(root)?;
            empty_tree.as_str()
        }
    };
    let args = [
        "--no-optional-locks",
        "-c",
        "core.quotepath=false",
        "diff",
        "--raw",
        "-z",
        "-M",
        "-l0",
        "--no-color",
        "--no-ext-diff",
        "--no-textconv",
        base_arg,
        commit.as_str(),
    ];
    let output = run_git(
        root,
        &[("LC_ALL", "C")],
        &args,
        GIT_STATUS_TIMEOUT,
        MAX_GIT_COMMIT_FILES_BYTES,
    )?;
    let (mut files, mut truncated) = parse_raw_changes(&output.stdout);
    if files.len() > MAX_GIT_COMMIT_FILES {
        files.truncate(MAX_GIT_COMMIT_FILES);
        truncated = true;
    }
    truncated |= output.stdout_capped;
    Ok(GitCommitFiles {
        commit: commit.clone(),
        base: base.clone(),
        files,
        truncated,
    })
}

/// Read one commit's patch against a chosen parent/base, reusing the shared
/// bounded unified-diff parser. `path` selects one committed path pair; a
/// rename supplies both old and new paths so Git emits the rename form rather
/// than a synthetic add/delete. Context is clamped to the M15 ceiling.
pub fn git_commit_diff(
    root: &Path,
    commit: &GitObjectId,
    base: &GitComparisonBase,
    old_path: Option<&Path>,
    path: &Path,
    context_lines: u8,
) -> Result<DiffInfo, GitError> {
    validate_commit_base(root, commit, base)?;
    let empty_tree;
    let base_arg = match base {
        GitComparisonBase::Parent(parent) => parent.as_str(),
        GitComparisonBase::EmptyTree => {
            empty_tree = empty_tree_oid(root)?;
            empty_tree.as_str()
        }
    };
    let context = context_lines.min(MAX_DIFF_CONTEXT_LINES);
    let context_arg = format!("-U{context}");
    let mut args = vec![
        "--literal-pathspecs".to_owned(),
        "--no-optional-locks".to_owned(),
        "-c".to_owned(),
        "core.quotepath=false".to_owned(),
        "diff".to_owned(),
        "--no-color".to_owned(),
        "--no-ext-diff".to_owned(),
        "--no-textconv".to_owned(),
        "--src-prefix=a/".to_owned(),
        "--dst-prefix=b/".to_owned(),
        "-M".to_owned(),
        context_arg,
        base_arg.to_owned(),
        commit.as_str().to_owned(),
        "--".to_owned(),
    ];
    if let Some(old) = old_path {
        args.push(old.to_string_lossy().into_owned());
    }
    args.push(path.to_string_lossy().into_owned());
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    let output = run_git(
        root,
        &[("LC_ALL", "C")],
        &arg_refs,
        GIT_STATUS_TIMEOUT,
        MAX_DIFF_BYTES,
    )?;
    let mut info = parse_diff(&output.stdout, output.stdout_capped, false);
    info.staged = false;
    Ok(info)
}

/// Resolve the empty-tree object ID for the repository's object format. Asking
/// Git avoids hardcoding a SHA-1 constant that is wrong in a SHA-256 repo.
fn empty_tree_oid(root: &Path) -> Result<GitObjectId, GitError> {
    let output = run_git(
        root,
        &[("LC_ALL", "C")],
        &["hash-object", "-t", "tree", "--stdin"],
        GIT_STATUS_TIMEOUT,
        1024,
    )?;
    let text = String::from_utf8_lossy(&output.stdout);
    GitObjectId::parse(text.trim())
        .map_err(|_| GitError::GitFailed("git returned an invalid empty-tree object ID".into()))
}

fn validate_commit_base(
    root: &Path,
    commit: &GitObjectId,
    base: &GitComparisonBase,
) -> Result<(), GitError> {
    let parents = git_commit_parents(root, commit)?;
    match base {
        GitComparisonBase::Parent(parent) if parents.contains(parent) => Ok(()),
        GitComparisonBase::EmptyTree if parents.is_empty() => Ok(()),
        GitComparisonBase::Parent(_) => Err(GitError::GitFailed(
            "comparison parent is not a parent of the selected commit".into(),
        )),
        GitComparisonBase::EmptyTree => Err(GitError::GitFailed(
            "empty-tree comparison is valid only for a root commit".into(),
        )),
    }
}

/// Parse `git diff --raw -z` records: `:oldmode newmode oldoid newoid status\0
/// path\0` plus a second `\0`-terminated path for `R`/`C`. A capped read that
/// ends mid-record drops the partial tail rather than fabricating a file.
fn parse_raw_changes(bytes: &[u8]) -> (Vec<GitCommitFile>, bool) {
    let complete = bytes.split(|byte| *byte == 0).collect::<Vec<_>>();
    let mut files = Vec::new();
    let mut index = 0;
    let mut truncated = false;
    while index < complete.len() {
        let header = complete[index];
        if header.is_empty() {
            index += 1;
            continue;
        }
        if !header.starts_with(b":") {
            // A record cut mid-way cannot be trusted.
            truncated = true;
            break;
        }
        let header = String::from_utf8_lossy(header);
        let mut parts = header.split(' ');
        let old_mode = parse_octal_mode(parts.next());
        let new_mode = parse_octal_mode(parts.next());
        let _old_oid = parts.next();
        let _new_oid = parts.next();
        let Some(status) = parts.next() else {
            truncated = true;
            break;
        };
        let Some(first_path) = complete.get(index + 1) else {
            truncated = true;
            break;
        };
        if first_path.is_empty() {
            truncated = true;
            break;
        }
        let first_path = git_path_from_bytes(first_path);
        let status_char = status.chars().next().unwrap_or('M');
        if matches!(status_char, 'R' | 'C') {
            // Raw diff order for R/C is `<source>\0<destination>\0`.
            let Some(destination) = complete.get(index + 2) else {
                truncated = true;
                break;
            };
            if destination.is_empty() {
                truncated = true;
                break;
            }
            files.push(GitCommitFile {
                old_path: Some(first_path),
                path: git_path_from_bytes(destination),
                kind: kind_from_status(status_char, old_mode, new_mode),
                old_mode,
                new_mode,
            });
            index += 3;
        } else {
            files.push(GitCommitFile {
                old_path: None,
                path: first_path,
                kind: kind_from_status(status_char, old_mode, new_mode),
                old_mode,
                new_mode,
            });
            index += 2;
        }
    }
    (files, truncated)
}

fn git_path_from_bytes(bytes: &[u8]) -> PathBuf {
    #[cfg(unix)]
    {
        PathBuf::from(std::ffi::OsString::from_vec(bytes.to_vec()))
    }
    #[cfg(not(unix))]
    {
        PathBuf::from(String::from_utf8_lossy(bytes).into_owned())
    }
}

fn parse_octal_mode(value: Option<&str>) -> Option<u32> {
    u32::from_str_radix(value?.trim(), 8).ok()
}

fn kind_from_status(
    status: char,
    old_mode: Option<u32>,
    new_mode: Option<u32>,
) -> GitCommitFileKind {
    match status {
        'A' => GitCommitFileKind::Added,
        'D' => GitCommitFileKind::Deleted,
        'R' => GitCommitFileKind::Renamed,
        'C' => GitCommitFileKind::Copied,
        'T' => GitCommitFileKind::TypeChanged,
        'M' => {
            // A mode-only edit reports `M` with differing modes and no hunks.
            match (old_mode, new_mode) {
                (Some(old), Some(new)) if old != new => GitCommitFileKind::ModeChanged,
                _ => GitCommitFileKind::Modified,
            }
        }
        _ => GitCommitFileKind::Modified,
    }
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
        refs: parse_decorations(record[7]),
        body: truncate_utf8(record[8], MAX_GIT_COMMIT_BODY_BYTES),
        shallow_boundary: false,
    })
}

/// Parse `%D` decoration text (`HEAD -> main, origin/main, tag: v1`) into
/// `GitRef` entries. `HEAD -> x` marks the ref HEAD points at; bare `HEAD`
/// (detached) becomes a head-kind entry. Unknown shapes drop (never fabricate).
fn parse_decorations(raw: &[u8]) -> Vec<GitRef> {
    let text = String::from_utf8_lossy(raw);
    let mut refs = Vec::new();
    for part in text.split(", ") {
        let part = part.trim();
        if part.is_empty() {
            continue;
        }
        if let Some(target) = part.strip_prefix("HEAD -> ") {
            refs.push(GitRef {
                name: target.to_owned(),
                kind: GitRefKind::Head,
            });
        } else if part == "HEAD" {
            refs.push(GitRef {
                name: "HEAD".to_owned(),
                kind: GitRefKind::Head,
            });
        } else if let Some(tag) = part.strip_prefix("tag: ") {
            refs.push(GitRef {
                name: tag.to_owned(),
                kind: GitRefKind::Tag,
            });
        } else if part.contains('/') {
            refs.push(GitRef {
                name: part.to_owned(),
                kind: GitRefKind::RemoteTracking,
            });
        } else {
            refs.push(GitRef {
                name: part.to_owned(),
                kind: GitRefKind::LocalBranch,
            });
        }
    }
    refs
}

fn parse_iso_offset(value: &str) -> Option<i16> {
    // Strict ISO 8601 (`%aI`) uses `Z` for UTC instead of `+00:00` — this is
    // what CI runners in UTC emit, while local zones emit `+HH:MM`. Without
    // this arm every record in a UTC repository fails and history reads
    // empty.
    if value.len() > 10 && value.ends_with(['Z', 'z']) {
        return Some(0);
    }
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
            "",
            "",
        ]
        .join("\0")
        .into_bytes()
        .into_iter()
        .chain(std::iter::once(0))
        .collect()
    }

    fn record_full(
        id: &str,
        parents: &str,
        subject: &str,
        decorations: &str,
        body: &str,
    ) -> Vec<u8> {
        [
            id,
            parents,
            "Ada",
            "ada@example.test",
            "10",
            "1970-01-01T00:00:10+05:30",
            subject,
            decorations,
            body,
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
    fn iso_offset_accepts_zulu_utc_and_signed_hours() {
        // `%aI` emits a literal `Z` in UTC zones (CI runners) and `+HH:MM`
        // elsewhere; both must parse or whole history pages drop to empty.
        assert_eq!(parse_iso_offset("2026-10-08T04:38:12Z"), Some(0));
        assert_eq!(parse_iso_offset("2026-10-08T04:38:12z"), Some(0));
        assert_eq!(parse_iso_offset("2026-10-08T11:38:12+07:00"), Some(420));
        assert_eq!(parse_iso_offset("1970-01-01T00:00:10+05:30"), Some(330));
        assert_eq!(parse_iso_offset("1970-01-01T00:00:10-04:00"), Some(-240));
        assert_eq!(parse_iso_offset("not-a-date"), None);
        assert_eq!(parse_iso_offset("2026-10-08T04:38:12+25:00"), None);
    }

    #[test]
    fn zulu_dated_records_parse_into_commits() {
        // Exact CI shape: UTC `%aI` with `Z`, one NUL-framed record.
        let bytes = [
            SHA1_A,
            "",
            "Ada",
            "ada@example.test",
            "10",
            "2026-10-08T04:38:12Z",
            "second",
            "HEAD -> master",
            "second\n",
        ]
        .join("\0")
        .into_bytes()
        .into_iter()
        .chain(std::iter::once(0))
        .collect::<Vec<_>>();
        let page = parse_history_log(&bytes, false, 10);
        assert!(!page.truncated);
        assert_eq!(page.commits.len(), 1);
        assert_eq!(page.commits[0].subject, "second");
        assert_eq!(page.commits[0].author_time.offset_minutes, 0);
    }

    #[test]
    fn decorations_and_body_parse_into_badges_and_text() {
        let mut bytes = record_full(
            SHA1_A,
            SHA1_B,
            "first",
            "HEAD -> main, origin/main, tag: v1",
            "multi\nline body",
        );
        bytes.extend(record(SHA1_B, "", "second"));
        let page = parse_history_log(&bytes, false, 10);
        assert!(!page.truncated);
        assert_eq!(page.commits.len(), 2);
        let kinds: Vec<GitRefKind> = page.commits[0].refs.iter().map(|r| r.kind).collect();
        assert!(kinds.contains(&GitRefKind::Head));
        assert!(kinds.contains(&GitRefKind::RemoteTracking));
        assert!(kinds.contains(&GitRefKind::Tag));
        assert_eq!(page.commits[0].body, "multi\nline body");
        assert!(page.commits[1].refs.is_empty());
        assert_eq!(page.commits[1].body, "");
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
        bytes.extend(b"1970-01-01T00:00:00+00:00\0x\0\0\0");
        let page = parse_history_log(&bytes, false, 10);

        assert!(page.truncated);
        assert!(page.commits.is_empty());
    }

    #[cfg(unix)]
    #[test]
    fn raw_change_paths_preserve_non_utf8_bytes() {
        use std::os::unix::ffi::OsStrExt;

        let bytes = b":100644 100644 aaaa bbbb M\0bad\xff.txt\0";
        let (files, truncated) = parse_raw_changes(bytes);

        assert!(!truncated);
        assert_eq!(files.len(), 1);
        assert_eq!(files[0].path.as_os_str().as_bytes(), b"bad\xff.txt");
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

    #[test]
    fn commit_files_classify_modify_add_delete_and_rename() {
        let repo = TestRepo::new();
        repo.write("sub/old.txt", "renamed body\n");
        repo.write("keep.txt", "keep\n");
        repo.commit("first", "sub/old.txt", "renamed body\n");
        repo.write("keep.txt", "keep modified\n");
        repo.write("added.txt", "new\n");
        repo.rename("sub/old.txt", "sub/new.txt");
        repo.git(&["add", "-A"]);
        repo.git(&["commit", "--quiet", "-m", "second"]);

        let head = repo.head_oid();
        let parents = git_commit_parents(repo.path(), &head).unwrap();
        assert_eq!(parents.len(), 1);
        let files = git_commit_files(
            repo.path(),
            &head,
            &GitComparisonBase::Parent(parents[0].clone()),
        )
        .unwrap();
        assert!(!files.truncated);
        let rename = files
            .files
            .iter()
            .find(|file| file.path == Path::new("sub/new.txt"))
            .unwrap_or_else(|| panic!("rename present: {:?}", files.files));
        assert_eq!(rename.kind, GitCommitFileKind::Renamed);
        assert_eq!(rename.old_path.as_deref(), Some(Path::new("sub/old.txt")));
        assert!(files.files.iter().any(
            |file| file.path == Path::new("added.txt") && file.kind == GitCommitFileKind::Added
        ));
    }

    #[test]
    fn root_commit_uses_resolved_empty_tree_and_rejects_nonparents() {
        let repo = TestRepo::new();
        repo.commit("root", "a.txt", "a\n");
        repo.commit("child", "a.txt", "b\n");

        let root = repo.head_oid();
        let parents = git_commit_parents(repo.path(), &root).unwrap();
        let root_parents = git_commit_parents(repo.path(), &parents[0]).unwrap();
        assert!(root_parents.is_empty());

        let files =
            git_commit_files(repo.path(), &parents[0], &GitComparisonBase::EmptyTree).unwrap();
        assert_eq!(files.files.len(), 1);
        assert_eq!(files.files[0].kind, GitCommitFileKind::Added);
        assert!(matches!(
            git_commit_files(repo.path(), &root, &GitComparisonBase::EmptyTree),
            Err(GitError::GitFailed(message)) if message.contains("root commit")
        ));
    }

    #[test]
    fn commit_diff_reads_committed_content_not_the_worktree() {
        let repo = TestRepo::new();
        repo.commit("first", "a.txt", "committed one\n");
        repo.commit("second", "a.txt", "committed two\n");
        // Dirty the worktree; a committed diff must ignore it entirely.
        repo.write("a.txt", "uncommitted scratch\n");

        let head = repo.head_oid();
        let parents = git_commit_parents(repo.path(), &head).unwrap();
        let info = git_commit_diff(
            repo.path(),
            &head,
            &GitComparisonBase::Parent(parents[0].clone()),
            None,
            Path::new("a.txt"),
            3,
        )
        .unwrap();
        assert_eq!(info.files.len(), 1);
        let body = info.files[0]
            .hunks
            .iter()
            .flat_map(|hunk| hunk.lines.iter())
            .map(|line| line.text.as_str())
            .collect::<Vec<_>>();
        assert!(body.iter().any(|text| text.contains("committed two")));
        assert!(!body.iter().any(|text| text.contains("scratch")));
    }

    #[test]
    fn commit_diff_keeps_rename_form_when_both_paths_are_supplied() {
        let repo = TestRepo::new();
        repo.commit("first", "old.txt", "body\n");
        repo.rename("old.txt", "new.txt");
        repo.git(&["add", "-A"]);
        repo.git(&["commit", "--quiet", "-m", "rename"]);

        let head = repo.head_oid();
        let parents = git_commit_parents(repo.path(), &head).unwrap();
        let info = git_commit_diff(
            repo.path(),
            &head,
            &GitComparisonBase::Parent(parents[0].clone()),
            Some(Path::new("old.txt")),
            Path::new("new.txt"),
            3,
        )
        .unwrap();
        assert_eq!(info.files.len(), 1);
        assert_eq!(
            info.files[0].old_path.as_deref(),
            Some(Path::new("old.txt"))
        );
        assert_eq!(info.files[0].status, omaterm_core::DiffFileStatus::Renamed);
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

        fn write(&self, path: &str, contents: &str) {
            let full = self.path.join(path);
            if let Some(parent) = full.parent() {
                std::fs::create_dir_all(parent).unwrap();
            }
            std::fs::write(full, contents).unwrap();
        }

        fn rename(&self, from: &str, to: &str) {
            self.git(&["mv", "--", from, to]);
        }

        fn head_oid(&self) -> GitObjectId {
            let output = std::process::Command::new("git")
                .args(["rev-parse", "HEAD"])
                .current_dir(&self.path)
                .output()
                .unwrap();
            assert!(output.status.success());
            GitObjectId::parse(String::from_utf8_lossy(&output.stdout).trim()).unwrap()
        }

        fn commit(&self, message: &str, path: &str, contents: &str) {
            self.write(path, contents);
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
