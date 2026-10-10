//! M15 unified-diff panel state: read-only hunk view over `git diff`
//! plus per-hunk file-stage buttons.
//!
//! GPUI-free. Rendering and key/mouse wiring live in `main.rs`, which owns
//! the worker threads: root resolution plus `git diff` run on a background
//! worker owned by [`DiffWorker`], never on the UI thread
//! (M14 off-thread precedent). This panel only tracks last-good diffs,
//! explicit empty/error states, the selected file/hunk, and the
//! staged/unstaged view toggle. Mutations (hunk stage buttons) go through
//! `GitCommand::StageHunk` — the same path as
//! IPC/CLI (blueprint §62) — and set a refresh hint the poller consumes.
//!
//! Payloads are `omaterm_core::DiffInfo` (root-relative paths, bounded
//! entries, accurate `truncated`). Code lines carry presentation-only spans
//! from the shared built-in highlighter over the `±` line coloring
//! (user-directed M15 scope change; each line tokenizes independently).

use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::sync::{Arc, Condvar, Mutex};
use std::thread::ThreadId;

use omaterm_core::{DiffInfo, GitComparisonBase, GitObjectId};

use crate::editor::{TokenSpan, tokenize};
use crate::git_panel::RepoKey;

/// One historical file selection from an expanded Graph commit. The base is
/// always explicit (first parent, or the resolved empty tree for roots);
/// merge parent switching reuses this selection with a different base.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CommitPreviewSel {
    pub commit: GitObjectId,
    pub base: GitComparisonBase,
    pub old_path: Option<PathBuf>,
    pub path: PathBuf,
    /// Bounded row metadata for the preview header (never refetched).
    pub subject: String,
    pub author_name: String,
    /// Full commit message body for the expanded header (C9.4). `None`
    /// until the details worker lands; worktree previews never set it.
    pub body: Option<String>,
    /// Parent oids for the merge parent selector (C9.4). Empty until the
    /// details worker lands.
    pub parents: Vec<GitObjectId>,
}

impl CommitPreviewSel {
    /// Provenance identity for rendering capabilities. Every control derives
    /// from this source: a commit preview never offers stage/unstage,
    /// discard or hunk-stage actions.
    pub fn source(&self) -> omaterm_core::DiffSource {
        omaterm_core::DiffSource::Commit {
            commit: self.commit.clone(),
            base: self.base.clone(),
            old_path: self.old_path.clone(),
            path: self.path.clone(),
        }
    }
}

/// Explicit non-data state for a project+side. `NoRoot` (M12 `none`) and
/// `NotRepo` render the empty state, never an error; failures name the
/// cause without paths or contents (blueprint §45).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DiffEmpty {
    NoRoot,
    NotRepo,
    Cancelled,
    Unavailable(String),
    Failed(String),
}

/// Outcome of one background diff refresh, tagged with the worker thread
/// that ran git. The UI thread drains these; it never spawns git itself —
/// the `worker != caller` test below pins that contract.
#[derive(Debug)]
pub struct DiffRefresh {
    pub worker: ThreadId,
    pub result: Result<DiffInfo, DiffEmpty>,
}

/// Detail-view mode. Split is the mock default; both modes render from
/// the same bounded unified hunks (no full-file fetch). Code lines in both
/// modes carry highlight spans computed in [`preview_rows`].
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default)]
pub enum DiffMode {
    #[default]
    Split,
    Inline,
}

/// One aligned body row for Split/Inline rendering: exactly one of the
/// line numbers is present on add/delete-only rows. `tokens` holds
/// presentation-only highlight spans over `text` (byte offsets into the
/// final tab-expanded text); empty means plain rendering.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AlignedRow {
    pub old_no: Option<u32>,
    pub new_no: Option<u32>,
    pub kind: omaterm_core::DiffLineKind,
    pub text: String,
    pub tokens: Vec<TokenSpan>,
    pub no_newline_at_end: bool,
}

/// One present side of a Split diff row. Split presentation deliberately keeps
/// old/new text independent so replacement pairs never repeat one side's text.
/// `tokens` mirrors [`AlignedRow::tokens`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SplitCell {
    pub line_no: Option<u32>,
    pub kind: omaterm_core::DiffLineKind,
    pub text: String,
    pub tokens: Vec<TokenSpan>,
    pub no_newline_at_end: bool,
}

/// A visual row in a Split diff. Edit runs pair deletions with additions by
/// position; an unmatched edit has one absent side and renders as a spacer.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SplitRow {
    pub old: Option<SplitCell>,
    pub new: Option<SplitCell>,
}

/// One fixed-height presentation row. The diff preview flattens the selected
/// file once per render generation and feeds these rows to GPUI's virtual list
/// so a long hunk does not create a correspondingly large element tree.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PreviewRow {
    HunkHeader {
        hunk: usize,
        header: String,
        old_start: u32,
        old_lines: u32,
        new_start: u32,
        new_lines: u32,
    },
    HunkActions {
        hunk: usize,
        id: u64,
        can_stage: bool,
        can_open: bool,
        can_copy: bool,
    },
    Split(SplitRow),
    Inline(AlignedRow),
    NoNewline {
        old: bool,
        new: bool,
    },
    HunkTruncated,
    FileTruncated,
}

/// Presentation-only syntax spans for one diff content line. The language
/// comes from the diffed file's path; binary files and unknown extensions
/// yield no spans (plain rendering). Each line tokenizes independently, so
/// multiline strings/comments spanning hunk lines are a documented subset
/// gap — same single-pass built-in highlighter as the editor, no new
/// dependencies, no UI-thread filesystem work (pure text in, spans out).
pub fn highlight_diff_line(
    language: omaterm_context::EditorLanguage,
    text: &str,
) -> Vec<TokenSpan> {
    tokenize(language, text)
}

pub fn can_stage_hunk(
    file: &omaterm_core::DiffFileInfo,
    hunk: &omaterm_core::DiffHunkInfo,
    staged: bool,
) -> bool {
    !staged
        && !file.binary
        && !file.truncated
        && !hunk.truncated
        && file.status == omaterm_core::DiffFileStatus::Modified
}

pub fn unified_hunk_text(hunk: &omaterm_core::DiffHunkInfo) -> String {
    use omaterm_core::DiffLineKind;

    let mut text = String::with_capacity(hunk.header.len());
    text.push_str(&hunk.header);
    text.push('\n');
    for line in &hunk.lines {
        text.push(match line.kind {
            DiffLineKind::Context => ' ',
            DiffLineKind::Addition => '+',
            DiffLineKind::Deletion => '-',
        });
        text.push_str(&line.text);
        text.push('\n');
        if line.no_newline_at_end {
            text.push_str("\\ No newline at end of file\n");
        }
    }
    text
}

/// Flatten every parsed hunk and line in source order. Parser bounds remain
/// authoritative; the row cap belongs to `uniform_list`'s viewport, not a
/// second, invisible presentation limit.
pub fn preview_rows(
    file: &omaterm_core::DiffFileInfo,
    mode: DiffMode,
    staged: bool,
) -> Vec<PreviewRow> {
    let mut rows = Vec::new();
    for (hunk_index, hunk) in file.hunks.iter().enumerate() {
        rows.push(PreviewRow::HunkHeader {
            hunk: hunk_index,
            header: hunk.header.clone(),
            old_start: hunk.old_start,
            old_lines: hunk.old_lines,
            new_start: hunk.new_start,
            new_lines: hunk.new_lines,
        });
        rows.push(PreviewRow::HunkActions {
            hunk: hunk_index,
            id: hunk.id,
            can_stage: can_stage_hunk(file, hunk, staged),
            can_open: file.status != omaterm_core::DiffFileStatus::Deleted,
            can_copy: !file.binary && !file.truncated && !hunk.truncated,
        });
        match mode {
            DiffMode::Split => {
                for row in split_hunk(hunk) {
                    let no_newline = row.old.as_ref().is_some_and(|cell| cell.no_newline_at_end)
                        || row.new.as_ref().is_some_and(|cell| cell.no_newline_at_end);
                    let old = row.old.as_ref().is_some_and(|cell| cell.no_newline_at_end);
                    let new = row.new.as_ref().is_some_and(|cell| cell.no_newline_at_end);
                    rows.push(PreviewRow::Split(row));
                    if no_newline {
                        rows.push(PreviewRow::NoNewline { old, new });
                    }
                }
            }
            DiffMode::Inline => {
                for row in align_hunk(hunk) {
                    let no_newline = row.no_newline_at_end;
                    let old = no_newline && row.kind != omaterm_core::DiffLineKind::Addition;
                    let new = no_newline && row.kind != omaterm_core::DiffLineKind::Deletion;
                    rows.push(PreviewRow::Inline(row));
                    if no_newline {
                        rows.push(PreviewRow::NoNewline { old, new });
                    }
                }
            }
        }
        if hunk.truncated {
            rows.push(PreviewRow::HunkTruncated);
        }
    }
    if file.truncated {
        rows.push(PreviewRow::FileTruncated);
    }
    // Read-only presentation uses four spaces per tab. Copy/stage still read
    // the untouched source DTO, preserving the exact Git bytes. Highlight
    // spans are computed over the final tab-expanded text so renderer offsets
    // stay valid; binary files keep plain rows.
    let language = if file.binary {
        omaterm_context::EditorLanguage::Plain
    } else {
        omaterm_context::detect_language(&file.path)
    };
    for row in &mut rows {
        match row {
            PreviewRow::Inline(line) => {
                line.text = line.text.replace('\t', "    ");
                line.tokens = highlight_diff_line(language, &line.text);
            }
            PreviewRow::Split(line) => {
                for cell in line.old.iter_mut().chain(line.new.iter_mut()) {
                    cell.text = cell.text.replace('\t', "    ");
                    cell.tokens = highlight_diff_line(language, &cell.text);
                }
            }
            _ => {}
        }
    }
    rows
}

/// Row index for a hunk header, used to reveal the keyboard-selected hunk in
/// a virtualized list.
pub fn hunk_row_index(rows: &[PreviewRow], hunk_index: usize) -> Option<usize> {
    rows.iter()
        .position(|row| matches!(row, PreviewRow::HunkHeader { hunk, .. } if *hunk == hunk_index))
}

pub const PREVIEW_ROW_HEIGHT: f32 = 21.0;

/// Map a pixel offset through a mode switch/refresh by source side and line,
/// preferring the same hunk identity when it survives. Keep the fractional
/// intra-row position instead of converting smooth scrolling to whole rows.
pub fn remap_preview_offset(previous: &[PreviewRow], next: &[PreviewRow], offset: f32) -> f32 {
    if previous.is_empty() || next.is_empty() || !offset.is_finite() {
        return 0.0;
    }
    let top = (-offset).max(0.0);
    let index = ((top / PREVIEW_ROW_HEIGHT).floor() as usize).min(previous.len() - 1);
    let fraction = top % PREVIEW_ROW_HEIGHT;
    let source_line = |row: &PreviewRow| match row {
        PreviewRow::Inline(line) => (line.old_no, line.new_no),
        PreviewRow::Split(line) => (
            line.old.as_ref().and_then(|cell| cell.line_no),
            line.new.as_ref().and_then(|cell| cell.line_no),
        ),
        _ => (None, None),
    };
    let hunk_id = |rows: &[PreviewRow], index: usize| {
        rows[..=index].iter().rev().find_map(|row| match row {
            PreviewRow::HunkActions { id, .. } => Some(*id),
            _ => None,
        })
    };
    let (old, new) = source_line(&previous[index]);
    let id = hunk_id(previous, index);
    let matches_source = |row: &PreviewRow| {
        let (candidate_old, candidate_new) = source_line(row);
        if let Some(new) = new {
            candidate_new == Some(new)
        } else if let Some(old) = old {
            candidate_old == Some(old)
        } else {
            matches!(row, PreviewRow::HunkActions { id: candidate, .. } if Some(*candidate) == id)
        }
    };
    let destination = next
        .iter()
        .enumerate()
        .find_map(|(index, row)| {
            (matches_source(row) && hunk_id(next, index) == id).then_some(index)
        })
        .or_else(|| next.iter().position(matches_source))
        .unwrap_or(index.min(next.len() - 1));
    -(destination as f32 * PREVIEW_ROW_HEIGHT + fraction)
}

/// Align one hunk's unified lines into old/new rows. Context lines pair
/// both sides; deletions occupy the old side only; additions the new
/// side only. Line numbers derive from the hunk starts; blank spacer
/// rows are the renderer's job (it sees `None` on the missing side).
pub fn align_hunk(hunk: &omaterm_core::DiffHunkInfo) -> Vec<AlignedRow> {
    let mut rows = Vec::with_capacity(hunk.lines.len());
    let mut old_no = Some(hunk.old_start);
    let mut new_no = Some(hunk.new_start);
    for line in &hunk.lines {
        match line.kind {
            omaterm_core::DiffLineKind::Context => {
                rows.push(AlignedRow {
                    old_no,
                    new_no,
                    kind: line.kind,
                    text: line.text.clone(),
                    tokens: Vec::new(),
                    no_newline_at_end: line.no_newline_at_end,
                });
                old_no = old_no.and_then(|number| number.checked_add(1));
                new_no = new_no.and_then(|number| number.checked_add(1));
            }
            omaterm_core::DiffLineKind::Deletion => {
                rows.push(AlignedRow {
                    old_no,
                    new_no: None,
                    kind: line.kind,
                    text: line.text.clone(),
                    tokens: Vec::new(),
                    no_newline_at_end: line.no_newline_at_end,
                });
                old_no = old_no.and_then(|number| number.checked_add(1));
            }
            omaterm_core::DiffLineKind::Addition => {
                rows.push(AlignedRow {
                    old_no: None,
                    new_no,
                    kind: line.kind,
                    text: line.text.clone(),
                    tokens: Vec::new(),
                    no_newline_at_end: line.no_newline_at_end,
                });
                new_no = new_no.and_then(|number| number.checked_add(1));
            }
        }
    }
    rows
}

/// Convert a unified hunk to paired Split rows without changing its Inline
/// order. Context lines pair directly. Each consecutive edit run is collected
/// first, then deletions/additions pair by position through the longer side.
pub fn split_hunk(hunk: &omaterm_core::DiffHunkInfo) -> Vec<SplitRow> {
    use omaterm_core::DiffLineKind;

    let mut rows = Vec::with_capacity(hunk.lines.len());
    let mut old_no = Some(hunk.old_start);
    let mut new_no = Some(hunk.new_start);
    let mut index = 0;
    while index < hunk.lines.len() {
        let line = &hunk.lines[index];
        if line.kind == DiffLineKind::Context {
            rows.push(SplitRow {
                old: Some(SplitCell {
                    line_no: old_no,
                    kind: line.kind,
                    text: line.text.clone(),
                    tokens: Vec::new(),
                    no_newline_at_end: line.no_newline_at_end,
                }),
                new: Some(SplitCell {
                    line_no: new_no,
                    kind: line.kind,
                    text: line.text.clone(),
                    tokens: Vec::new(),
                    no_newline_at_end: line.no_newline_at_end,
                }),
            });
            old_no = old_no.and_then(|number| number.checked_add(1));
            new_no = new_no.and_then(|number| number.checked_add(1));
            index += 1;
            continue;
        }

        let mut deletions = Vec::new();
        let mut additions = Vec::new();
        while let Some(line) = hunk.lines.get(index) {
            match line.kind {
                DiffLineKind::Context => break,
                DiffLineKind::Deletion => {
                    deletions.push(SplitCell {
                        line_no: old_no,
                        kind: line.kind,
                        text: line.text.clone(),
                        tokens: Vec::new(),
                        no_newline_at_end: line.no_newline_at_end,
                    });
                    old_no = old_no.and_then(|number| number.checked_add(1));
                }
                DiffLineKind::Addition => {
                    additions.push(SplitCell {
                        line_no: new_no,
                        kind: line.kind,
                        text: line.text.clone(),
                        tokens: Vec::new(),
                        no_newline_at_end: line.no_newline_at_end,
                    });
                    new_no = new_no.and_then(|number| number.checked_add(1));
                }
            }
            index += 1;
        }
        for pair in 0..deletions.len().max(additions.len()) {
            rows.push(SplitRow {
                old: deletions.get(pair).cloned(),
                new: additions.get(pair).cloned(),
            });
        }
    }
    rows
}

struct CachedPreviewRows {
    rows: Arc<[PreviewRow]>,
}

#[derive(Default)]
pub struct DiffPanel {
    diffs: HashMap<(RepoKey, bool), Arc<DiffInfo>>,
    empties: HashMap<(RepoKey, bool), DiffEmpty>,
    selected_file: HashMap<RepoKey, PathBuf>,
    selected_hunk: HashMap<RepoKey, usize>,
    /// Untracked paths per project, synced from the latest `git status`.
    /// `git diff` never contains these, so prune must keep them selected
    /// instead of treating them as vanished (which flickered the preview
    /// tab open then closed).
    untracked: HashMap<RepoKey, HashSet<PathBuf>>,
    show_staged: HashMap<RepoKey, bool>,
    /// View-local preview tab in the main area (M15): which projects have
    /// the diff preview open. Never persisted, never on the wire — the
    /// core tab model stays terminal-only (M13/M17 scope).
    preview_open: HashMap<RepoKey, bool>,
    /// Split/Inline detail mode per project. Split by default (mock);
    /// view-local, never persisted.
    diff_mode: HashMap<RepoKey, DiffMode>,
    /// Flattened presentation rows are immutable and reused across GPUI
    /// frames. Invalidated only when the source side, selection, or mode changes.
    preview_rows: HashMap<(RepoKey, bool, PathBuf, DiffMode), CachedPreviewRows>,
    /// Historical commit selection per project. Present means the preview
    /// shows that immutable comparison instead of the worktree side.
    selected_commit: HashMap<RepoKey, CommitPreviewSel>,
    /// Landed historical patches per project (one preview per project).
    commit_diffs: HashMap<RepoKey, Arc<DiffInfo>>,
    commit_empties: HashMap<RepoKey, DiffEmpty>,
    /// The exact request key that produced the stored historical patch or
    /// error. The tick compares this before reusing anything: commit, base,
    /// paths, roots and context all gate validity.
    commit_keys: HashMap<RepoKey, CommitDiffKey>,
    /// Flattened rows for commit previews, keyed by full commit OID (never
    /// an abbreviation) plus path and mode.
    commit_rows: HashMap<(RepoKey, String, PathBuf, DiffMode), CachedPreviewRows>,
}

impl DiffPanel {
    pub fn diff_for(&self, project: RepoKey, staged: bool) -> Option<&DiffInfo> {
        self.diffs.get(&(project, staged)).map(Arc::as_ref)
    }

    pub fn diff_shared_for(&self, project: RepoKey, staged: bool) -> Option<Arc<DiffInfo>> {
        self.diffs.get(&(project, staged)).cloned()
    }

    pub fn empty_for(&self, project: RepoKey, staged: bool) -> Option<&DiffEmpty> {
        self.empties.get(&(project, staged))
    }

    /// Whether the panel shows the staged (`--cached`) side for a project.
    /// Unstaged by default, every launch (view chrome, never persisted).
    pub fn show_staged(&self, project: RepoKey) -> bool {
        self.show_staged.get(&project).copied().unwrap_or(false)
    }

    /// Detail-view mode for a project. Split by default, every launch
    /// (view chrome, never persisted).
    pub fn diff_mode(&self, project: RepoKey) -> DiffMode {
        self.diff_mode.get(&project).copied().unwrap_or_default()
    }

    pub fn set_diff_mode(&mut self, project: RepoKey, mode: DiffMode) {
        self.diff_mode.insert(project.clone(), mode);
        self.preview_rows
            .retain(|(owner, _, _, _), _| *owner != project);
    }

    pub fn set_show_staged(&mut self, project: RepoKey, staged: bool) {
        self.show_staged.insert(project, staged);
    }

    /// Open the main-area preview tab for a project. View-local only:
    /// never persisted, never sent over IPC (core tabs stay terminal).
    pub fn open_preview(&mut self, project: RepoKey) {
        self.preview_open.insert(project, true);
    }

    /// Close the main-area preview tab for a project.
    pub fn close_preview(&mut self, project: RepoKey) {
        self.preview_open.remove(&project);
    }

    /// Whether the main area shows the diff preview for a project.
    pub fn preview_open(&self, project: RepoKey) -> bool {
        self.preview_open.get(&project).copied().unwrap_or(false)
    }

    /// Record a landed refresh: diffs replace any error and vice versa.
    pub fn apply_current_refresh(
        &mut self,
        result: DiffWorkerResult,
        pending: Option<&DiffRequestKey>,
        current: Option<&DiffRequestKey>,
    ) -> bool {
        if pending != Some(&result.key) || current != Some(&result.key) {
            return false;
        }
        self.apply_refresh(result.key.repo, result.key.staged, result.refresh);
        true
    }

    /// Retire both comparisons without losing the user's file/hunk intent.
    pub fn invalidate_data(&mut self, project: RepoKey) {
        self.diffs.retain(|(owner, _), _| *owner != project);
        self.empties.retain(|(owner, _), _| *owner != project);
        self.preview_rows
            .retain(|(owner, _, _, _), _| *owner != project);
        self.commit_diffs.remove(&project);
        self.commit_empties.remove(&project);
        self.commit_keys.remove(&project);
        self.commit_rows
            .retain(|(owner, _, _, _), _| *owner != project);
    }

    /// Includes closed projects, which no longer appear in the workspace.
    pub fn retain_project(&mut self, project: Option<RepoKey>) {
        let keep = |owner: &RepoKey| {
            project
                .as_ref()
                .is_some_and(|key| owner.matches_project(key.project))
        };
        self.diffs.retain(|(owner, _), _| keep(owner));
        self.empties.retain(|(owner, _), _| keep(owner));
        self.selected_file.retain(|owner, _| keep(owner));
        self.untracked.retain(|owner, _| keep(owner));
        self.selected_hunk.retain(|owner, _| keep(owner));
        self.show_staged.retain(|owner, _| keep(owner));
        self.preview_open.retain(|owner, _| keep(owner));
        self.diff_mode.retain(|owner, _| keep(owner));
        self.preview_rows.retain(|(owner, _, _, _), _| keep(owner));
        self.selected_commit.retain(|owner, _| keep(owner));
        self.commit_diffs.retain(|owner, _| keep(owner));
        self.commit_empties.retain(|owner, _| keep(owner));
        self.commit_keys.retain(|owner, _| keep(owner));
        self.commit_rows.retain(|(owner, _, _, _), _| keep(owner));
    }

    pub fn apply_refresh(&mut self, project: RepoKey, staged: bool, refresh: DiffRefresh) {
        self.preview_rows
            .retain(|(owner, side, _, _), _| *owner != project || *side != staged);
        match refresh.result {
            Ok(info) => {
                self.empties.remove(&(project.clone(), staged));
                self.diffs.insert((project.clone(), staged), Arc::new(info));
                self.prune_selection(project, staged);
            }
            Err(empty) => {
                self.diffs.remove(&(project.clone(), staged));
                self.empties.insert((project.clone(), staged), empty);
                // The errored side is the visible one: its selection
                // no longer names anything real. A historical preview owns
                // the hunk cursor instead, so worktree errors leave it alone.
                if self.show_staged(project.clone()) == staged
                    && !self.selected_commit.contains_key(&project)
                {
                    self.selected_file.remove(&project);
                    self.selected_hunk.remove(&project);
                }
            }
        }
    }

    /// Select a file and reset its hunk cursor. Called by file-row clicks
    /// and by diff-on-select from the Source Control panel. A worktree
    /// selection replaces any historical preview (one preview per project).
    pub fn select_file(&mut self, project: RepoKey, path: PathBuf) {
        self.preview_rows
            .retain(|(owner, _, cached, _), _| *owner != project || cached == &path);
        self.selected_file.insert(project.clone(), path);
        self.selected_hunk.insert(project.clone(), 0);
        self.clear_commit_preview(project);
    }
    /// Select a historical comparison from the Graph. Replaces any worktree
    /// file selection; the tick fetches the patch for exactly this base.
    /// Stored patches belong to the previous selection and are dropped so
    /// the preview shows the new request's loading state immediately.
    pub fn select_commit(&mut self, project: RepoKey, sel: CommitPreviewSel) {
        self.commit_rows.retain(|(owner, commit, cached, _), _| {
            *owner != project || commit != sel.commit.as_str() || cached == &sel.path
        });
        self.selected_file.remove(&project);
        self.selected_commit.insert(project.clone(), sel);
        self.selected_hunk.insert(project.clone(), 0);
        self.commit_diffs.remove(&project);
        self.commit_empties.remove(&project);
        self.commit_keys.remove(&project);
    }
    /// Drop the historical selection and its cached rows/diffs. Called by
    /// worktree selection and preview close paths.
    pub fn clear_commit_preview(&mut self, project: RepoKey) {
        self.selected_commit.remove(&project);
        self.commit_diffs.remove(&project);
        self.commit_empties.remove(&project);
        self.commit_keys.remove(&project);
        self.commit_rows
            .retain(|(owner, _, _, _), _| *owner != project);
    }

    pub fn selected_commit(&self, project: RepoKey) -> Option<&CommitPreviewSel> {
        self.selected_commit.get(&project)
    }

    pub fn commit_diff_for(&self, project: RepoKey) -> Option<&DiffInfo> {
        self.commit_diffs.get(&project).map(Arc::as_ref)
    }

    pub fn commit_shared_for(&self, project: RepoKey) -> Option<Arc<DiffInfo>> {
        self.commit_diffs.get(&project).cloned()
    }

    pub fn commit_empty_for(&self, project: RepoKey) -> Option<&DiffEmpty> {
        self.commit_empties.get(&project)
    }

    /// The request key behind the stored historical patch or error, if any.
    pub fn commit_key_for(&self, project: RepoKey) -> Option<&CommitDiffKey> {
        self.commit_keys.get(&project)
    }

    /// Record a landed historical patch. Empty states replace the diff and
    /// vice versa; unlike worktree sides there is no cross-side pruning.
    /// The caller matches the result key against the pending/current
    /// request first (stale landings drop before reaching this method).
    pub fn apply_commit_refresh(&mut self, project: RepoKey, landed: CommitDiffResult) {
        self.commit_rows
            .retain(|(owner, _, _, _), _| *owner != project);
        self.commit_keys.insert(project.clone(), landed.key);
        match landed.result {
            Ok(info) => {
                self.commit_empties.remove(&project);
                self.commit_diffs.insert(project.clone(), Arc::new(info));
                let count = self.commit_hunk_count_for(project.clone());
                if self.selected_hunk(project.clone()) >= count.max(1) {
                    self.selected_hunk.insert(project, 0);
                }
            }
            Err(empty) => {
                self.commit_diffs.remove(&project);
                self.commit_empties.insert(project, empty);
            }
        }
    }

    /// Parsed hunk count for the selected historical file.
    pub fn commit_hunk_count_for(&self, project: RepoKey) -> usize {
        let selected = self.selected_commit.get(&project);
        self.commit_diffs
            .get(&project)
            .and_then(|info| {
                selected.and_then(|sel| {
                    info.files
                        .iter()
                        .find(|file| file.path == sel.path)
                        .map(|file| file.hunks.len())
                })
            })
            .unwrap_or(0)
    }

    /// Flattened rows for the selected historical file. Built with
    /// `staged = true` so `Stage Hunk` can never appear: `can_stage_hunk`
    /// requires the worktree side, and every other row is side-agnostic.
    /// Split side headers are labeled by the renderer from the selection.
    pub fn commit_preview_rows_for(&mut self, project: RepoKey) -> Option<Arc<[PreviewRow]>> {
        let sel = self.selected_commit.get(&project)?.clone();
        let mode = self.diff_mode(project.clone());
        let key = (
            project.clone(),
            sel.commit.as_str().to_owned(),
            sel.path.clone(),
            mode,
        );
        if let Some(rows) = self.commit_rows.get(&key) {
            return Some(Arc::clone(&rows.rows));
        }
        let file = self
            .commit_diffs
            .get(&project)?
            .files
            .iter()
            .find(|file| file.path == sel.path)?;
        let rows: Arc<[PreviewRow]> = preview_rows(file, mode, true).into();
        self.commit_rows.insert(
            key,
            CachedPreviewRows {
                rows: Arc::clone(&rows),
            },
        );
        Some(rows)
    }

    pub fn selected_file(&self, project: RepoKey) -> Option<&PathBuf> {
        self.selected_file.get(&project)
    }

    /// Sync the untracked set from the latest status. A stale entry can only
    /// linger until the next status lands; it never dispatches anything.
    pub fn set_untracked(&mut self, project: RepoKey, paths: HashSet<PathBuf>) {
        if paths.is_empty() {
            self.untracked.remove(&project);
        } else {
            self.untracked.insert(project, paths);
        }
    }

    /// Whether `path` is currently untracked for `project`.
    pub fn is_untracked(&self, project: RepoKey, path: &PathBuf) -> bool {
        self.untracked
            .get(&project)
            .is_some_and(|paths| paths.contains(path))
    }

    pub fn selected_hunk(&self, project: RepoKey) -> usize {
        self.selected_hunk.get(&project).copied().unwrap_or(0)
    }

    /// Parsed hunk count for the visible preview: the historical file when
    /// a commit is selected, else the worktree side. Hunk navigation
    /// (Alt+N/P) therefore follows the visible preview without branching
    /// at every call site.
    pub fn hunk_count_for(&self, project: RepoKey) -> usize {
        if self.selected_commit.contains_key(&project) {
            return self.commit_hunk_count_for(project);
        }
        let staged = self.show_staged(project.clone());
        let selected = self.selected_file.get(&project);
        self.diffs
            .get(&(project, staged))
            .and_then(|info| {
                selected.and_then(|path| {
                    info.files
                        .iter()
                        .find(|file| &file.path == path)
                        .map(|file| file.hunks.len())
                })
            })
            .unwrap_or(0)
    }

    pub fn preview_rows_for(
        &mut self,
        project: RepoKey,
        staged: bool,
    ) -> Option<Arc<[PreviewRow]>> {
        let path = self.selected_file.get(&project)?.clone();
        let mode = self.diff_mode(project.clone());
        let key = (project.clone(), staged, path.clone(), mode);
        if let Some(rows) = self.preview_rows.get(&key) {
            return Some(Arc::clone(&rows.rows));
        }
        let file = self
            .diffs
            .get(&(project, staged))?
            .files
            .iter()
            .find(|file| file.path == path)?;
        let rows: Arc<[PreviewRow]> = preview_rows(file, mode, staged).into();
        self.preview_rows.insert(
            key,
            CachedPreviewRows {
                rows: Arc::clone(&rows),
            },
        );
        Some(rows)
    }

    /// Advance the hunk cursor, wrapping across all parsed hunks so
    /// keyboard navigation never walks off the end. Empty selection is a
    /// no-op. The preview viewport follows the cursor. Returns the new
    /// cursor.
    pub fn next_hunk(&mut self, project: RepoKey) -> usize {
        let count = self.hunk_count_for(project.clone());
        let next = if count == 0 {
            0
        } else {
            (self.selected_hunk(project.clone()) + 1) % count
        };
        self.selected_hunk.insert(project, next);
        next
    }

    /// Move the hunk cursor back, wrapping to the last parsed hunk.
    /// The preview viewport follows the cursor. Returns the new cursor.
    pub fn prev_hunk(&mut self, project: RepoKey) -> usize {
        let count = self.hunk_count_for(project.clone());
        let cursor = self.selected_hunk(project.clone());
        let next = if count == 0 {
            0
        } else {
            cursor.checked_sub(1).unwrap_or(count - 1)
        };
        self.selected_hunk.insert(project, next);
        next
    }

    /// Drop the file selection when it leaves the refreshed side, and
    /// clamp the hunk cursor into the parsed hunk range. Untracked files
    /// never appear in `git diff`, so they are kept selected: the renderer
    /// shows an explicit untracked state instead of closing the preview.
    /// A selected historical preview is immutable: worktree refreshes never
    /// prune its selection or cursor.
    fn prune_selection(&mut self, project: RepoKey, staged: bool) {
        if self.selected_commit.contains_key(&project) {
            return;
        }
        if self.show_staged(project.clone()) != staged {
            return;
        }
        if self
            .selected_file
            .get(&project)
            .is_some_and(|selected| self.is_untracked(project.clone(), selected))
        {
            return;
        }
        let visible = self
            .diffs
            .get(&(project.clone(), staged))
            .and_then(|info| {
                self.selected_file
                    .get(&project)
                    .map(|selected| info.files.iter().any(|file| &file.path == selected))
            })
            .unwrap_or(false);
        if !visible {
            self.selected_file.remove(&project);
            self.selected_hunk.remove(&project);
            return;
        }
        let count = self.hunk_count_for(project.clone());
        if self.selected_hunk(project.clone()) >= count.max(1) {
            self.selected_hunk.insert(project, 0);
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiffRequestKey {
    pub generation: u64,
    pub root_generation: u64,
    pub repo: RepoKey,
    pub path: Option<PathBuf>,
    pub pinned_root: Option<PathBuf>,
    pub active_cwd: Option<PathBuf>,
    pub repo_root: Option<PathBuf>,
    pub staged: bool,
    pub context_lines: u8,
    /// Whether the selected path is untracked: the worker renders
    /// empty → content via `--no-index` instead of the index diff.
    pub untracked: bool,
}

/// Spawn parameters for one selected diff. It is sent through a single worker
/// with one replaceable pending slot rather than one detached thread per query.
pub struct DiffSpawn {
    pub key: DiffRequestKey,
    pub cancelled: Arc<std::sync::atomic::AtomicBool>,
}

#[derive(Debug)]
pub struct DiffWorkerResult {
    pub key: DiffRequestKey,
    pub refresh: DiffRefresh,
}

#[derive(Default)]
struct DiffWorkerState {
    pending: Option<DiffSpawn>,
    active_cancel: Option<Arc<std::sync::atomic::AtomicBool>>,
    result: Option<DiffWorkerResult>,
    shutdown: bool,
}

/// One actual Git worker and one latest-only pending request. Superseding a
/// selection cannot create another process/thread; the active Git operation
/// remains governed by its bounded runner deadline and reaping.
pub struct DiffWorker {
    state: Arc<(Mutex<DiffWorkerState>, Condvar)>,
    thread: Option<std::thread::JoinHandle<()>>,
}

impl DiffWorker {
    pub fn new() -> Self {
        Self::with_runner(run_diff_spawn)
    }

    fn with_runner(mut run: impl FnMut(DiffSpawn) -> DiffWorkerResult + Send + 'static) -> Self {
        let state = Arc::new((Mutex::new(DiffWorkerState::default()), Condvar::new()));
        let worker_state = Arc::clone(&state);
        let thread = std::thread::spawn(move || {
            loop {
                let spawn = {
                    let (lock, ready) = &*worker_state;
                    let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                    while state.pending.is_none() && !state.shutdown {
                        state = ready
                            .wait(state)
                            .unwrap_or_else(|poisoned| poisoned.into_inner());
                    }
                    if state.shutdown {
                        return;
                    }
                    let spawn = state.pending.take().expect("pending diff request exists");
                    state.active_cancel = Some(Arc::clone(&spawn.cancelled));
                    spawn
                };
                let cancel = Arc::clone(&spawn.cancelled);
                let landed = run(spawn);
                {
                    let (lock, _) = &*worker_state;
                    let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                    if state
                        .active_cancel
                        .as_ref()
                        .is_some_and(|active| Arc::ptr_eq(active, &cancel))
                    {
                        state.active_cancel = None;
                    }
                    // Publication and supersession share a lock. A cancelled
                    // request cannot refill the mailbox after cancel/close.
                    if !state.shutdown && !cancel.load(std::sync::atomic::Ordering::Acquire) {
                        state.result = Some(landed);
                    }
                }
            }
        });
        Self {
            state,
            thread: Some(thread),
        }
    }

    pub fn submit(&self, spawn: DiffSpawn) {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if state.shutdown {
            return;
        }
        if let Some(active) = &state.active_cancel {
            active.store(true, std::sync::atomic::Ordering::Release);
        }
        let mut spawn = spawn;
        spawn.cancelled = Arc::new(std::sync::atomic::AtomicBool::new(false));
        state.pending = Some(spawn);
        state.result = None;
        ready.notify_one();
    }

    pub fn cancel(&self) {
        let (lock, _) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if let Some(active) = &state.active_cancel {
            active.store(true, std::sync::atomic::Ordering::Release);
        }
        state.pending = None;
        state.result = None;
    }

    pub fn shutdown(&self) {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        state.shutdown = true;
        if let Some(active) = &state.active_cancel {
            active.store(true, std::sync::atomic::Ordering::Release);
        }
        state.pending = None;
        state.result = None;
        ready.notify_one();
    }

    /// Transfer the join to desktop shutdown's background cleanup thread.
    pub fn take_shutdown_thread(&mut self) -> Option<std::thread::JoinHandle<()>> {
        self.shutdown();
        self.thread.take()
    }

    pub fn take_result(&self) -> Option<DiffWorkerResult> {
        self.state
            .0
            .lock()
            .ok()
            .and_then(|mut state| state.result.take())
    }
}

impl Default for DiffWorker {
    fn default() -> Self {
        Self::new()
    }
}

impl Drop for DiffWorker {
    fn drop(&mut self) {
        self.shutdown();
    }
}

/// Request key for one historical patch fetch. Equality (commit, base,
/// paths, roots) gates admission so a stale landing never replaces the
/// visible preview.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CommitDiffKey {
    pub generation: u64,
    pub repo: RepoKey,
    pub commit: GitObjectId,
    pub base: GitComparisonBase,
    pub old_path: Option<PathBuf>,
    pub path: PathBuf,
    pub pinned_root: Option<PathBuf>,
    pub active_cwd: Option<PathBuf>,
    pub repo_root: Option<PathBuf>,
    pub context_lines: u8,
}

/// Spawn parameters for one historical patch. Sent through a dedicated
/// latest-only worker so a rapid Graph walk cannot stack subprocesses.
pub struct CommitDiffSpawn {
    pub key: CommitDiffKey,
    pub cancelled: Arc<std::sync::atomic::AtomicBool>,
}

/// Outcome of one historical patch fetch.
#[derive(Debug)]
pub struct CommitDiffResult {
    pub key: CommitDiffKey,
    pub worker: ThreadId,
    pub result: Result<DiffInfo, DiffEmpty>,
}

#[derive(Default)]
struct CommitDiffWorkerState {
    pending: Option<CommitDiffSpawn>,
    active_cancel: Option<Arc<std::sync::atomic::AtomicBool>>,
    result: Option<CommitDiffResult>,
    shutdown: bool,
}

/// One actual Git worker and one latest-only pending historical request.
/// Same publication/supersession contract as [`DiffWorker`]: a cancelled
/// request never refills the mailbox. The underlying `git diff` call is
/// bounded by the shared runner deadline; cancellation here drops the
/// landing rather than killing the subprocess mid-read.
pub struct CommitDiffWorker {
    state: Arc<(Mutex<CommitDiffWorkerState>, Condvar)>,
    thread: Option<std::thread::JoinHandle<()>>,
}

impl CommitDiffWorker {
    pub fn new() -> Self {
        let state = Arc::new((Mutex::new(CommitDiffWorkerState::default()), Condvar::new()));
        let worker_state = Arc::clone(&state);
        let thread = std::thread::spawn(move || {
            loop {
                let spawn = {
                    let (lock, ready) = &*worker_state;
                    let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                    while state.pending.is_none() && !state.shutdown {
                        state = ready
                            .wait(state)
                            .unwrap_or_else(|poisoned| poisoned.into_inner());
                    }
                    if state.shutdown {
                        return;
                    }
                    let spawn = state.pending.take().expect("pending commit request exists");
                    state.active_cancel = Some(Arc::clone(&spawn.cancelled));
                    spawn
                };
                let cancel = Arc::clone(&spawn.cancelled);
                let landed = run_commit_spawn(spawn);
                {
                    let (lock, _) = &*worker_state;
                    let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                    if state
                        .active_cancel
                        .as_ref()
                        .is_some_and(|active| Arc::ptr_eq(active, &cancel))
                    {
                        state.active_cancel = None;
                    }
                    // Publication and supersession share a lock. A cancelled
                    // request cannot refill the mailbox after cancel/close.
                    if !state.shutdown && !cancel.load(std::sync::atomic::Ordering::Acquire) {
                        state.result = Some(landed);
                    }
                }
            }
        });
        Self {
            state,
            thread: Some(thread),
        }
    }

    pub fn submit(&self, spawn: CommitDiffSpawn) {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if state.shutdown {
            return;
        }
        if let Some(active) = &state.active_cancel {
            active.store(true, std::sync::atomic::Ordering::Release);
        }
        let mut spawn = spawn;
        spawn.cancelled = Arc::new(std::sync::atomic::AtomicBool::new(false));
        state.pending = Some(spawn);
        state.result = None;
        ready.notify_one();
    }

    pub fn cancel(&self) {
        let (lock, _) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if let Some(active) = &state.active_cancel {
            active.store(true, std::sync::atomic::Ordering::Release);
        }
        state.pending = None;
        state.result = None;
    }

    pub fn shutdown(&self) {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        state.shutdown = true;
        if let Some(active) = &state.active_cancel {
            active.store(true, std::sync::atomic::Ordering::Release);
        }
        state.pending = None;
        state.result = None;
        ready.notify_one();
    }

    /// Transfer the join to desktop shutdown's background cleanup thread.
    pub fn take_shutdown_thread(&mut self) -> Option<std::thread::JoinHandle<()>> {
        self.shutdown();
        self.thread.take()
    }

    pub fn take_result(&self) -> Option<CommitDiffResult> {
        self.state
            .0
            .lock()
            .ok()
            .and_then(|mut state| state.result.take())
    }
}

impl Default for CommitDiffWorker {
    fn default() -> Self {
        Self::new()
    }
}

impl Drop for CommitDiffWorker {
    fn drop(&mut self) {
        self.shutdown();
    }
}

fn run_commit_spawn(spawn: CommitDiffSpawn) -> CommitDiffResult {
    let worker = std::thread::current().id();
    let cancelled = Arc::clone(&spawn.cancelled);
    let result = commit_diff_off_thread(&spawn.key, &cancelled);
    CommitDiffResult {
        key: spawn.key,
        worker,
        result,
    }
}

fn commit_diff_off_thread(
    key: &CommitDiffKey,
    cancelled: &std::sync::atomic::AtomicBool,
) -> Result<DiffInfo, DiffEmpty> {
    if cancelled.load(std::sync::atomic::Ordering::Acquire) {
        return Err(DiffEmpty::Cancelled);
    }
    let explicit = key.repo_root.as_deref().filter(|dir| dir.is_dir());
    let resolved = match explicit {
        Some(dir) => omaterm_context::resolve_root(Some(dir), None),
        None => {
            omaterm_context::resolve_root(key.pinned_root.as_deref(), key.active_cwd.as_deref())
        }
    };
    if cancelled.load(std::sync::atomic::Ordering::Acquire) {
        return Err(DiffEmpty::Cancelled);
    }
    let Some(root) = resolved.root else {
        return Err(DiffEmpty::NoRoot);
    };
    match omaterm_context::git_commit_diff(
        &root,
        &key.commit,
        &key.base,
        key.old_path.as_deref(),
        &key.path,
        key.context_lines,
    ) {
        Ok(mut info) => {
            info.staged = false;
            Ok(info)
        }
        Err(omaterm_context::GitError::NotARepo) => Err(DiffEmpty::NotRepo),
        Err(omaterm_context::GitError::GitUnavailable(message)) => {
            Err(DiffEmpty::Unavailable(message))
        }
        Err(omaterm_context::GitError::Timeout) => {
            Err(DiffEmpty::Failed("historical diff timed out".into()))
        }
        Err(omaterm_context::GitError::Cancelled) => Err(DiffEmpty::Cancelled),
        Err(omaterm_context::GitError::GitFailed(message)) => Err(DiffEmpty::Failed(message)),
        Err(omaterm_context::GitError::PathOutsideRoot) => {
            Err(DiffEmpty::Failed("path escapes the project root".into()))
        }
        // Unreachable on the read-only commit path (kept for exhaustiveness).
        Err(
            omaterm_context::GitError::DirtyWorktree
            | omaterm_context::GitError::CurrentBranch
            | omaterm_context::GitError::AuthFailed
            | omaterm_context::GitError::Offline
            | omaterm_context::GitError::Diverged
            | omaterm_context::GitError::NonFastForward
            | omaterm_context::GitError::NoUpstream,
        ) => Err(DiffEmpty::Failed("branch state blocks this diff".into())),
        Err(omaterm_context::GitError::Io(error)) => Err(DiffEmpty::Failed(error.to_string())),
    }
}

/// One-off worker entry retained for focused tests.
#[cfg(test)]
pub fn spawn_diff_thread(
    caller: ThreadId,
    spawn: DiffSpawn,
    tx: std::sync::mpsc::Sender<DiffWorkerResult>,
) {
    std::thread::spawn(move || {
        debug_assert_ne!(std::thread::current().id(), caller);
        let _ = tx.send(run_diff_spawn(spawn));
    });
}

fn run_diff_spawn(spawn: DiffSpawn) -> DiffWorkerResult {
    let worker = std::thread::current().id();
    let result = refresh_off_thread(&spawn.key, &spawn.cancelled);
    DiffWorkerResult {
        key: spawn.key,
        refresh: DiffRefresh { worker, result },
    }
}

fn refresh_off_thread(
    key: &DiffRequestKey,
    cancelled: &std::sync::atomic::AtomicBool,
) -> Result<DiffInfo, DiffEmpty> {
    let explicit = key.repo_root.as_deref().filter(|dir| dir.is_dir());
    let resolved = match explicit {
        Some(dir) => omaterm_context::resolve_root(Some(dir), None),
        None => {
            omaterm_context::resolve_root(key.pinned_root.as_deref(), key.active_cwd.as_deref())
        }
    };
    if cancelled.load(std::sync::atomic::Ordering::Acquire) {
        return Err(DiffEmpty::Cancelled);
    }
    let Some(root) = resolved.root else {
        return Err(DiffEmpty::NoRoot);
    };
    let request = omaterm_context::DiffRequest {
        staged: key.staged,
        path: key.path.clone(),
        context_lines: key.context_lines,
        files_only: false,
        untracked: key.untracked,
    };
    match omaterm_context::git_diff_cancellable(&root, &request, cancelled) {
        Ok(info) => Ok(info),
        Err(omaterm_context::GitError::NotARepo) => Err(DiffEmpty::NotRepo),
        Err(omaterm_context::GitError::GitUnavailable(message)) => {
            Err(DiffEmpty::Unavailable(message))
        }
        Err(omaterm_context::GitError::Timeout) => {
            Err(DiffEmpty::Failed("git diff timed out".into()))
        }
        Err(omaterm_context::GitError::Cancelled) => Err(DiffEmpty::Cancelled),
        Err(omaterm_context::GitError::GitFailed(message)) => Err(DiffEmpty::Failed(message)),
        Err(omaterm_context::GitError::PathOutsideRoot) => {
            Err(DiffEmpty::Failed("path escapes the project root".into()))
        }
        // Unreachable on the read-only worktree diff path (kept for
        // exhaustiveness).
        Err(
            omaterm_context::GitError::DirtyWorktree
            | omaterm_context::GitError::CurrentBranch
            | omaterm_context::GitError::AuthFailed
            | omaterm_context::GitError::Offline
            | omaterm_context::GitError::Diverged
            | omaterm_context::GitError::NonFastForward
            | omaterm_context::GitError::NoUpstream,
        ) => Err(DiffEmpty::Failed("branch state blocks this diff".into())),
        Err(omaterm_context::GitError::Io(error)) => Err(DiffEmpty::Failed(error.to_string())),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use omaterm_core::{
        DiffFileInfo, DiffFileStatus, DiffHunkInfo, DiffLineInfo, DiffLineKind, ProjectId,
    };

    fn rk(project: ProjectId) -> RepoKey {
        RepoKey::root(project)
    }

    fn hunk(lines: usize) -> DiffHunkInfo {
        DiffHunkInfo {
            id: 0,
            header: format!("@@ -1,{lines} +1,{lines} @@"),
            old_start: 1,
            old_lines: lines as u32,
            new_start: 1,
            new_lines: lines as u32,
            lines: (0..lines)
                .map(|i| DiffLineInfo {
                    kind: DiffLineKind::Context,
                    text: format!("line {i}"),
                    no_newline_at_end: false,
                })
                .collect(),
            truncated: false,
        }
    }

    fn file(path: &str, hunks: usize) -> DiffFileInfo {
        DiffFileInfo {
            path: PathBuf::from(path),
            old_path: None,
            status: DiffFileStatus::Modified,
            binary: false,
            hunks: (0..hunks).map(|_| hunk(2)).collect(),
            hunk_count: hunks,
            truncated: false,
        }
    }

    fn panel_with(project: RepoKey, files: Vec<DiffFileInfo>) -> DiffPanel {
        let mut panel = DiffPanel::default();
        panel.apply_refresh(
            project.clone(),
            false,
            DiffRefresh {
                worker: std::thread::current().id(),
                result: Ok(DiffInfo {
                    files,
                    truncated: false,
                    staged: false,
                }),
            },
        );
        panel
    }

    fn request_key(project: RepoKey, generation: u64) -> DiffRequestKey {
        DiffRequestKey {
            generation,
            root_generation: 1,
            repo: project.clone(),
            path: Some(PathBuf::from("a.txt")),
            pinned_root: Some(PathBuf::from("/repo")),
            active_cwd: None,
            repo_root: None,
            staged: false,
            context_lines: 3,
            untracked: false,
        }
    }

    fn empty_result(key: DiffRequestKey) -> DiffWorkerResult {
        DiffWorkerResult {
            key,
            refresh: DiffRefresh {
                worker: std::thread::current().id(),
                result: Err(DiffEmpty::NoRoot),
            },
        }
    }

    fn commit_key(project: RepoKey, commit: &omaterm_core::GitObjectId) -> CommitDiffKey {
        CommitDiffKey {
            generation: 1,
            repo: project.clone(),
            commit: commit.clone(),
            base: omaterm_core::GitComparisonBase::EmptyTree,
            old_path: None,
            path: PathBuf::from("a.txt"),
            pinned_root: Some(PathBuf::from("/repo")),
            active_cwd: None,
            repo_root: None,
            context_lines: 3,
        }
    }

    #[test]
    fn stale_completions_cannot_replace_the_visible_diff() {
        let project = rk(ProjectId::new());
        let mut panel = panel_with(project.clone(), vec![file("a.txt", 1)]);
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        let key = request_key(project.clone(), 1);
        let changed = [
            DiffRequestKey {
                generation: 2,
                ..key.clone()
            },
            DiffRequestKey {
                root_generation: 2,
                ..key.clone()
            },
            DiffRequestKey {
                repo: rk(ProjectId::new()),
                ..key.clone()
            },
            DiffRequestKey {
                path: Some(PathBuf::from("b.txt")),
                ..key.clone()
            },
            DiffRequestKey {
                pinned_root: Some(PathBuf::from("/other")),
                ..key.clone()
            },
            DiffRequestKey {
                active_cwd: Some(PathBuf::from("/other")),
                ..key.clone()
            },
            DiffRequestKey {
                staged: true,
                ..key.clone()
            },
            DiffRequestKey {
                context_lines: 0,
                ..key.clone()
            },
            DiffRequestKey {
                untracked: true,
                ..key.clone()
            },
        ];
        for current in &changed {
            assert!(!panel.apply_current_refresh(
                empty_result(key.clone()),
                Some(&key),
                Some(current)
            ));
            assert!(panel.diff_for(project.clone(), false).is_some());
        }
        assert!(!panel.apply_current_refresh(empty_result(key.clone()), Some(&key), None));
        assert!(!panel.apply_current_refresh(empty_result(key.clone()), None, Some(&key)));
        assert!(panel.apply_current_refresh(empty_result(key.clone()), Some(&key), Some(&key)));
        assert!(panel.diff_for(project.clone(), false).is_none());
        assert_eq!(
            panel.empty_for(project.clone(), false),
            Some(&DiffEmpty::NoRoot)
        );
    }

    #[test]
    fn invalidation_releases_data_but_retains_selection_until_project_retirement() {
        let project = rk(ProjectId::new());
        let mut panel = panel_with(project.clone(), vec![file("a.txt", 3)]);
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        panel.open_preview(project.clone());
        panel.next_hunk(project.clone());
        assert!(panel.preview_rows_for(project.clone(), false).is_some());
        panel.invalidate_data(project.clone());
        assert!(panel.preview_rows_for(project.clone(), false).is_none());
        assert_eq!(
            panel.selected_file(project.clone()),
            Some(&PathBuf::from("a.txt"))
        );
        assert_eq!(panel.selected_hunk(project.clone()), 1);
        assert!(panel.preview_open(project.clone()));
        panel.retain_project(Some(rk(ProjectId::new())));
        assert!(panel.selected_file(project.clone()).is_none());
        assert!(!panel.preview_open(project.clone()));
    }

    #[test]
    fn commit_selection_replaces_worktree_and_hides_stage_actions() {
        use omaterm_core::{GitComparisonBase, GitObjectId};
        let project = rk(ProjectId::new());
        let mut panel = panel_with(project.clone(), vec![file("a.txt", 1)]);
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        assert!(panel.selected_commit(project.clone()).is_none());
        assert!(panel.commit_preview_rows_for(project.clone()).is_none());

        // The worktree side offers hunk staging; the historical rows never do.
        let worktree_rows = panel
            .preview_rows_for(project.clone(), false)
            .expect("worktree rows");
        assert!(worktree_rows.iter().any(|row| matches!(
            row,
            PreviewRow::HunkActions {
                can_stage: true,
                ..
            }
        )));

        let commit = GitObjectId::parse("b".repeat(40)).expect("fixture oid");
        panel.select_commit(
            project.clone(),
            CommitPreviewSel {
                commit: commit.clone(),
                base: GitComparisonBase::EmptyTree,
                old_path: None,
                path: PathBuf::from("a.txt"),
                subject: "second".into(),
                author_name: "Ada".into(),
                body: None,
                parents: Vec::new(),
            },
        );
        assert!(panel.selected_commit(project.clone()).is_some());
        assert!(panel.selected_file(project.clone()).is_none());
        let sel = panel
            .selected_commit(project.clone())
            .expect("commit selection");
        assert!(!sel.source().capabilities().stage_hunk);

        panel.apply_commit_refresh(
            project.clone(),
            CommitDiffResult {
                key: commit_key(project.clone(), &commit),
                worker: std::thread::current().id(),
                result: Ok(omaterm_core::DiffInfo {
                    files: vec![file("a.txt", 2)],
                    truncated: false,
                    staged: false,
                }),
            },
        );
        assert_eq!(panel.commit_hunk_count_for(project.clone()), 2);
        let rows = panel
            .commit_preview_rows_for(project.clone())
            .expect("commit rows");
        assert!(
            rows.iter()
                .any(|row| matches!(row, PreviewRow::HunkActions { .. }))
        );
        assert!(rows.iter().all(|row| !matches!(
            row,
            PreviewRow::HunkActions {
                can_stage: true,
                ..
            }
        )));

        // Selecting a worktree file replaces the historical preview.
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        assert!(panel.selected_commit(project.clone()).is_none());
        assert!(panel.commit_preview_rows_for(project.clone()).is_none());

        // Project retirement drops the commit slot with everything else.
        panel.select_commit(
            project.clone(),
            CommitPreviewSel {
                commit,
                base: GitComparisonBase::EmptyTree,
                old_path: None,
                path: PathBuf::from("a.txt"),
                subject: "second".into(),
                author_name: "Ada".into(),
                body: None,
                parents: Vec::new(),
            },
        );
        panel.retain_project(Some(rk(ProjectId::new())));
        assert!(panel.selected_commit(project.clone()).is_none());
    }

    /// The historical fetch runs off the caller thread and reads committed
    /// content, never the dirty worktree.
    #[test]
    fn commit_worker_runs_off_the_caller_thread() {
        use omaterm_core::{GitComparisonBase, GitObjectId};
        use std::time::{Duration, Instant};
        let caller = std::thread::current().id();
        let repo: PathBuf =
            std::env::temp_dir().join(format!("omaterm-commit-diff-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&repo);
        std::fs::create_dir_all(&repo).unwrap();
        let git = |args: &[&str]| {
            assert!(
                std::process::Command::new("git")
                    .args(args)
                    .current_dir(&repo)
                    .env("GIT_TERMINAL_PROMPT", "0")
                    .env("GIT_CONFIG_NOSYSTEM", "1")
                    .stdin(std::process::Stdio::null())
                    .stdout(std::process::Stdio::null())
                    .stderr(std::process::Stdio::null())
                    .status()
                    .expect("git must spawn")
                    .success()
            );
        };
        git(&["init"]);
        git(&["config", "user.email", "history@test"]);
        git(&["config", "user.name", "history"]);
        git(&["config", "commit.gpgsign", "false"]);
        std::fs::write(repo.join("a.txt"), b"committed one\n").unwrap();
        git(&["add", "-A"]);
        git(&["commit", "-qm", "first"]);
        std::fs::write(repo.join("a.txt"), b"committed two\n").unwrap();
        git(&["add", "-A"]);
        git(&["commit", "-qm", "second"]);
        // Dirty the worktree; the historical patch must ignore it entirely.
        std::fs::write(repo.join("a.txt"), b"uncommitted scratch\n").unwrap();

        let head =
            omaterm_context::git_history(&repo, omaterm_core::GitHistoryScope::CurrentHead, 1)
                .expect("history page")
                .commits
                .pop()
                .expect("head commit");
        let base = GitComparisonBase::Parent(head.parents[0].clone());
        let project = rk(ProjectId::new());
        let worker = CommitDiffWorker::new();
        worker.submit(CommitDiffSpawn {
            key: CommitDiffKey {
                generation: 1,
                repo: project.clone(),
                commit: head.id.clone(),
                base,
                old_path: None,
                path: PathBuf::from("a.txt"),
                pinned_root: Some(repo.clone()),
                active_cwd: None,
                repo_root: None,
                context_lines: 3,
            },
            cancelled: Arc::new(std::sync::atomic::AtomicBool::new(false)),
        });
        let deadline = Instant::now() + Duration::from_secs(30);
        let landed = loop {
            if let Some(result) = worker.take_result() {
                break result;
            }
            assert!(Instant::now() < deadline, "worker must answer");
            std::thread::sleep(Duration::from_millis(5));
        };
        assert_ne!(landed.worker, caller, "git must run off the caller thread");
        let info = landed.result.expect("historical patch");
        assert_eq!(info.files.len(), 1);
        let body: String = info.files[0]
            .hunks
            .iter()
            .flat_map(|hunk| hunk.lines.iter().map(|line| line.text.clone()))
            .collect();
        assert!(body.contains("committed two"), "patch body: {body:?}");
        assert!(!body.contains("uncommitted"), "patch body: {body:?}");

        // Cancelling drops the landing instead of publishing it.
        worker.submit(CommitDiffSpawn {
            key: CommitDiffKey {
                generation: 2,
                repo: project.clone(),
                commit: GitObjectId::parse("f".repeat(40)).expect("fixture oid"),
                base: GitComparisonBase::EmptyTree,
                old_path: None,
                path: PathBuf::from("a.txt"),
                pinned_root: Some(repo.clone()),
                active_cwd: None,
                repo_root: None,
                context_lines: 3,
            },
            cancelled: Arc::new(std::sync::atomic::AtomicBool::new(false)),
        });
        worker.cancel();
        std::thread::sleep(Duration::from_millis(50));
        assert!(worker.take_result().is_none());

        let _ = std::fs::remove_dir_all(&repo);
    }

    #[test]
    fn supersession_retires_active_work_before_starting_only_the_latest_pending_job() {
        use std::sync::atomic::Ordering;
        use std::time::Duration;
        let (started_tx, started_rx) = std::sync::mpsc::channel();
        let (release_tx, release_rx) = std::sync::mpsc::channel();
        let mut worker = DiffWorker::with_runner(move |spawn| {
            started_tx
                .send((spawn.key.generation, Arc::clone(&spawn.cancelled)))
                .unwrap();
            release_rx.recv_timeout(Duration::from_secs(5)).unwrap();
            empty_result(spawn.key)
        });
        let project = rk(ProjectId::new());
        let submit = |generation| {
            worker.submit(DiffSpawn {
                key: request_key(project.clone(), generation),
                cancelled: Arc::new(std::sync::atomic::AtomicBool::new(false)),
            })
        };
        submit(0);
        let (generation, cancel) = started_rx.recv_timeout(Duration::from_secs(5)).unwrap();
        assert_eq!(generation, 0);
        for generation in 1..=100 {
            submit(generation);
        }
        assert!(cancel.load(Ordering::Acquire));
        assert!(started_rx.try_recv().is_err());
        release_tx.send(()).unwrap();
        let (generation, _) = started_rx.recv_timeout(Duration::from_secs(5)).unwrap();
        assert_eq!(generation, 100);
        assert!(worker.take_result().is_none());
        // Close with the latest job active and another request queued.
        submit(101);
        let thread = worker.take_shutdown_thread().unwrap();
        release_tx.send(()).unwrap();
        thread.join().unwrap();
        assert!(worker.take_result().is_none());
        assert!(started_rx.try_recv().is_err());
    }

    #[test]
    fn align_hunk_pairs_context_and_splits_add_delete() {
        use omaterm_core::{DiffLineInfo, DiffLineKind};
        let hunk = DiffHunkInfo {
            id: 0,
            header: "@@ -10,3 +20,3 @@".into(),
            old_start: 10,
            old_lines: 3,
            new_start: 20,
            new_lines: 3,
            lines: vec![
                DiffLineInfo {
                    kind: DiffLineKind::Context,
                    text: "keep".into(),
                    no_newline_at_end: false,
                },
                DiffLineInfo {
                    kind: DiffLineKind::Deletion,
                    text: "old".into(),
                    no_newline_at_end: false,
                },
                DiffLineInfo {
                    kind: DiffLineKind::Addition,
                    text: "new".into(),
                    no_newline_at_end: false,
                },
            ],
            truncated: false,
        };
        let rows = align_hunk(&hunk);
        assert_eq!(rows.len(), 3);
        assert_eq!(rows[0].old_no, Some(10));
        assert_eq!(rows[0].new_no, Some(20));
        assert_eq!(rows[0].kind, DiffLineKind::Context);
        assert_eq!(rows[1].old_no, Some(11));
        assert_eq!(rows[1].new_no, None);
        assert_eq!(rows[2].old_no, None);
        assert_eq!(rows[2].new_no, Some(21));
        assert_eq!(rows[2].text, "new");
    }

    #[test]
    fn split_hunk_pairs_replacements_and_preserves_unmatched_edits() {
        let hunk = DiffHunkInfo {
            id: 0,
            header: "@@ -10,4 +20,5 @@".into(),
            old_start: 10,
            old_lines: 4,
            new_start: 20,
            new_lines: 5,
            lines: vec![
                DiffLineInfo {
                    kind: DiffLineKind::Context,
                    text: "keep before".into(),
                    no_newline_at_end: false,
                },
                DiffLineInfo {
                    kind: DiffLineKind::Deletion,
                    text: "old first".into(),
                    no_newline_at_end: false,
                },
                DiffLineInfo {
                    kind: DiffLineKind::Deletion,
                    text: "old second".into(),
                    no_newline_at_end: false,
                },
                DiffLineInfo {
                    kind: DiffLineKind::Addition,
                    text: "new first".into(),
                    no_newline_at_end: false,
                },
                DiffLineInfo {
                    kind: DiffLineKind::Addition,
                    text: "new second".into(),
                    no_newline_at_end: false,
                },
                DiffLineInfo {
                    kind: DiffLineKind::Addition,
                    text: "new third".into(),
                    no_newline_at_end: false,
                },
                DiffLineInfo {
                    kind: DiffLineKind::Context,
                    text: "keep after".into(),
                    no_newline_at_end: false,
                },
            ],
            truncated: false,
        };

        let rows = split_hunk(&hunk);
        assert_eq!(rows.len(), 5);
        assert_eq!(
            rows[0].old.as_ref().map(|cell| cell.line_no),
            Some(Some(10))
        );
        assert_eq!(
            rows[0].new.as_ref().map(|cell| cell.line_no),
            Some(Some(20))
        );
        assert_eq!(
            rows[1].old.as_ref().map(|cell| cell.text.as_str()),
            Some("old first")
        );
        assert_eq!(
            rows[1].new.as_ref().map(|cell| cell.text.as_str()),
            Some("new first")
        );
        assert_eq!(
            rows[2].old.as_ref().map(|cell| cell.text.as_str()),
            Some("old second")
        );
        assert_eq!(
            rows[2].new.as_ref().map(|cell| cell.text.as_str()),
            Some("new second")
        );
        assert!(rows[3].old.is_none());
        assert_eq!(
            rows[3].new.as_ref().map(|cell| cell.text.as_str()),
            Some("new third")
        );
        assert_eq!(
            rows[4].old.as_ref().map(|cell| cell.line_no),
            Some(Some(13))
        );
        assert_eq!(
            rows[4].new.as_ref().map(|cell| cell.line_no),
            Some(Some(24))
        );
    }

    #[test]
    fn preview_rows_preserve_all_hunks_and_long_hunk_lines() {
        let mut long = hunk(300);
        long.id = 41;
        long.lines[0].no_newline_at_end = true;
        let mut second = hunk(1);
        second.id = 42;
        let file = DiffFileInfo {
            path: PathBuf::from("long.rs"),
            old_path: None,
            status: DiffFileStatus::Modified,
            binary: false,
            hunks: vec![long, second],
            hunk_count: 2,
            truncated: false,
        };
        let rows = preview_rows(&file, DiffMode::Inline, false);
        assert_eq!(rows.len(), 1 + 1 + 300 + 1 + 1 + 1 + 1);
        assert_eq!(hunk_row_index(&rows, 0), Some(0));
        assert_eq!(hunk_row_index(&rows, 1), Some(303));
        assert!(matches!(rows[2], PreviewRow::Inline(_)));
        assert_eq!(
            rows[3],
            PreviewRow::NoNewline {
                old: true,
                new: true
            }
        );
        assert!(matches!(rows[303], PreviewRow::HunkHeader { hunk: 1, .. }));
        assert!(matches!(rows.last(), Some(PreviewRow::Inline(_))));
    }

    fn code_hunk() -> DiffHunkInfo {
        DiffHunkInfo {
            id: 7,
            header: "@@ -1,2 +1,2 @@".into(),
            old_start: 1,
            old_lines: 2,
            new_start: 1,
            new_lines: 2,
            lines: vec![
                DiffLineInfo {
                    kind: DiffLineKind::Deletion,
                    text: "let old = 1; // gone".into(),
                    no_newline_at_end: false,
                },
                DiffLineInfo {
                    kind: DiffLineKind::Addition,
                    text: "let在他 = \"x\"; // 界".into(),
                    no_newline_at_end: false,
                },
            ],
            truncated: false,
        }
    }

    fn spans_valid(text: &str, spans: &[TokenSpan]) {
        let mut end = 0;
        for span in spans {
            assert!(span.start >= end, "overlap at {span:?}");
            assert!(text.is_char_boundary(span.start));
            assert!(text.is_char_boundary(span.start + span.len));
            assert!(span.start + span.len <= text.len());
            end = span.start + span.len;
        }
    }

    #[test]
    fn preview_rows_highlight_code_lines_by_file_language() {
        let mut file = file("src/main.rs", 0);
        file.hunks = vec![code_hunk()];
        file.hunk_count = 1;
        for mode in [DiffMode::Inline, DiffMode::Split] {
            let rows = preview_rows(&file, mode, false);
            // Inline yields one row per hunk line; Split pairs the
            // deletion/addition run into a single row with two cells.
            let cells: Vec<(&str, &[TokenSpan])> = rows
                .iter()
                .flat_map(|row| match row {
                    PreviewRow::Inline(line) => {
                        vec![(line.text.as_str(), line.tokens.as_slice())]
                    }
                    PreviewRow::Split(split) => split
                        .old
                        .iter()
                        .chain(split.new.iter())
                        .map(|cell| (cell.text.as_str(), cell.tokens.as_slice()))
                        .collect(),
                    _ => Vec::new(),
                })
                .collect();
            assert_eq!(cells.len(), 2, "{mode:?}");
            for (text, tokens) in cells {
                assert!(!tokens.is_empty(), "{mode:?} {text:?} has no spans");
                assert!(
                    tokens
                        .iter()
                        .any(|span| span.kind == crate::editor::TokenKind::Keyword
                            && &text[span.start..span.start + span.len] == "let"),
                    "{mode:?} {text:?} missing `let` keyword"
                );
                spans_valid(text, tokens);
            }
        }
    }

    #[test]
    fn preview_rows_stay_plain_for_binary_unknown_and_tabbed_lines() {
        // Unknown extension: no spans.
        let mut plain = file("notes.txt", 0);
        plain.hunks = vec![code_hunk()];
        plain.hunk_count = 1;
        let rows = preview_rows(&plain, DiffMode::Inline, false);
        assert!(
            rows.iter()
                .filter_map(|row| match row {
                    PreviewRow::Inline(line) => Some(line),
                    _ => None,
                })
                .all(|line| line.tokens.is_empty())
        );
        // Binary: no spans even for a highlightable extension.
        let mut binary = file("src/main.rs", 0);
        binary.binary = true;
        binary.hunks = vec![code_hunk()];
        binary.hunk_count = 1;
        let rows = preview_rows(&binary, DiffMode::Inline, false);
        assert!(
            rows.iter()
                .filter_map(|row| match row {
                    PreviewRow::Inline(line) => Some(line),
                    _ => None,
                })
                .all(|line| line.tokens.is_empty())
        );
        // Tabs expand before tokenizing: offsets address the final text.
        let mut tabbed = file("run.sh", 0);
        tabbed.hunks = vec![DiffHunkInfo {
            id: 8,
            header: "@@ -1,1 +1,1 @@".into(),
            old_start: 1,
            old_lines: 1,
            new_start: 1,
            new_lines: 1,
            lines: vec![DiffLineInfo {
                kind: DiffLineKind::Addition,
                text: "\t# hi".into(),
                no_newline_at_end: false,
            }],
            truncated: false,
        }];
        tabbed.hunk_count = 1;
        let rows = preview_rows(&tabbed, DiffMode::Inline, false);
        let line = rows
            .iter()
            .find_map(|row| match row {
                PreviewRow::Inline(line) => Some(line),
                _ => None,
            })
            .expect("one inline row");
        assert_eq!(line.text, "    # hi");
        assert_eq!(line.tokens.len(), 1);
        assert_eq!(
            &line.text[line.tokens[0].start..line.tokens[0].start + line.tokens[0].len],
            "# hi"
        );
        spans_valid(&line.text, &line.tokens);
    }

    #[test]
    fn hunk_stage_eligibility_requires_current_complete_unstaged_text() {
        let file = file("a.rs", 1);
        let hunk = &file.hunks[0];
        assert!(can_stage_hunk(&file, hunk, false));
        assert!(!can_stage_hunk(&file, hunk, true));
        let mut truncated_file = file.clone();
        truncated_file.truncated = true;
        assert!(!can_stage_hunk(&truncated_file, hunk, false));
        let mut truncated_hunk = hunk.clone();
        truncated_hunk.truncated = true;
        assert!(!can_stage_hunk(&file, &truncated_hunk, false));
        let mut binary = file.clone();
        binary.binary = true;
        assert!(!can_stage_hunk(&binary, hunk, false));
    }

    #[test]
    fn copy_text_preserves_original_header_body_and_no_newline_marker() {
        let mut hunk = hunk(2);
        hunk.header = "@@ -4,2 +4,2 @@ impl Example".into();
        hunk.lines[0] = DiffLineInfo {
            kind: DiffLineKind::Deletion,
            text: "old".into(),
            no_newline_at_end: true,
        };
        hunk.lines[1] = DiffLineInfo {
            kind: DiffLineKind::Addition,
            text: "new".into(),
            no_newline_at_end: false,
        };
        assert_eq!(
            unified_hunk_text(&hunk),
            "@@ -4,2 +4,2 @@ impl Example\n-old\n\\ No newline at end of file\n+new\n"
        );
    }

    #[test]
    fn tab_expansion_is_presentation_only_and_preserves_unicode_and_copy_bytes() {
        let mut file = file("tabs.rs", 1);
        file.hunks[0].lines[0].text = "\t界e\u{301}\tend".into();
        let original = unified_hunk_text(&file.hunks[0]);
        for mode in [DiffMode::Inline, DiffMode::Split] {
            let rows = preview_rows(&file, mode, false);
            match &rows[2] {
                PreviewRow::Inline(line) => assert_eq!(line.text, "    界e\u{301}    end"),
                PreviewRow::Split(line) => {
                    assert_eq!(line.old.as_ref().unwrap().text, "    界e\u{301}    end");
                    assert_eq!(line.new.as_ref().unwrap().text, "    界e\u{301}    end");
                }
                _ => panic!("expected code row"),
            }
        }
        assert_eq!(unified_hunk_text(&file.hunks[0]), original);
        assert!(original.contains("\t界e\u{301}\tend"));
    }

    #[test]
    fn mode_and_refresh_anchors_preserve_source_line_and_fractional_offset() {
        let mut file = file("anchor.rs", 1);
        file.hunks[0].id = 42;
        file.hunks[0].lines = vec![
            DiffLineInfo {
                kind: DiffLineKind::Deletion,
                text: "old".into(),
                no_newline_at_end: false,
            },
            DiffLineInfo {
                kind: DiffLineKind::Addition,
                text: "new".into(),
                no_newline_at_end: false,
            },
            DiffLineInfo {
                kind: DiffLineKind::Context,
                text: "tail".into(),
                no_newline_at_end: false,
            },
        ];
        let split = preview_rows(&file, DiffMode::Split, false);
        let inline = preview_rows(&file, DiffMode::Inline, false);
        // Tail is row 3 in Split and row 4 in Inline.
        assert_eq!(remap_preview_offset(&split, &inline, -66.5), -87.5);
        assert_eq!(remap_preview_offset(&inline, &split, -87.5), -66.5);
        // A changed hunk hash does not discard a surviving source-line anchor.
        file.hunks[0].id = 43;
        let refreshed = preview_rows(&file, DiffMode::Inline, false);
        assert_eq!(remap_preview_offset(&split, &refreshed, -66.5), -87.5);
        assert_eq!(remap_preview_offset(&split, &[], -66.5), 0.0);
    }

    #[test]
    fn overflowing_source_line_numbers_are_omitted_not_wrapped() {
        let hunk = DiffHunkInfo {
            id: 0,
            header: "@@ -4294967295,2 +4294967295,2 @@".into(),
            old_start: u32::MAX,
            old_lines: 2,
            new_start: u32::MAX,
            new_lines: 2,
            lines: vec![
                DiffLineInfo {
                    kind: DiffLineKind::Context,
                    text: "last representable".into(),
                    no_newline_at_end: false,
                },
                DiffLineInfo {
                    kind: DiffLineKind::Context,
                    text: "overflow".into(),
                    no_newline_at_end: false,
                },
            ],
            truncated: false,
        };
        let inline = align_hunk(&hunk);
        assert_eq!(inline[0].old_no, Some(u32::MAX));
        assert_eq!(inline[1].old_no, None);
        let split = split_hunk(&hunk);
        assert_eq!(split[0].old.as_ref().unwrap().line_no, Some(u32::MAX));
        assert_eq!(split[1].old.as_ref().unwrap().line_no, None);
    }

    #[test]
    fn diff_mode_defaults_split_and_clears_with_project() {
        let project = rk(ProjectId::new());
        let mut panel = DiffPanel::default();
        assert_eq!(panel.diff_mode(project.clone()), DiffMode::Split);
        panel.set_diff_mode(project.clone(), DiffMode::Inline);
        assert_eq!(panel.diff_mode(project.clone()), DiffMode::Inline);
        panel.retain_project(None);
        assert_eq!(panel.diff_mode(project.clone()), DiffMode::Split);
    }

    #[test]
    fn hunk_cursor_wraps_within_the_rendered_window() {
        let project = rk(ProjectId::new());
        let mut panel = panel_with(project.clone(), vec![file("a.txt", 3)]);
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        assert_eq!(panel.selected_hunk(project.clone()), 0);
        assert_eq!(panel.next_hunk(project.clone()), 1);
        assert_eq!(panel.next_hunk(project.clone()), 2);
        assert_eq!(panel.next_hunk(project.clone()), 0);
        assert_eq!(panel.prev_hunk(project.clone()), 2);
        assert_eq!(panel.prev_hunk(project.clone()), 1);
    }

    #[test]
    fn hunk_cursor_is_a_no_op_without_selection() {
        let project = rk(ProjectId::new());
        let mut panel = panel_with(project.clone(), vec![file("a.txt", 2)]);
        assert_eq!(panel.next_hunk(project.clone()), 0);
        assert_eq!(panel.prev_hunk(project.clone()), 0);
        assert_eq!(panel.hunk_count_for(project.clone()), 0);
    }

    #[test]
    fn select_file_resets_the_hunk_cursor() {
        let project = rk(ProjectId::new());
        let mut panel = panel_with(project.clone(), vec![file("a.txt", 3), file("b.txt", 1)]);
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        panel.next_hunk(project.clone());
        panel.next_hunk(project.clone());
        assert_eq!(panel.selected_hunk(project.clone()), 2);
        panel.select_file(project.clone(), PathBuf::from("b.txt"));
        assert_eq!(panel.selected_hunk(project.clone()), 0);
        assert_eq!(panel.hunk_count_for(project.clone()), 1);
    }

    #[test]
    fn refresh_prunes_gone_files_and_clamps_the_cursor() {
        let project = rk(ProjectId::new());
        let mut panel = panel_with(project.clone(), vec![file("a.txt", 3)]);
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        panel.next_hunk(project.clone());
        // Same file with fewer hunks: cursor clamps back to zero.
        panel.apply_refresh(
            project.clone(),
            false,
            DiffRefresh {
                worker: std::thread::current().id(),
                result: Ok(DiffInfo {
                    files: vec![file("a.txt", 1)],
                    truncated: false,
                    staged: false,
                }),
            },
        );
        assert_eq!(panel.selected_hunk(project.clone()), 0);
        // File gone entirely: selection clears.
        panel.apply_refresh(
            project.clone(),
            false,
            DiffRefresh {
                worker: std::thread::current().id(),
                result: Ok(DiffInfo {
                    files: vec![],
                    truncated: false,
                    staged: false,
                }),
            },
        );
        assert!(panel.selected_file(project.clone()).is_none());
    }

    #[test]
    fn refresh_keeps_untracked_selection_and_prunes_once_tracked() {
        let project = rk(ProjectId::new());
        let mut panel = panel_with(project.clone(), vec![]);
        panel.select_file(project.clone(), PathBuf::from("new.txt"));
        panel.set_untracked(
            project.clone(),
            [PathBuf::from("new.txt")].into_iter().collect(),
        );
        assert!(panel.is_untracked(project.clone(), &PathBuf::from("new.txt")));
        // An empty `git diff` (which never lists untracked files) must not
        // drop the selection: that flickered the preview tab open then shut.
        panel.apply_refresh(
            project.clone(),
            false,
            DiffRefresh {
                worker: std::thread::current().id(),
                result: Ok(DiffInfo {
                    files: vec![],
                    truncated: false,
                    staged: false,
                }),
            },
        );
        assert_eq!(
            panel.selected_file(project.clone()),
            Some(&PathBuf::from("new.txt"))
        );
        // Once the path leaves the untracked set, normal pruning resumes.
        panel.set_untracked(project.clone(), HashSet::new());
        assert!(!panel.is_untracked(project.clone(), &PathBuf::from("new.txt")));
        panel.apply_refresh(
            project.clone(),
            false,
            DiffRefresh {
                worker: std::thread::current().id(),
                result: Ok(DiffInfo {
                    files: vec![],
                    truncated: false,
                    staged: false,
                }),
            },
        );
        assert!(panel.selected_file(project.clone()).is_none());
    }

    #[test]
    fn staged_toggle_defaults_unstaged_and_refreshes_swap_sides() {
        let project = rk(ProjectId::new());
        let mut panel = DiffPanel::default();
        assert!(!panel.show_staged(project.clone()));
        panel.set_show_staged(project.clone(), true);
        assert!(panel.show_staged(project.clone()));
        // A landing refresh for the hidden side never disturbs the
        // visible selection.
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        panel.apply_refresh(
            project.clone(),
            false,
            DiffRefresh {
                worker: std::thread::current().id(),
                result: Ok(DiffInfo {
                    files: vec![],
                    truncated: false,
                    staged: false,
                }),
            },
        );
        assert_eq!(
            panel.selected_file(project.clone()),
            Some(&PathBuf::from("a.txt"))
        );
    }

    #[test]
    fn error_refresh_clears_the_visible_side_only() {
        let project = rk(ProjectId::new());
        let mut panel = panel_with(project.clone(), vec![file("a.txt", 1)]);
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        panel.apply_refresh(
            project.clone(),
            false,
            DiffRefresh {
                worker: std::thread::current().id(),
                result: Err(DiffEmpty::Failed("boom".into())),
            },
        );
        assert!(panel.diff_for(project.clone(), false).is_none());
        assert_eq!(
            panel.empty_for(project.clone(), false),
            Some(&DiffEmpty::Failed("boom".into()))
        );
        assert!(panel.selected_file(project.clone()).is_none());
    }

    #[test]
    fn preview_open_close_is_view_local_per_project() {
        let project = rk(ProjectId::new());
        let other = rk(ProjectId::new());
        let mut panel = DiffPanel::default();
        assert!(!panel.preview_open(project.clone()));
        panel.open_preview(project.clone());
        assert!(panel.preview_open(project.clone()));
        assert!(!panel.preview_open(other));
        panel.close_preview(project.clone());
        assert!(!panel.preview_open(project.clone()));
        // Clearing the project closes its preview and viewport.
        panel.open_preview(project.clone());
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        panel.retain_project(None);
        assert!(!panel.preview_open(project.clone()));
    }

    #[test]
    fn navigation_tracks_all_hunks_and_scroll_range() {
        let project = rk(ProjectId::new());
        // Cursor and scroll range cover every source hunk.
        let mut panel = panel_with(project.clone(), vec![file("a.txt", 10)]);
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
        panel.open_preview(project.clone());
        for _ in 0..8 {
            panel.next_hunk(project.clone());
        }
        assert_eq!(panel.selected_hunk(project.clone()), 8);
        panel.prev_hunk(project.clone());
        panel.prev_hunk(project.clone());
        // Cursor wrap keeps the viewport valid.
        panel.select_file(project.clone(), PathBuf::from("a.txt"));
    }

    #[test]
    fn diff_worker_runs_off_the_calling_thread() {
        let (tx, rx) = std::sync::mpsc::channel();
        let project = rk(ProjectId::new());
        let caller = std::thread::current().id();
        let key = DiffRequestKey {
            generation: 7,
            root_generation: 3,
            repo: project.clone(),
            path: None,
            pinned_root: Some(std::env::temp_dir()),
            active_cwd: None,
            repo_root: None,
            staged: false,
            context_lines: 3,
            untracked: false,
        };
        spawn_diff_thread(
            caller,
            DiffSpawn {
                key: key.clone(),
                cancelled: Arc::new(std::sync::atomic::AtomicBool::new(false)),
            },
            tx,
        );
        let result = rx
            .recv_timeout(std::time::Duration::from_secs(10))
            .expect("diff worker must answer");
        assert_eq!(result.key, key);
        assert_ne!(result.refresh.worker, caller);
        // A plain temp dir resolves (pinned) but is not a repo.
        assert_eq!(result.refresh.result.unwrap_err(), DiffEmpty::NotRepo);
    }

    #[test]
    fn latest_diff_worker_keeps_only_one_pending_request() {
        let caller = std::thread::current().id();
        let worker = DiffWorker::new();
        let project = rk(ProjectId::new());
        let root = std::env::temp_dir();
        for generation in 0..64 {
            let key = DiffRequestKey {
                generation,
                root_generation: generation + 1,
                repo: project.clone(),
                path: Some(PathBuf::from(format!("file-{generation}.rs"))),
                pinned_root: Some(root.clone()),
                active_cwd: Some(root.clone()),
                repo_root: None,
                staged: generation % 2 == 1,
                context_lines: generation as u8,
                untracked: false,
            };
            worker.submit(DiffSpawn {
                key,
                cancelled: Arc::new(std::sync::atomic::AtomicBool::new(false)),
            });
        }
        let expected_latest_key = DiffRequestKey {
            generation: 63,
            root_generation: 64,
            repo: project.clone(),
            path: Some(PathBuf::from("file-63.rs")),
            pinned_root: Some(root.clone()),
            active_cwd: Some(root),
            repo_root: None,
            staged: true,
            context_lines: 63,
            untracked: false,
        };
        let start = std::time::Instant::now();
        loop {
            if let Some(result) = worker.take_result()
                && result.key.generation == 63
            {
                assert_ne!(result.refresh.worker, caller);
                assert_eq!(result.key, expected_latest_key);
                assert_eq!(result.refresh.result.unwrap_err(), DiffEmpty::NotRepo);
                break;
            }
            assert!(start.elapsed() < std::time::Duration::from_secs(5));
            std::thread::sleep(std::time::Duration::from_millis(5));
        }
    }
}
