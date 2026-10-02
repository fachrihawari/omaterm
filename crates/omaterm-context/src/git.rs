//! Git status + stage/unstage/discard over the system git binary (M14,
//! blueprint §32).
//!
//! The embedded-libgit2 route is deliberately NOT taken: spawning the
//! user's installed `git` keeps `.gitconfig`, credential helpers, SSH,
//! GPG, hooks, and worktree configuration working. Every entry point here
//! takes an explicit project root (resolved by M12) and passes argument
//! vectors only — never shell interpolation.
//!
//! Env hygiene (blueprint §32): `GIT_TERMINAL_PROMPT=0` so git can never
//! block on an interactive prompt, `--no-optional-locks` on read-only
//! calls, `--no-pager` everywhere, stdin closed. All I/O runs on the
//! caller's thread (the desktop moves it to background workers with a
//! generation guard); every subprocess wait is bounded with kill+reap, so
//! a wedged git costs at most the timeout.
//!
//! No `gpui` dependency. Paths in [`omaterm_core::GitStatusInfo`] are
//! relative to the project root: git runs with the root as its working
//! directory and `-c status.relativePaths=true`, so porcelain paths land
//! root-relative even when user config says otherwise.

use std::io::{Read, Write};
use std::path::{Component, Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::Duration;

use omaterm_core::{GitEntry, GitStatusInfo};

use crate::ContextError;

/// Bounded `git status` wait. Status output is small (bounded below) and
/// local; a wedged git is killed and reaped at this deadline.
pub const GIT_STATUS_TIMEOUT: Duration = Duration::from_secs(10);
/// Bounded mutation wait (`add`/`reset`/`restore` + hooks, which may run
/// user code).
pub const GIT_MUTATION_TIMEOUT: Duration = Duration::from_secs(30);
/// Largest `git status` stdout kept in memory. Large repos report many
/// entries; beyond this the parse still succeeds over the kept prefix
/// (cut on a NUL boundary) and the envelope is marked truncated — never
/// an unbounded memory interface (blueprint §64).
pub const MAX_STATUS_BYTES: usize = 4 * 1024 * 1024;
/// Largest git stderr kept for a `git_failed` message. Agents key off the
/// stable code, never the text (blueprint §44).
pub const MAX_STDERR_BYTES: usize = 4 * 1024;

/// Git failures. `GitFailed` carries bounded stderr; `NotARepo` is the
/// explicit non-repository state (status renders the empty envelope
/// instead); `GitUnavailable` means the binary itself is missing.
#[derive(Debug, thiserror::Error)]
pub enum GitError {
    #[error("project root is not inside a git repository")]
    NotARepo,
    #[error("git failed: {0}")]
    GitFailed(String),
    #[error("system git is unavailable: {0}")]
    GitUnavailable(String),
    #[error("path escapes the project root")]
    PathOutsideRoot,
    #[error("git operation timed out")]
    Timeout,
    #[error(transparent)]
    Io(#[from] std::io::Error),
}

impl GitError {
    /// Stable machine code for the wire (`not_a_repo`, `git_failed`,
    /// `git_unavailable`, `path_outside_root`, `timeout`).
    pub const fn code(&self) -> &'static str {
        match self {
            Self::NotARepo => "not_a_repo",
            Self::GitFailed(_) => "git_failed",
            Self::GitUnavailable(_) => "git_unavailable",
            Self::PathOutsideRoot => "path_outside_root",
            Self::Timeout => "timeout",
            Self::Io(_) => "io_error",
        }
    }
}

impl From<ContextError> for GitError {
    fn from(error: ContextError) -> Self {
        match error {
            ContextError::PathOutsideRoot => Self::PathOutsideRoot,
            ContextError::Io(error) => Self::Io(error),
        }
    }
}

/// Join `user_path` onto the canonical root without requiring the target
/// to exist (discard must accept deleted paths, which
/// [`crate::canonicalize_under_root`] cannot resolve). Lexical `..`
/// components that climb above the root are rejected; when the joined
/// path exists it is additionally canonicalized so a symlink inside the
/// root pointing outside is rejected exactly like `..` traversal.
pub fn join_under_root(root: &Path, user_path: &Path) -> Result<PathBuf, GitError> {
    let canonical_root = std::fs::canonicalize(root)?;
    let joined = if user_path.is_absolute() {
        normalize_lexically(user_path)
    } else {
        normalize_lexically(&canonical_root.join(user_path))
    };
    if joined != canonical_root && !joined.starts_with(&canonical_root) {
        return Err(GitError::PathOutsideRoot);
    }
    if joined.exists()
        && let Ok(canonical) = std::fs::canonicalize(&joined)
        && (!canonical.starts_with(&canonical_root))
    {
        return Err(GitError::PathOutsideRoot);
    }
    Ok(joined)
}

/// Resolve `.`/`..`/duplicate separators lexically, without touching the
/// filesystem. A `..` that climbs above the filesystem root collapses
/// there (the `starts_with` check against the canonical root still
/// rejects escapes).
fn normalize_lexically(path: &Path) -> PathBuf {
    let mut out = PathBuf::new();
    for component in path.components() {
        match component {
            Component::CurDir => {}
            Component::ParentDir => {
                if !out.pop() {
                    out.push("..");
                }
            }
            other => out.push(other.as_os_str()),
        }
    }
    if out.as_os_str().is_empty() {
        PathBuf::from("/")
    } else {
        out
    }
}

/// Parsed `git status --porcelain=v2 -z --branch` output before the entry
/// cap is applied.
#[derive(Debug, Default, PartialEq, Eq)]
struct ParsedStatus {
    branch: Option<String>,
    upstream: Option<String>,
    ahead: usize,
    behind: usize,
    staged: Vec<GitEntry>,
    unstaged: Vec<GitEntry>,
    untracked: Vec<GitEntry>,
}

/// Parse NUL-separated porcelain v2 records. Unknown line kinds (`!`
/// ignored paths, `# stash.*`, future kinds) are skipped, never fatal;
/// malformed entry lines are skipped so one odd record cannot sink a whole
/// status. Rename (`2`) records consume the following NUL field as the
/// pre-rename path (`-z` order is new-then-old, verified against git
/// 2.55.0).
fn parse_porcelain_v2(output: &[u8]) -> ParsedStatus {
    let mut parsed = ParsedStatus::default();
    // `split` drops the trailing empty after a final NUL; a record cut
    // mid-entry by the stdout cap has no NUL terminator and arrives here
    // as the last field — entry parsers below only accept well-formed
    // records, so the torn tail is dropped, never half-parsed.
    let mut fields = output.split(|byte| *byte == 0).peekable();
    while let Some(field) = fields.next() {
        if field.is_empty() {
            continue;
        }
        let first = field[0];
        if first == b'#' {
            parse_header_field(&mut parsed, field);
        } else if first == b'?' {
            if let Some(entry) = parse_simple_entry(field) {
                parsed.untracked.push(entry);
            }
        } else if first == b'1' || first == b'u' {
            if let Some(entry) = parse_index_entry(field) {
                push_index_entry(&mut parsed, entry);
            }
        } else if first == b'2' {
            let next = fields.peek().copied().unwrap_or_default();
            if let Some(entry) = parse_rename_entry(field, next) {
                // The orig-path field belongs to this record: consume it
                // so it is never parsed as a path of its own.
                let _ = fields.next();
                push_index_entry(&mut parsed, entry);
            }
        }
        // `!` (ignored) and anything else: skipped by design.
    }
    parsed
}

fn parse_header_field(parsed: &mut ParsedStatus, field: &[u8]) {
    let text = String::from_utf8_lossy(field);
    let mut parts = text.splitn(3, ' ');
    let (Some(hash), Some(key)) = (parts.next(), parts.next()) else {
        return;
    };
    if hash != "#" {
        return;
    }
    let value = parts.next().unwrap_or_default();
    match key {
        "branch.head" if !value.starts_with('(') && !value.is_empty() => {
            parsed.branch = Some(value.to_owned());
        }
        "branch.upstream" if !value.is_empty() => {
            parsed.upstream = Some(value.to_owned());
        }
        "branch.ab" => {
            let mut ahead = 0;
            let mut behind = 0;
            for token in value.split_whitespace() {
                if let Some(rest) = token.strip_prefix('+') {
                    ahead = rest.parse().unwrap_or(0);
                } else if let Some(rest) = token.strip_prefix('-') {
                    behind = rest.parse().unwrap_or(0);
                }
            }
            parsed.ahead = ahead;
            parsed.behind = behind;
        }
        _ => {}
    }
}

/// `? <path>` records (untracked files and directories; directories keep
/// their trailing `/`).
fn parse_simple_entry(field: &[u8]) -> Option<GitEntry> {
    let text = String::from_utf8_lossy(field);
    let path = text.strip_prefix("? ")?;
    if path.is_empty() {
        return None;
    }
    Some(GitEntry {
        path: PathBuf::from(path),
        renamed_from: None,
        x: '?',
        y: '?',
    })
}

/// `1 XY ... <path>` and `u XY ... <path>` records: the path is the
/// remainder after the fixed field prefix, so spaces inside paths survive.
/// `1` lines carry 8 tokens before the path (`1 XY subm mH mI mW hH
/// hI`); `u` lines carry 10 (`u XY subm m1 m2 m3 mW h1 h2 h3`).
fn parse_index_entry(field: &[u8]) -> Option<(GitEntry, bool)> {
    let text = String::from_utf8_lossy(field);
    let kind = text.as_bytes().first()?;
    let skips = match kind {
        b'1' => 8,
        b'u' => 10,
        _ => return None,
    };
    let mut tokens = text.splitn(3, ' ');
    tokens.next()?;
    let xy: Vec<char> = tokens.next().unwrap_or_default().chars().collect();
    let (Some(&x), Some(&y)) = (xy.first(), xy.get(1)) else {
        return None;
    };
    // Skip the remaining fixed fields; the path is everything after
    // them. With `-z` records are NUL-terminated, so no newline trimming:
    // a filename may itself end in `\n`.
    let mut rest: &str = &text;
    for _ in 0..skips {
        rest = rest.split_once(' ')?.1;
    }
    if rest.is_empty() {
        return None;
    }
    Some((
        GitEntry {
            path: PathBuf::from(rest),
            renamed_from: None,
            x,
            y,
        },
        *kind == b'u',
    ))
}

/// Similarity score token shape (`R100`, `C75`): a rename/copy marker
/// plus digits. Only a real score is stripped; anything else is kept as
/// part of the path.
fn is_similarity_score(token: &str) -> bool {
    token.len() > 1
        && (token.starts_with('R') || token.starts_with('C'))
        && token[1..].chars().all(|char| char.is_ascii_digit())
}

/// `2 XY ... <X><score> <new-path>` records plus the following NUL field
/// (`<old-path>`). Nine fixed tokens precede the score (`2 XY subm mH
/// mI mW hH hI`); the score token is stripped, leaving the new path.
fn parse_rename_entry(head: &[u8], orig_field: &[u8]) -> Option<(GitEntry, bool)> {
    let text = String::from_utf8_lossy(head);
    if text.as_bytes().first() != Some(&b'2') {
        return None;
    }
    let mut tokens = text.splitn(3, ' ');
    tokens.next()?;
    let xy: Vec<char> = tokens.next().unwrap_or_default().chars().collect();
    let (Some(&x), Some(&y)) = (xy.first(), xy.get(1)) else {
        return None;
    };
    let mut rest: &str = &text;
    for _ in 0..9 {
        rest = rest.split_once(' ')?.1;
    }
    // Remainder is `<X><score> <new-path>` (e.g. `R100 my file.txt`):
    // strip the score token only when it has the score shape, so spaces
    // inside the path survive.
    let new_path = match rest.split_once(' ') {
        Some((score, path)) if is_similarity_score(score) => path,
        _ => rest,
    };
    if new_path.is_empty() || orig_field.is_empty() {
        return None;
    }
    let orig = String::from_utf8_lossy(orig_field);
    if orig.is_empty() {
        return None;
    }
    Some((
        GitEntry {
            path: PathBuf::from(new_path),
            renamed_from: Some(PathBuf::from(orig.into_owned())),
            x,
            y,
        },
        false,
    ))
}

/// Route an index/worktree entry to its groups. Unmerged (`u`) entries
/// appear in both groups: both sides need resolution.
fn push_index_entry(parsed: &mut ParsedStatus, entry: (GitEntry, bool)) {
    let (entry, unmerged) = entry;
    if unmerged || entry.is_staged() {
        parsed.staged.push(entry.clone());
    }
    if unmerged || entry.is_unstaged() {
        parsed.unstaged.push(entry);
    }
}

/// Apply the entry cap in group fill order (staged, then unstaged, then
/// untracked) with an accurate `truncated` flag.
fn truncate_parsed(mut parsed: ParsedStatus, limit: usize) -> GitStatusInfo {
    let limit = limit.max(1);
    let mut remaining = limit;
    let mut truncated = false;
    for group in [
        &mut parsed.staged,
        &mut parsed.unstaged,
        &mut parsed.untracked,
    ] {
        if group.len() > remaining {
            group.truncate(remaining);
            truncated = true;
            remaining = 0;
        } else {
            remaining -= group.len();
        }
    }
    GitStatusInfo {
        branch: parsed.branch,
        upstream: parsed.upstream,
        ahead: parsed.ahead,
        behind: parsed.behind,
        staged: parsed.staged,
        unstaged: parsed.unstaged,
        untracked: parsed.untracked,
        truncated,
    }
}

pub(crate) struct GitOutput {
    pub(crate) stdout: Vec<u8>,
    pub(crate) stdout_capped: bool,
}

impl std::fmt::Debug for GitOutput {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("GitOutput")
            .field("stdout_len", &self.stdout.len())
            .field("stdout_capped", &self.stdout_capped)
            .finish()
    }
}

/// Spawn the system git binary with the M14 env hygiene (blueprint §32):
/// prompts disabled, no pager, stdin closed, bounded wait with kill+reap.
/// Stdout is capped at `stdout_cap` bytes (cut on read-chunk granularity;
/// NUL-safe parsing drops any torn tail); stderr is capped at
/// [`MAX_STDERR_BYTES`].
/// Crate-internal raw spawn for sibling modules (diff) that share the M14
/// env hygiene and bounded-wait contract with their own argv.
pub(crate) fn run_git(
    root: &Path,
    extra_env: &[(&str, &str)],
    args: &[&str],
    timeout: Duration,
    stdout_cap: usize,
) -> Result<GitOutput, GitError> {
    run_git_with("git", root, extra_env, args, None, timeout, stdout_cap)
}

/// Bounded Git mutation with internally generated stdin. Callers pass only
/// trusted context-owned bytes; IPC/UI never expose a generic patch channel.
pub(crate) fn run_git_input(
    root: &Path,
    extra_env: &[(&str, &str)],
    args: &[&str],
    input: &[u8],
    timeout: Duration,
    stdout_cap: usize,
) -> Result<GitOutput, GitError> {
    run_git_with(
        "git",
        root,
        extra_env,
        args,
        Some(input),
        timeout,
        stdout_cap,
    )
}

/// Same spawn with an explicit binary path. Production always passes
/// `"git"` (`PATH` lookup, blueprint §32); tests point it at a missing
/// executable to prove the `git_unavailable` mapping without touching the
/// process environment.
fn run_git_with(
    git_bin: &str,
    root: &Path,
    extra_env: &[(&str, &str)],
    args: &[&str],
    input: Option<&[u8]>,
    timeout: Duration,
    stdout_cap: usize,
) -> Result<GitOutput, GitError> {
    let mut command = Command::new(git_bin);
    command
        .arg("--no-pager")
        .args(args)
        .current_dir(root)
        .env("GIT_TERMINAL_PROMPT", "0")
        .stdin(if input.is_some() {
            Stdio::piped()
        } else {
            Stdio::null()
        })
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    for (key, value) in extra_env {
        command.env(key, value);
    }
    let mut child = command
        .spawn()
        .map_err(|error| GitError::GitUnavailable(format!("cannot spawn git: {error}")))?;
    if let Some(input) = input
        && let Some(mut stdin) = child.stdin.take()
    {
        stdin.write_all(input)?;
    }
    // The reader thread owns only the pipes; the caller keeps the child
    // handle so every path below reaps it (no orphans, no leaked waiter
    // threads), mirroring `resolve.rs`. Output is capped before
    // allocation: status output is bounded by `stdout_cap`, stderr by
    // `MAX_STDERR_BYTES`.
    let stdout_pipe = child.stdout.take();
    let stderr_pipe = child.stderr.take();
    let (done_tx, done_rx) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        let mut stdout = Vec::new();
        if let Some(pipe) = stdout_pipe {
            let _ = pipe.take(stdout_cap as u64).read_to_end(&mut stdout);
        }
        let mut stderr = Vec::new();
        if let Some(pipe) = stderr_pipe {
            let _ = pipe.take(MAX_STDERR_BYTES as u64).read_to_end(&mut stderr);
        }
        let _ = done_tx.send((stdout, stderr));
    });
    let (stdout, stderr) = done_rx.recv_timeout(timeout).map_err(|_| {
        // Wedged git: terminate and reap before reporting, so the timeout
        // bounds the whole call including cleanup.
        let _ = child.kill();
        let _ = child.wait();
        GitError::Timeout
    })?;
    // Output arrived: reap promptly. `kill` is a no-op when git already
    // exited; `wait` then collects the zombie without blocking.
    let _ = child.kill();
    let status = child.wait()?;
    if !status.success() {
        let text = String::from_utf8_lossy(&stderr);
        let trimmed = text.trim();
        // Case-insensitive: `status` reports "fatal: not a git
        // repository" while `diff` outside a work tree warns "Not a git
        // repository. Use --no-index ...". Both mean the same explicit
        // non-repository state (never an error for read-only views).
        if trimmed.to_lowercase().contains("not a git repository") {
            return Err(GitError::NotARepo);
        }
        return Err(GitError::GitFailed(if trimmed.is_empty() {
            format!("git failed with {status}")
        } else {
            trimmed.to_owned()
        }));
    }
    let capped = stdout.len() >= stdout_cap;
    Ok(GitOutput {
        stdout,
        stdout_capped: capped,
    })
}

/// Read-only status over `root` (M14 product contract). Runs
/// `git status --porcelain=v2 -z --branch` with the root as its working
/// directory, parses branch/ahead-behind/groups, and caps entries with an
/// accurate `truncated` flag. A non-repository root returns
/// [`GitError::NotARepo`] (the router renders the empty envelope).
pub fn git_status(root: &Path, limit: usize) -> Result<GitStatusInfo, GitError> {
    let output = run_git(
        root,
        &[],
        &[
            "--no-optional-locks",
            "-c",
            "status.relativePaths=true",
            "status",
            "--porcelain=v2",
            "-z",
            "--branch",
            "--untracked-files=normal",
            "--",
            ".",
        ],
        GIT_STATUS_TIMEOUT,
        MAX_STATUS_BYTES,
    )?;
    let mut info = truncate_parsed(parse_porcelain_v2(&output.stdout), limit);
    info.truncated = info.truncated || output.stdout_capped;
    Ok(info)
}

/// Stage explicit root-relative paths (`git add -- <paths>`). Paths are
/// boundary-checked (lexical + symlink-aware for existing targets);
/// traversal is rejected before git ever runs.
pub fn git_stage(root: &Path, paths: &[PathBuf]) -> Result<(), GitError> {
    let resolved = resolve_paths(root, paths)?;
    if resolved.is_empty() {
        return Ok(());
    }
    let args = mutation_args("add", &resolved);
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    run_git(root, &[], &arg_refs, GIT_MUTATION_TIMEOUT, 64 * 1024)?;
    Ok(())
}

/// Commit staged changes with an explicit message (`git commit -m`).
/// Authorship comes from the repository's git config — no author UI.
/// Returns the short HEAD oid of the new commit. An empty message is
/// rejected by validation before this runs; a nothing-staged race or a
/// hook/GPG failure surfaces as `GitFailed` with bounded stderr.
pub fn git_commit(root: &Path, message: &str) -> Result<String, GitError> {
    debug_assert!(!message.is_empty());
    let args = ["commit".to_owned(), "-m".to_owned(), message.to_owned()];
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    run_git(root, &[], &arg_refs, GIT_MUTATION_TIMEOUT, 64 * 1024)?;
    let output = run_git(
        root,
        &[],
        &["rev-parse", "--short", "HEAD"],
        GIT_STATUS_TIMEOUT,
        1024,
    )?;
    Ok(String::from_utf8_lossy(&output.stdout).trim().to_owned())
}

/// Unstage explicit root-relative paths (`git reset -q HEAD -- <paths>`:
/// mixed reset of the pathspec — index restored, worktree untouched).
pub fn git_unstage(root: &Path, paths: &[PathBuf]) -> Result<(), GitError> {
    let resolved = resolve_paths(root, paths)?;
    if resolved.is_empty() {
        return Ok(());
    }
    let mut args = vec![
        "reset".to_owned(),
        "-q".to_owned(),
        "HEAD".to_owned(),
        "--".to_owned(),
    ];
    args.extend(resolved);
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    run_git(root, &[], &arg_refs, GIT_MUTATION_TIMEOUT, 64 * 1024)?;
    Ok(())
}

/// Discard explicit root-relative paths: tracked paths are restored from
/// HEAD (index and worktree, i.e. `checkout HEAD --` semantics via
/// `git restore`); untracked paths are deleted from disk (files, or whole
/// directories). Missing paths are already discarded — success, not an
/// error. The desktop arms this two-step before dispatching.
pub fn git_discard(root: &Path, paths: &[PathBuf]) -> Result<(), GitError> {
    let resolved = resolve_paths(root, paths)?;
    if resolved.is_empty() {
        return Ok(());
    }
    let tracked = tracked_subset(root, &resolved)?;
    if !tracked.is_empty() {
        let mut args = vec![
            "restore".to_owned(),
            "--source=HEAD".to_owned(),
            "--staged".to_owned(),
            "--worktree".to_owned(),
            "--".to_owned(),
        ];
        args.extend(tracked.iter().cloned());
        let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
        run_git(root, &[], &arg_refs, GIT_MUTATION_TIMEOUT, 64 * 1024)?;
    }
    let canonical_root = std::fs::canonicalize(root)?;
    for path in &resolved {
        if tracked_contains(&tracked, path) {
            continue;
        }
        let absolute = canonical_root.join(path);
        // Untracked: delete from disk. Missing is already discarded.
        if let Ok(kind) = std::fs::symlink_metadata(&absolute).map(|meta| meta.file_type()) {
            if kind.is_dir() && !kind.is_symlink() {
                std::fs::remove_dir_all(&absolute)?;
            } else {
                std::fs::remove_file(&absolute)?;
            }
        }
    }
    Ok(())
}

/// Boundary-check every path and render root-relative strings for argv.
/// Deleted (missing) paths stay relative — git resolves them against its
/// working directory (the root).
fn resolve_paths(root: &Path, paths: &[PathBuf]) -> Result<Vec<String>, GitError> {
    let canonical_root = std::fs::canonicalize(root).map_err(GitError::Io)?;
    let mut resolved = Vec::with_capacity(paths.len());
    for path in paths {
        let joined = join_under_root(root, path)?;
        let relative = joined
            .strip_prefix(&canonical_root)
            .map_err(|_| GitError::PathOutsideRoot)?;
        if relative.as_os_str().is_empty() {
            return Err(GitError::PathOutsideRoot);
        }
        resolved.push(relative.to_string_lossy().into_owned());
    }
    Ok(resolved)
}

fn mutation_args(subcommand: &str, resolved: &[String]) -> Vec<String> {
    let mut args = vec![subcommand.to_owned(), "--".to_owned()];
    args.extend(resolved.iter().cloned());
    args
}

/// Partition resolved root-relative paths into the tracked subset via one
/// `git ls-files -z` call (raw NUL-separated bytes: no quoting games).
fn tracked_subset(root: &Path, resolved: &[String]) -> Result<Vec<String>, GitError> {
    let mut ordered = vec!["ls-files".to_owned(), "-z".to_owned(), "--".to_owned()];
    ordered.extend(resolved.iter().cloned());
    let arg_refs: Vec<&str> = ordered.iter().map(String::as_str).collect();
    let output = run_git(root, &[], &arg_refs, GIT_MUTATION_TIMEOUT, 1024 * 1024)?;
    Ok(output
        .stdout
        .split(|byte| *byte == 0)
        .filter(|field| !field.is_empty())
        .map(|field| String::from_utf8_lossy(field).into_owned())
        .collect())
}

fn tracked_contains(tracked: &[String], path: &str) -> bool {
    tracked.iter().any(|known| known == path)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry(path: &str, x: char, y: char) -> GitEntry {
        GitEntry {
            path: PathBuf::from(path),
            renamed_from: None,
            x,
            y,
        }
    }

    #[test]
    fn parses_branch_headers_and_ahead_behind() {
        let mut raw = Vec::new();
        for line in [
            "# branch.oid abc123",
            "# branch.head main",
            "# branch.upstream origin/main",
            "# branch.ab +2 -1",
        ] {
            raw.extend_from_slice(line.as_bytes());
            raw.push(0);
        }
        let parsed = parse_porcelain_v2(&raw);
        assert_eq!(parsed.branch.as_deref(), Some("main"));
        assert_eq!(parsed.upstream.as_deref(), Some("origin/main"));
        assert_eq!((parsed.ahead, parsed.behind), (2, 1));
    }

    #[test]
    fn detached_head_and_missing_upstream_parse_to_none() {
        let raw = b"# branch.head (detached)\0";
        let parsed = parse_porcelain_v2(raw);
        assert_eq!(parsed.branch, None);
        assert_eq!(parsed.upstream, None);
        assert_eq!((parsed.ahead, parsed.behind), (0, 0));
    }

    #[test]
    fn groups_staged_unstaged_and_untracked() {
        let records: &[&[u8]] = &[
            b"1 M. N... 100644 100644 100644 a a staged.rs",
            b"1 .M N... 100644 100644 100644 b b unstaged.rs",
            b"1 MM N... 100644 100644 100644 c c both.rs",
            b"? new dir/",
            b"? plain.txt",
            b"! ignored.o",
        ];
        let mut raw = Vec::new();
        for record in records {
            raw.extend_from_slice(record);
            raw.push(0);
        }
        let parsed = parse_porcelain_v2(&raw);
        let staged: Vec<&str> = parsed
            .staged
            .iter()
            .map(|entry| entry.path.to_str().unwrap())
            .collect();
        let unstaged: Vec<&str> = parsed
            .unstaged
            .iter()
            .map(|entry| entry.path.to_str().unwrap())
            .collect();
        assert_eq!(staged, vec!["staged.rs", "both.rs"]);
        assert_eq!(unstaged, vec!["unstaged.rs", "both.rs"]);
        assert_eq!(parsed.untracked.len(), 2);
        assert!(
            parsed
                .untracked
                .iter()
                .any(|e| e.path.as_path() == Path::new("new dir/"))
        );
        // `!` ignored records are skipped, never fatal.
        assert_eq!(parsed.staged.len() + parsed.unstaged.len(), 4);
        let _ = entry("x", 'M', '.');
    }

    #[test]
    fn rename_consumes_the_orig_field_and_keeps_unicode_spaces() {
        let head = "2 R. N... 100644 100644 100644 a b R100 my file ü.txt";
        let mut raw = Vec::new();
        raw.extend_from_slice(head.as_bytes());
        raw.push(0);
        raw.extend_from_slice("old name.txt".as_bytes());
        raw.push(0);
        raw.extend_from_slice(b"? after.txt");
        raw.push(0);
        let parsed = parse_porcelain_v2(&raw);
        assert_eq!(parsed.staged.len(), 1);
        let renamed = &parsed.staged[0];
        assert_eq!(renamed.path, PathBuf::from("my file ü.txt"));
        assert_eq!(renamed.renamed_from, Some(PathBuf::from("old name.txt")));
        assert!((renamed.x, renamed.y) == ('R', '.'));
        // The orig field was consumed, not parsed as its own path.
        assert_eq!(parsed.untracked.len(), 1);
        assert_eq!(parsed.untracked[0].path, PathBuf::from("after.txt"));
    }

    #[test]
    fn unmerged_entries_land_in_both_groups() {
        let mut raw = Vec::new();
        raw.extend_from_slice(b"u UU N... 1 2 3 4 a b c conflict.rs");
        raw.push(0);
        let parsed = parse_porcelain_v2(&raw);
        assert_eq!(parsed.staged.len(), 1);
        assert_eq!(parsed.unstaged.len(), 1);
    }

    #[test]
    fn torn_tail_from_the_stdout_cap_is_dropped() {
        let mut raw = Vec::new();
        raw.extend_from_slice(b"1 M. N... 100644 100644 100644 a a whole.rs");
        raw.push(0);
        // Torn record: no NUL terminator, truncated mid-hash.
        raw.extend_from_slice(b"1 .M N... 100644 10064");
        let parsed = parse_porcelain_v2(&raw);
        assert_eq!(parsed.staged.len(), 1);
        assert_eq!(parsed.staged[0].path, PathBuf::from("whole.rs"));
        assert!(parsed.unstaged.is_empty());
    }

    #[test]
    fn truncation_fills_groups_in_order_with_an_accurate_flag() {
        let parsed = ParsedStatus {
            staged: vec![entry("s1", 'M', '.'), entry("s2", 'M', '.')],
            unstaged: vec![entry("u1", '.', 'M')],
            untracked: vec![entry("n1", '?', '?')],
            ..ParsedStatus::default()
        };
        let capped = truncate_parsed(parsed, 3);
        assert_eq!(capped.staged.len(), 2);
        assert_eq!(capped.unstaged.len(), 1);
        assert!(capped.untracked.is_empty());
        assert!(capped.truncated);
        let roomy = truncate_parsed(
            ParsedStatus {
                staged: vec![entry("s1", 'M', '.')],
                ..ParsedStatus::default()
            },
            100,
        );
        assert!(!roomy.truncated);
    }

    #[test]
    fn join_rejects_traversal_but_accepts_missing_paths() {
        let root = std::env::temp_dir().join(format!("omaterm-m14-join-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(root.join("sub")).unwrap();
        // Missing paths (deleted files) resolve lexically.
        let gone = join_under_root(&root, Path::new("gone.txt")).unwrap();
        assert!(gone.ends_with("gone.txt"));
        // Traversal above the root is rejected before git runs.
        assert!(matches!(
            join_under_root(&root, Path::new("../outside.txt")),
            Err(GitError::PathOutsideRoot)
        ));
        assert!(matches!(
            join_under_root(&root, Path::new("sub/../../outside.txt")),
            Err(GitError::PathOutsideRoot)
        ));
        // The root itself is never a valid mutation target.
        assert!(matches!(
            join_under_root(&root, Path::new(".")),
            Ok(path) if path == std::fs::canonicalize(&root).unwrap()
        ));
        let _ = std::fs::remove_dir_all(&root);
    }

    #[cfg(unix)]
    #[test]
    fn join_rejects_symlink_escapes_for_existing_targets() {
        let root = std::env::temp_dir().join(format!("omaterm-m14-link-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        let outside =
            std::env::temp_dir().join(format!("omaterm-m14-outside-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&outside);
        std::fs::create_dir_all(&outside).unwrap();
        std::fs::write(outside.join("secret.txt"), b"secret").unwrap();
        std::os::unix::fs::symlink(&outside, root.join("link")).unwrap();
        let error = join_under_root(&root, Path::new("link/secret.txt")).expect_err("escape");
        assert_eq!(error.code(), "path_outside_root");
        let _ = std::fs::remove_dir_all(&root);
        let _ = std::fs::remove_dir_all(&outside);
    }

    #[test]
    fn missing_binary_maps_to_git_unavailable() {
        let missing = std::env::temp_dir().join("omaterm-m14-no-such-git");
        let _ = std::fs::remove_file(&missing);
        let result = run_git_with(
            missing.to_str().unwrap(),
            std::env::temp_dir().as_path(),
            &[],
            &["status"],
            None,
            Duration::from_secs(5),
            1024,
        );
        assert!(
            matches!(result, Err(GitError::GitUnavailable(_))),
            "missing binary must surface git_unavailable, got {result:?}"
        );
    }

    #[test]
    fn stable_codes_mark_each_failure() {
        assert_eq!(GitError::NotARepo.code(), "not_a_repo");
        assert_eq!(GitError::GitFailed("x".into()).code(), "git_failed");
        assert_eq!(
            GitError::GitUnavailable("x".into()).code(),
            "git_unavailable"
        );
        assert_eq!(GitError::PathOutsideRoot.code(), "path_outside_root");
        assert_eq!(GitError::Timeout.code(), "timeout");
    }
}
