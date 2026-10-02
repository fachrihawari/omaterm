//! Unified-diff parser + runner over the system git binary (M15,
//! blueprint §33).
//!
//! Read-only-by-default viewer for unstaged (`git diff`), staged
//! (`git diff --cached`), and single-path diffs, always with `--no-color
//! --no-ext-diff` for machine parsing. The model is `Diff → Files[] →
//! Hunks[] → Lines[]`; binary files render as "binary, not shown" (no
//! content stored). Everything is capped before allocation
//! (files/hunks/lines/bytes) with an accurate `truncated` flag. No syntax
//! highlighting (Tree-sitter arrives with the v0.3 editor); the desktop
//! colors `±` lines in plain monospace.
//!
//! Env hygiene and bounded waits reuse the M14 [`super::git`] contract:
//! prompts disabled, no pager, stdin closed, kill+reap on timeout. Stdout
//! is capped at [`MAX_DIFF_BYTES`] (a torn tail line is dropped, never
//! half-parsed); stderr is capped by the shared runner. No `gpui`
//! dependency. Paths in [`omaterm_core::DiffInfo`] are relative to the
//! project root: git runs with the root as its working directory.

use std::path::{Path, PathBuf};

use omaterm_core::{
    DiffFileInfo, DiffFileStatus, DiffHunkInfo, DiffInfo, DiffLineInfo, DiffLineKind,
};

use super::git::{
    GIT_MUTATION_TIMEOUT, GIT_STATUS_TIMEOUT, GitError, join_under_root, run_git, run_git_input,
};

/// Largest `git diff` stdout kept in memory. Beyond this the parse still
/// succeeds over the kept prefix (the torn tail line is dropped) and the
/// envelope is marked truncated — never an unbounded memory interface
/// (blueprint §64).
pub const MAX_DIFF_BYTES: usize = 4 * 1024 * 1024;
/// Largest file count in one envelope. The 1000-file integration case
/// proves caps hit with a valid envelope.
pub const MAX_DIFF_FILES: usize = 1_000;
/// Largest hunk count kept per file.
pub const MAX_DIFF_HUNKS_PER_FILE: usize = 256;
/// Largest line count kept per hunk.
pub const MAX_DIFF_LINES_PER_HUNK: usize = 1_000;
/// Largest single diff line kept in bytes. Truncation lands on a `char`
/// boundary so multibyte sequences never split.
pub const MAX_DIFF_LINE_BYTES: usize = 8 * 1024;
/// Largest accepted `-U` context size. Generous for review, small enough
/// to keep one wire response bounded.
pub const MAX_DIFF_CONTEXT_LINES: u8 = 10;
/// Default `-U` context size (git's own default).
pub const DEFAULT_DIFF_CONTEXT_LINES: u8 = 3;

/// What to diff. `path` is a root-relative (or absolute-inside-root)
/// single-path filter; `files_only` parses file headers and hunk counts
/// but drops hunk bodies (the fast 1000-file surface).
#[derive(Debug, Clone)]
pub struct DiffRequest {
    pub staged: bool,
    pub path: Option<PathBuf>,
    pub context_lines: u8,
    pub files_only: bool,
}

impl DiffRequest {
    pub fn unstaged() -> Self {
        Self {
            staged: false,
            path: None,
            context_lines: DEFAULT_DIFF_CONTEXT_LINES,
            files_only: false,
        }
    }
}

/// Read-only unified diff over `root`. `NotARepo` lets the router render
/// the explicit empty envelope (M14 product contract); traversal escapes
/// are rejected before git ever runs.
pub fn git_diff(root: &Path, request: &DiffRequest) -> Result<DiffInfo, GitError> {
    let context = request.context_lines.min(MAX_DIFF_CONTEXT_LINES);
    let context_arg = format!("-U{}", context);
    let mut args = vec![
        "--literal-pathspecs".to_owned(),
        "--no-optional-locks".to_owned(),
        "-c".to_owned(),
        "status.relativePaths=true".to_owned(),
        "diff".to_owned(),
        "--no-color".to_owned(),
        "--no-ext-diff".to_owned(),
        "--no-textconv".to_owned(),
        "--src-prefix=a/".to_owned(),
        "--dst-prefix=b/".to_owned(),
        context_arg,
        "--".to_owned(),
    ];
    if request.staged {
        // `--cached` must precede `--`; insert before the separator.
        args.insert(args.len() - 1, "--cached".to_owned());
    }
    if let Some(path) = &request.path {
        let joined = join_under_root(root, path)?;
        let relative = joined
            .strip_prefix(std::fs::canonicalize(root).map_err(GitError::Io)?)
            .map_err(|_| GitError::PathOutsideRoot)?;
        if relative.as_os_str().is_empty() {
            return Err(GitError::PathOutsideRoot);
        }
        args.push(relative.to_string_lossy().into_owned());
    }
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    match run_git(root, &[], &arg_refs, GIT_STATUS_TIMEOUT, MAX_DIFF_BYTES) {
        Ok(output) => {
            let mut info = parse_diff(&output.stdout, output.stdout_capped, request.files_only);
            info.staged = request.staged;
            Ok(info)
        }
        // Outside a work tree git reinterprets `diff` as a `--no-index`
        // two-path comparison and rejects repo-only options (`--cached`,
        // `--src-prefix`): that reinterpretation IS the non-repository
        // signal, so it maps to `NotARepo` (empty envelope) like the
        // plain warning. Every other failure stays loud.
        Err(GitError::GitFailed(message)) if message.contains("no-index") => {
            Err(GitError::NotARepo)
        }
        Err(error) => Err(error),
    }
}

/// Stage one complete, current unstaged hunk. The caller supplies only the
/// root-contained path and a hunk ID returned by [`git_diff`]; this function
/// re-reads the raw authoritative diff and generates the exact patch itself.
/// Capped, missing, or changed hunks are rejected rather than approximated.
pub fn git_stage_hunk(root: &Path, path: &Path, hunk_id: u64) -> Result<(), GitError> {
    let joined = join_under_root(root, path)?;
    let canonical_root = std::fs::canonicalize(root)?;
    let relative = joined
        .strip_prefix(&canonical_root)
        .map_err(|_| GitError::PathOutsideRoot)?;
    if relative.as_os_str().is_empty() {
        return Err(GitError::PathOutsideRoot);
    }
    let path = relative.to_string_lossy().into_owned();
    let args = [
        "--literal-pathspecs",
        "--no-optional-locks",
        "diff",
        "--no-color",
        "--no-ext-diff",
        "--no-textconv",
        "--src-prefix=a/",
        "--dst-prefix=b/",
        "-U3",
        "--",
        &path,
    ];
    let output = run_git(root, &[], &args, GIT_STATUS_TIMEOUT, MAX_DIFF_BYTES)?;
    if output.stdout_capped {
        return Err(GitError::GitFailed(
            "diff is too large to stage a hunk safely".into(),
        ));
    }
    let info = parse_diff(&output.stdout, false, false);
    let Some(file) = info.files.iter().find(|file| file.path == Path::new(&path)) else {
        return Err(GitError::GitFailed(
            "selected hunk is no longer available".into(),
        ));
    };
    if file.truncated || file.binary || file.status != DiffFileStatus::Modified {
        return Err(GitError::GitFailed(
            "selected hunk is not safe to stage".into(),
        ));
    }
    let Some(index) = file.hunks.iter().position(|hunk| hunk.id == hunk_id) else {
        return Err(GitError::GitFailed("selected hunk is stale".into()));
    };
    if file.hunks[index].truncated {
        return Err(GitError::GitFailed("selected hunk is not complete".into()));
    }
    let patch = extract_hunk_patch(&output.stdout, index)
        .ok_or_else(|| GitError::GitFailed("selected hunk cannot be reconstructed".into()))?;
    run_git_input(
        root,
        &[],
        &["apply", "--cached", "--whitespace=nowarn"],
        &patch,
        GIT_MUTATION_TIMEOUT,
        64 * 1024,
    )?;
    Ok(())
}

/// Keep the file headers plus exactly one raw `@@` block. This only accepts
/// raw Git output generated immediately above; it never consumes UI/IPC text.
fn extract_hunk_patch(raw: &[u8], selected: usize) -> Option<Vec<u8>> {
    let mut starts = Vec::new();
    let mut offset = 0;
    for line in raw.split_inclusive(|byte| *byte == b'\n') {
        // A directory path or rename can produce multiple files or extended
        // metadata. Never accidentally stage another file or mode change.
        if (offset > 0 && line.starts_with(b"diff --git "))
            || line.starts_with(b"old mode ")
            || line.starts_with(b"new mode ")
        {
            return None;
        }
        if line.starts_with(b"@@ ") {
            starts.push(offset);
        }
        offset += line.len();
    }
    let start = *starts.get(selected)?;
    let end = starts.get(selected + 1).copied().unwrap_or(raw.len());
    let mut patch = Vec::with_capacity(start + end - start);
    patch.extend_from_slice(&raw[..starts[0]]);
    patch.extend_from_slice(&raw[start..end]);
    Some(patch)
}

/// Parse unified-diff bytes into the bounded envelope. Unknown or
/// malformed lines are skipped, never fatal, so one odd record cannot sink
/// a whole diff. A torn tail (stdout cap cut mid-line) is dropped.
pub fn parse_diff(output: &[u8], capped: bool, files_only: bool) -> DiffInfo {
    let text = String::from_utf8_lossy(output);
    let text = text.as_ref();
    // A capped read can end mid-line: the last fragment is not a real
    // record, so it goes with the cap rather than parsing half a hunk.
    // A file record left with nothing but its `diff --git` header by the
    // cut is dropped below for the same reason; the envelope flag stays
    // accurate either way.
    let ends_clean = output.ends_with(b"\n");
    let mut truncated = capped;
    let parse_text = if capped && !ends_clean {
        let complete_len = text.rfind('\n').map_or(0, |index| index + 1);
        &text[..complete_len]
    } else {
        text
    };
    let mut files: Vec<DiffFileInfo> = Vec::new();
    let mut current: Option<FileBuilder> = None;
    let mut hunk: Option<HunkBuilder> = None;

    // Flush the open hunk into its file (obeys the per-hunk line cap).
    let flush_hunk =
        |hunk: &mut Option<HunkBuilder>, current: &mut Option<FileBuilder>, files_only: bool| {
            let Some(builder) = hunk.take() else {
                return;
            };
            let Some(file) = current.as_mut() else {
                return;
            };
            file.hunk_total += 1;
            file.substantial = true;
            if files_only || file.hunks.len() >= MAX_DIFF_HUNKS_PER_FILE {
                if file.hunks.len() >= MAX_DIFF_HUNKS_PER_FILE {
                    file.truncated = true;
                }
                return;
            }
            let kept = builder
                .lines
                .into_iter()
                .take(MAX_DIFF_LINES_PER_HUNK)
                .collect::<Vec<_>>();
            if builder.line_total > kept.len() {
                file.truncated = true;
            }
            let id = DiffHunkInfo::id_for(
                builder.old_start,
                builder.old_lines,
                builder.new_start,
                builder.new_lines,
                &kept,
            );
            file.hunks.push(DiffHunkInfo {
                id,
                old_start: builder.old_start,
                old_lines: builder.old_lines,
                new_start: builder.new_start,
                new_lines: builder.new_lines,
                lines: kept,
                truncated: builder.line_total > MAX_DIFF_LINES_PER_HUNK,
            });
        };

    // `lines()` iterates without allocating a potentially multi-million-
    // entry vector; `parse_text` excludes a capped torn tail line.
    for line in parse_text.lines() {
        if let Some(rest) = line.strip_prefix("diff --git ") {
            flush_hunk(&mut hunk, &mut current, files_only);
            if let Some(builder) = current.take() {
                files.push(builder.finish());
            }
            if files.len() >= MAX_DIFF_FILES {
                truncated = true;
                current = None;
                // Keep consuming input shape (headers only) without
                // allocating further files.
                continue;
            }
            current = Some(FileBuilder::new(rest));
            continue;
        }
        if current.is_none() {
            continue;
        }
        if line.starts_with("Binary files ") && line.ends_with(" differ") {
            flush_hunk(&mut hunk, &mut current, files_only);
            if let Some(file) = current.as_mut() {
                file.binary = true;
                file.substantial = true;
                file.hunks.clear();
            }
            continue;
        }
        if line.starts_with("GIT binary patch") {
            flush_hunk(&mut hunk, &mut current, files_only);
            if let Some(file) = current.as_mut() {
                file.binary = true;
                file.substantial = true;
                file.hunks.clear();
            }
            continue;
        }
        // Hunk headers and body lines need the hunk slot, not the file.
        if let Some(header) = line.strip_prefix("@@ ") {
            flush_hunk(&mut hunk, &mut current, files_only);
            if let Some(span) = parse_hunk_header(header) {
                hunk = Some(HunkBuilder::new(span));
            }
            continue;
        }
        if line == "\\ No newline at end of file" {
            // The marker belongs to the preceding source line. Retaining it
            // is required for a truthful preview and exact future patch use.
            if let Some(body) = hunk.as_mut()
                && body.line_total == body.lines.len()
                && let Some(previous) = body.lines.last_mut()
            {
                previous.no_newline_at_end = true;
            }
            continue;
        }
        if hunk.is_some() {
            if let Some((kind, content)) = split_body_line(line) {
                let overlong = content.len() > MAX_DIFF_LINE_BYTES;
                if let Some(body) = hunk.as_mut() {
                    body.line_total += 1;
                    if !files_only && body.lines.len() < MAX_DIFF_LINES_PER_HUNK {
                        body.lines.push(DiffLineInfo {
                            kind,
                            text: truncate_str(content, MAX_DIFF_LINE_BYTES),
                            no_newline_at_end: false,
                        });
                    }
                }
                if overlong && let Some(file) = current.as_mut() {
                    file.truncated = true;
                }
            }
            continue;
        }
        // File-header lines below only run when no hunk is open.
        if line.starts_with("new file mode") {
            if let Some(file) = current.as_mut() {
                file.new_file = true;
                file.substantial = true;
            }
            continue;
        }
        if line.starts_with("deleted file mode") {
            if let Some(file) = current.as_mut() {
                file.deleted_file = true;
                file.substantial = true;
            }
            continue;
        }
        if let Some(value) = line.strip_prefix("rename from ") {
            let value = unquote(value.trim().to_owned());
            if let Some(file) = current.as_mut() {
                file.rename_from = Some(value);
                file.substantial = true;
            }
            continue;
        }
        if let Some(value) = line.strip_prefix("rename to ") {
            let value = unquote(value.trim().to_owned());
            if let Some(file) = current.as_mut() {
                file.rename_to = Some(value);
                file.substantial = true;
            }
            continue;
        }
        if line.starts_with("copy from ") || line.starts_with("copy to ") {
            if let Some(file) = current.as_mut() {
                file.copied = true;
                file.substantial = true;
            }
            continue;
        }
        if line.starts_with("old mode") || line.starts_with("new mode") {
            // Mode-only change: no legs exist, so these lines are the
            // substance that keeps the file (see `fallback_path`).
            if let Some(file) = current.as_mut() {
                file.substantial = true;
            }
            continue;
        }
        if let Some(rest) = line.strip_prefix("--- ") {
            let leg = strip_leg(rest);
            if let Some(file) = current.as_mut() {
                file.old_leg = Some(leg);
                file.substantial = true;
            }
            continue;
        }
        if let Some(rest) = line.strip_prefix("+++ ") {
            let leg = strip_leg(rest);
            if let Some(file) = current.as_mut() {
                file.new_leg = Some(leg);
                file.substantial = true;
            }
            continue;
        }
        // All other lines (index lines, mode lines, extended headers)
        // carry no envelope data and are skipped by design.
    }
    flush_hunk(&mut hunk, &mut current, files_only);
    if let Some(builder) = current.take() {
        // A capped cut can strand a bare `diff --git` header (or a
        // half extended header) with no substance: drop it rather than
        // emit a phantom file. Complete mode-only changes only occur
        // here with a clean ending, so they survive.
        if builder.substantial || !capped || ends_clean {
            files.push(builder.finish());
        }
    }
    truncated |= files
        .iter()
        .any(|file| file.truncated || file.hunks.iter().any(|hunk| hunk.truncated));
    DiffInfo {
        files,
        truncated,
        staged: false,
    }
}

/// Truncate to at most `max_bytes` on a `char` boundary (multibyte-safe:
/// no split UTF-8 sequence, no broken grapheme byte).
fn truncate_str(text: &str, max_bytes: usize) -> String {
    if text.len() <= max_bytes {
        return text.to_owned();
    }
    let mut end = max_bytes;
    while end > 0 && !text.is_char_boundary(end) {
        end -= 1;
    }
    text[..end].to_owned()
}

/// Split a hunk body line into its kind marker and content. Only the three
/// unified-diff markers are content; anything else is skipped.
fn split_body_line(line: &str) -> Option<(DiffLineKind, &str)> {
    let (marker, content) = line.split_at_checked(1)?;
    match marker {
        " " => Some((DiffLineKind::Context, content)),
        "+" => Some((DiffLineKind::Addition, content)),
        "-" => Some((DiffLineKind::Deletion, content)),
        _ => None,
    }
}

/// Parse `-old[,count] +new[,count]` spans from a hunk header with the
/// leading `@@ ` already stripped by the caller. A missing count means
/// one line (unified-diff convention); a zero count is a pure
/// insertion/deletion anchor and parses to zero.
fn parse_hunk_header(header: &str) -> Option<(u32, u32, u32, u32)> {
    let spans = header.split_once(" @@")?.0;
    let (old, new) = spans.split_once(' ')?;
    Some((
        parse_span(old.strip_prefix('-')?)?.0,
        parse_span(old.strip_prefix('-')?)?.1,
        parse_span(new.strip_prefix('+')?)?.0,
        parse_span(new.strip_prefix('+')?)?.1,
    ))
}

fn parse_span(span: &str) -> Option<(u32, u32)> {
    match span.split_once(',') {
        Some((start, count)) => Some((start.parse().ok()?, count.parse().ok()?)),
        None => Some((span.parse().ok()?, 1)),
    }
}

/// Strip the `a/`/`b/` prefix git emits (pinned by `--src-prefix`/
/// `--dst-prefix`) and any trailing timestamp. `/dev/null` marks the
/// added/deleted leg.
fn strip_leg(leg: &str) -> String {
    let path = leg.split('\t').next().unwrap_or(leg).trim();
    let path = unquote(path.to_owned());
    path.strip_prefix("a/")
        .or_else(|| path.strip_prefix("b/"))
        .map(str::to_owned)
        .unwrap_or(path)
}

/// Undo git's C-style quoting of unusual paths (`"my file ü.txt"` with
/// octal escapes for non-ASCII bytes). Unquoted paths pass through.
fn unquote(path: String) -> String {
    if !(path.starts_with('"') && path.ends_with('"') && path.len() >= 2) {
        return path;
    }
    let inner = &path[1..path.len() - 1];
    let bytes = inner.as_bytes();
    let mut out: Vec<u8> = Vec::with_capacity(inner.len());
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] != b'\\' || index + 1 >= bytes.len() {
            out.push(bytes[index]);
            index += 1;
            continue;
        }
        match bytes[index + 1] {
            b'n' => {
                out.push(b'\n');
                index += 2;
            }
            b't' => {
                out.push(b'\t');
                index += 2;
            }
            b'"' => {
                out.push(b'"');
                index += 2;
            }
            b'\\' => {
                out.push(b'\\');
                index += 2;
            }
            digit @ b'0'..=b'7' => {
                let mut value = (digit - b'0') as u32;
                let mut consumed = 1;
                for offset in 2..4 {
                    if index + offset < bytes.len()
                        && bytes[index + offset].is_ascii_digit()
                        && bytes[index + offset] < b'8'
                    {
                        value = value * 8 + (bytes[index + offset] - b'0') as u32;
                        consumed += 1;
                    } else {
                        break;
                    }
                }
                out.push(value as u8);
                index += 1 + consumed;
            }
            _ => {
                out.push(bytes[index + 1]);
                index += 2;
            }
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

struct HunkBuilder {
    old_start: u32,
    old_lines: u32,
    new_start: u32,
    new_lines: u32,
    lines: Vec<DiffLineInfo>,
    line_total: usize,
}

impl HunkBuilder {
    fn new(span: (u32, u32, u32, u32)) -> Self {
        Self {
            old_start: span.0,
            old_lines: span.1,
            new_start: span.2,
            new_lines: span.3,
            lines: Vec::new(),
            line_total: 0,
        }
    }
}

struct FileBuilder {
    git_paths: String,
    new_file: bool,
    deleted_file: bool,
    copied: bool,
    rename_from: Option<String>,
    rename_to: Option<String>,
    old_leg: Option<String>,
    new_leg: Option<String>,
    binary: bool,
    hunks: Vec<DiffHunkInfo>,
    hunk_total: usize,
    truncated: bool,
    /// Any record beyond the bare `diff --git` header. A capped cut that
    /// strands a header-only tail drops it (see `parse_diff`).
    substantial: bool,
}

impl FileBuilder {
    fn new(git_paths: &str) -> Self {
        Self {
            git_paths: git_paths.to_owned(),
            new_file: false,
            deleted_file: false,
            copied: false,
            rename_from: None,
            rename_to: None,
            old_leg: None,
            new_leg: None,
            binary: false,
            hunks: Vec::new(),
            hunk_total: 0,
            truncated: false,
            substantial: false,
        }
    }

    fn finish(mut self) -> DiffFileInfo {
        // Display path: the new leg wins (post-image); the old leg is
        // kept for renames. `/dev/null` legs mark added/deleted files.
        let new_path = self
            .rename_to
            .clone()
            .or_else(|| self.new_leg.clone())
            .unwrap_or_default();
        let old_path = self.rename_from.clone().or_else(|| self.old_leg.clone());
        let new_is_null = new_path == "/dev/null";
        let old_is_null = old_path.as_deref() == Some("/dev/null");
        let path = if new_is_null {
            old_path.clone().unwrap_or_default()
        } else if new_path.is_empty() {
            // Header-only change (mode-only edit): fall back to the
            // `diff --git` paths.
            fallback_path(&self.git_paths)
        } else {
            new_path
        };
        let status = if self.rename_from.is_some() || self.rename_to.is_some() {
            DiffFileStatus::Renamed
        } else if self.copied {
            DiffFileStatus::Copied
        } else if self.new_file || old_is_null {
            DiffFileStatus::Added
        } else if self.deleted_file || new_is_null {
            DiffFileStatus::Deleted
        } else {
            DiffFileStatus::Modified
        };
        let old_path = match status {
            DiffFileStatus::Renamed => old_path.filter(|old| old != &path),
            _ => None,
        };
        if self.binary {
            self.hunks.clear();
        }
        let hunk_count = self.hunk_total.max(self.hunks.len());
        DiffFileInfo {
            path: PathBuf::from(path),
            old_path: old_path.map(PathBuf::from),
            status,
            binary: self.binary,
            hunks: self.hunks,
            hunk_count,
            truncated: self.truncated,
        }
    }
}

/// `diff --git` fallback when no `---`/`+++` legs exist (mode-only
/// change): the `b/` path, unquoted and prefix-stripped.
fn fallback_path(git_paths: &str) -> String {
    let Some((_, second)) = parse_git_header_paths(git_paths) else {
        return String::new();
    };
    let path = unquote(second.to_owned());
    path.strip_prefix("b/")
        .or_else(|| path.strip_prefix("a/"))
        .map(str::to_owned)
        .unwrap_or(path)
}

/// Parse the two path tokens from Git's `diff --git <old> <new>` header.
/// Paths needing escaping are C-quoted by Git; quoted tokens can contain
/// spaces and escaped quotes, so splitting on whitespace is not sufficient.
fn parse_git_header_paths(header: &str) -> Option<(&str, &str)> {
    let bytes = header.as_bytes();
    let mut tokens = [None, None];
    let mut cursor = 0;
    for token in &mut tokens {
        while cursor < bytes.len() && bytes[cursor].is_ascii_whitespace() {
            cursor += 1;
        }
        if cursor >= bytes.len() {
            return None;
        }
        let start = cursor;
        if bytes[cursor] == b'"' {
            cursor += 1;
            while cursor < bytes.len() {
                if bytes[cursor] == b'\\' {
                    cursor = (cursor + 2).min(bytes.len());
                } else if bytes[cursor] == b'"' {
                    cursor += 1;
                    break;
                } else {
                    cursor += 1;
                }
            }
        } else {
            while cursor < bytes.len() && !bytes[cursor].is_ascii_whitespace() {
                cursor += 1;
            }
        }
        *token = Some(&header[start..cursor]);
    }
    Some((tokens[0]?, tokens[1]?))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parse(text: &str) -> DiffInfo {
        parse_diff(text.as_bytes(), false, false)
    }

    #[test]
    fn parses_modified_file_with_context_and_markers() {
        let raw = "diff --git a/src/main.rs b/src/main.rs\n\
            index 123..456 100644\n\
            --- a/src/main.rs\n\
            +++ b/src/main.rs\n\
            @@ -1,3 +1,4 @@ fn main() {\n\
            \u{20}let x = 1;\n\
            -let y = 2;\n\
            +let y = 3;\n\
            +let z = 4;\n\
            \\ No newline at end of file\n";
        let info = parse(raw);
        assert!(!info.truncated);
        assert_eq!(info.files.len(), 1);
        let file = &info.files[0];
        assert_eq!(file.path, PathBuf::from("src/main.rs"));
        assert_eq!(file.status, DiffFileStatus::Modified);
        assert!(!file.binary);
        assert_eq!(file.hunks.len(), 1);
        let hunk = &file.hunks[0];
        assert_eq!((hunk.old_start, hunk.old_lines), (1, 3));
        assert_eq!((hunk.new_start, hunk.new_lines), (1, 4));
        let kinds: Vec<DiffLineKind> = hunk.lines.iter().map(|line| line.kind).collect();
        assert_eq!(
            kinds,
            vec![
                DiffLineKind::Context,
                DiffLineKind::Deletion,
                DiffLineKind::Addition,
                DiffLineKind::Addition,
            ]
        );
        assert_eq!(hunk.lines[1].text, "let y = 2;");
        assert!(hunk.lines[3].no_newline_at_end);
    }

    #[test]
    fn parses_added_deleted_renamed_and_mode_only_files() {
        let raw = "diff --git a/new.txt b/new.txt\n\
            new file mode 100644\n\
            --- /dev/null\n\
            +++ b/new.txt\n\
            @@ -0,0 +1 @@\n\
            +hello\n\
            diff --git a/gone.txt b/gone.txt\n\
            deleted file mode 100644\n\
            --- a/gone.txt\n\
            +++ /dev/null\n\
            @@ -1 +0,0 @@\n\
            -bye\n\
            diff --git a/old.txt b/new-name.txt\n\
            similarity index 90%\n\
            rename from old.txt\n\
            rename to new-name.txt\n\
            --- a/old.txt\n\
            +++ b/new-name.txt\n\
            @@ -1 +1 @@\n\
            \u{20}same\n\
            diff --git a/run.sh b/run.sh\n\
            old mode 100644\n\
            new mode 100755\n";
        let info = parse(raw);
        assert_eq!(info.files.len(), 4);
        assert_eq!(info.files[0].status, DiffFileStatus::Added);
        assert_eq!(info.files[0].path, PathBuf::from("new.txt"));
        assert_eq!(info.files[1].status, DiffFileStatus::Deleted);
        assert_eq!(info.files[2].status, DiffFileStatus::Renamed);
        assert_eq!(info.files[2].path, PathBuf::from("new-name.txt"));
        assert_eq!(info.files[2].old_path, Some(PathBuf::from("old.txt")));
        // Mode-only change: no legs, falls back to the b/ path.
        assert_eq!(info.files[3].status, DiffFileStatus::Modified);
        assert_eq!(info.files[3].path, PathBuf::from("run.sh"));
        assert!(info.files[3].hunks.is_empty());

        // The mode-only fallback must parse Git's quoted path pair rather
        // than splitting at the first path-internal space.
        let mode_only_quoted = "diff --git \"a/my mode file.txt\" \"b/my mode file.txt\"\nold mode 100644\nnew mode 100755\n";
        let info = parse(mode_only_quoted);
        assert_eq!(info.files[0].path, PathBuf::from("my mode file.txt"));
    }

    #[test]
    fn binary_files_carry_no_content() {
        let raw = "diff --git a/logo.png b/logo.png\n\
            new file mode 100644\n\
            index 0000000..1111111\n\
            Binary files /dev/null and b/logo.png differ\n";
        let info = parse(raw);
        assert_eq!(info.files.len(), 1);
        assert!(info.files[0].binary);
        assert!(info.files[0].hunks.is_empty());
        assert_eq!(info.files[0].status, DiffFileStatus::Added);
    }

    #[test]
    fn quoted_unicode_paths_with_spaces_survive() {
        // git C-quotes non-ASCII bytes as octal (`ü` = 303 274).
        let raw = "diff --git \"a/my file \\303\\274.txt\" \"b/my file \\303\\274.txt\"\n\
            --- \"a/my file \\303\\274.txt\"\n\
            +++ \"b/my file \\303\\274.txt\"\n\
            @@ -1 +1 @@\n\
            -old\n\
            +new\n";
        let info = parse(raw);
        assert_eq!(info.files.len(), 1);
        assert_eq!(info.files[0].path, PathBuf::from("my file ü.txt"));
        assert_eq!(info.files[0].hunks[0].lines[1].text, "new");
    }

    #[test]
    fn huge_single_line_truncates_multibyte_safe() {
        let wide = "é".repeat(10_000);
        let raw = format!(
            "diff --git a/wide.txt b/wide.txt\n--- a/wide.txt\n+++ b/wide.txt\n@@ -1 +1 @@\n+{wide}\n"
        );
        let info = parse(&raw);
        let line = &info.files[0].hunks[0].lines[0];
        assert!(line.text.len() <= MAX_DIFF_LINE_BYTES);
        assert!(line.text.chars().count() > 1000);
        // Truncation lands on a char boundary: valid UTF-8, no half-`é`.
        assert!(line.text.is_char_boundary(line.text.len()));
        assert!(info.files[0].truncated);
    }

    #[test]
    fn file_cap_sets_the_envelope_flag() {
        let mut raw = String::new();
        for index in 0..MAX_DIFF_FILES + 5 {
            raw.push_str(&format!(
                "diff --git a/f{index}.txt b/f{index}.txt\n--- a/f{index}.txt\n+++ b/f{index}.txt\n@@ -1 +1 @@\n-old\n+new\n"
            ));
        }
        let info = parse_diff(raw.as_bytes(), false, false);
        assert_eq!(info.files.len(), MAX_DIFF_FILES);
        assert!(info.truncated);
    }

    #[test]
    fn hunk_and_line_caps_keep_valid_prefixes_and_flags() {
        let mut raw =
            String::from("diff --git a/cap.txt b/cap.txt\n--- a/cap.txt\n+++ b/cap.txt\n");
        for index in 0..=MAX_DIFF_HUNKS_PER_FILE {
            raw.push_str(&format!("@@ -{index},1 +{index},1 @@\n-old\n+new\n"));
        }
        raw.push_str(
            "diff --git a/cap-lines.txt b/cap-lines.txt\n--- a/cap-lines.txt\n+++ b/cap-lines.txt\n@@ -1,1001 +1,1001 @@\n",
        );
        for _ in 0..MAX_DIFF_LINES_PER_HUNK + 1 {
            raw.push_str(" context\n");
        }

        let info = parse_diff(raw.as_bytes(), false, false);
        assert!(info.truncated);
        assert_eq!(info.files.len(), 2);
        let file = &info.files[0];
        assert_eq!(file.hunk_count, MAX_DIFF_HUNKS_PER_FILE + 1);
        assert_eq!(file.hunks.len(), MAX_DIFF_HUNKS_PER_FILE);
        assert!(file.truncated);
        assert_eq!(file.hunks[0].lines.len(), 2);
        assert_eq!(file.hunks[MAX_DIFF_HUNKS_PER_FILE - 1].lines.len(), 2);
        assert!(!file.hunks[MAX_DIFF_HUNKS_PER_FILE - 1].truncated);
        let capped_lines = &info.files[1];
        assert_eq!(capped_lines.hunks.len(), 1);
        assert_eq!(capped_lines.hunks[0].lines.len(), MAX_DIFF_LINES_PER_HUNK);
        assert!(capped_lines.hunks[0].truncated);
    }

    #[test]
    fn no_newline_marker_is_attached_to_its_preceding_line() {
        let raw = "diff --git a/a.txt b/a.txt\n\
            --- a/a.txt\n\
            +++ b/a.txt\n\
            @@ -1 +1 @@\n\
            -old\n\
            \\ No newline at end of file\n\
            +new\n\
            \\ No newline at end of file\n";
        let info = parse(raw);
        let lines = &info.files[0].hunks[0].lines;
        assert!(lines[0].no_newline_at_end);
        assert!(lines[1].no_newline_at_end);
    }

    #[test]
    fn torn_tail_from_the_stdout_cap_is_dropped() {
        let whole =
            "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n";
        // Cap cuts mid-record without a trailing newline: the fragment
        // must not parse as its own file.
        let torn = format!("{whole}diff --git a/b.txt b/b.txt\n--- a/b.tx");
        let info = parse_diff(torn.as_bytes(), true, false);
        assert_eq!(info.files.len(), 1);
        assert_eq!(info.files[0].path, PathBuf::from("a.txt"));
        assert!(info.truncated);
    }

    #[test]
    fn files_only_mode_counts_hunks_without_bodies() {
        let raw = "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n@@ -10 +10 @@\n\u{20}ctx\n";
        let info = parse_diff(raw.as_bytes(), false, true);
        assert_eq!(info.files.len(), 1);
        assert!(info.files[0].hunks.is_empty());
        assert_eq!(info.files[0].hunk_count, 2);
    }

    #[test]
    fn malformed_records_never_sink_the_diff() {
        let raw = "@@ nonsense @@\n\
            diff --git a/ok.txt b/ok.txt\n\
            --- a/ok.txt\n\
            +++ b/ok.txt\n\
            @@ -1 +1 @@\n\
            ??? unknown marker\n\
            +kept\n";
        let info = parse(raw);
        assert_eq!(info.files.len(), 1);
        assert_eq!(info.files[0].hunks.len(), 1);
        assert_eq!(info.files[0].hunks[0].lines.len(), 1);
        assert_eq!(info.files[0].hunks[0].lines[0].text, "kept");
    }

    #[test]
    fn empty_diff_parses_to_an_empty_envelope() {
        let info = parse("");
        assert!(info.files.is_empty());
        assert!(!info.truncated);
    }
}
