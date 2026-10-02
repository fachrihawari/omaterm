//! M15 unified-diff panel state: read-only hunk view over `git diff`
//! plus per-hunk file-stage buttons.
//!
//! GPUI-free. Rendering and key/mouse wiring live in `main.rs`, which owns
//! the worker threads: root resolution plus `git diff` run on a background
//! worker spawned through [`spawn_diff_thread`], never on the UI thread
//! (M14 off-thread precedent). This panel only tracks last-good diffs,
//! explicit empty/error states, the selected file/hunk, and the
//! staged/unstaged view toggle. Mutations (hunk stage buttons) go through
//! `GitCommand::Stage` — the same path as the Source Control panel and
//! IPC/CLI (blueprint §62) — and set a refresh hint the poller consumes.
//!
//! Payloads are `omaterm_core::DiffInfo` (root-relative paths, bounded
//! entries, accurate `truncated`). No syntax highlighting in v0.2: the
//! desktop colors `±` lines in plain monospace.

use std::collections::HashMap;
use std::path::PathBuf;
use std::thread::ThreadId;

use omaterm_core::{DiffInfo, ProjectId};

/// Explicit non-data state for a project+side. `NoRoot` (M12 `none`) and
/// `NotRepo` render the empty state, never an error; failures name the
/// cause without paths or contents (blueprint §45).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DiffEmpty {
    NoRoot,
    NotRepo,
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
/// the same bounded unified hunks (no full-file fetch, no syntax
/// highlighting in v0.2).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum DiffMode {
    #[default]
    Split,
    Inline,
}

/// One aligned body row for Split/Inline rendering: exactly one of the
/// line numbers is present on add/delete-only rows.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AlignedRow {
    pub old_no: Option<u32>,
    pub new_no: Option<u32>,
    pub kind: omaterm_core::DiffLineKind,
    pub text: String,
}

/// One present side of a Split diff row. Split presentation deliberately keeps
/// old/new text independent so replacement pairs never repeat one side's text.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SplitCell {
    pub line_no: u32,
    pub kind: omaterm_core::DiffLineKind,
    pub text: String,
}

/// A visual row in a Split diff. Edit runs pair deletions with additions by
/// position; an unmatched edit has one absent side and renders as a spacer.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SplitRow {
    pub old: Option<SplitCell>,
    pub new: Option<SplitCell>,
}

/// Align one hunk's unified lines into old/new rows. Context lines pair
/// both sides; deletions occupy the old side only; additions the new
/// side only. Line numbers derive from the hunk starts; blank spacer
/// rows are the renderer's job (it sees `None` on the missing side).
pub fn align_hunk(hunk: &omaterm_core::DiffHunkInfo) -> Vec<AlignedRow> {
    let mut rows = Vec::with_capacity(hunk.lines.len());
    let mut old_no = hunk.old_start;
    let mut new_no = hunk.new_start;
    for line in &hunk.lines {
        match line.kind {
            omaterm_core::DiffLineKind::Context => {
                rows.push(AlignedRow {
                    old_no: Some(old_no),
                    new_no: Some(new_no),
                    kind: line.kind,
                    text: line.text.clone(),
                });
                old_no += 1;
                new_no += 1;
            }
            omaterm_core::DiffLineKind::Deletion => {
                rows.push(AlignedRow {
                    old_no: Some(old_no),
                    new_no: None,
                    kind: line.kind,
                    text: line.text.clone(),
                });
                old_no += 1;
            }
            omaterm_core::DiffLineKind::Addition => {
                rows.push(AlignedRow {
                    old_no: None,
                    new_no: Some(new_no),
                    kind: line.kind,
                    text: line.text.clone(),
                });
                new_no += 1;
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
    let mut old_no = hunk.old_start;
    let mut new_no = hunk.new_start;
    let mut index = 0;
    while index < hunk.lines.len() {
        let line = &hunk.lines[index];
        if line.kind == DiffLineKind::Context {
            rows.push(SplitRow {
                old: Some(SplitCell {
                    line_no: old_no,
                    kind: line.kind,
                    text: line.text.clone(),
                }),
                new: Some(SplitCell {
                    line_no: new_no,
                    kind: line.kind,
                    text: line.text.clone(),
                }),
            });
            old_no += 1;
            new_no += 1;
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
                    });
                    old_no += 1;
                }
                DiffLineKind::Addition => {
                    additions.push(SplitCell {
                        line_no: new_no,
                        kind: line.kind,
                        text: line.text.clone(),
                    });
                    new_no += 1;
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

/// Hunks rendered per selected file at most; prev/next navigation cycles
/// within the rendered window.
pub const MAX_DIFF_RENDER_HUNKS: usize = 32;
/// Body lines rendered per hunk at most.
pub const MAX_DIFF_RENDER_LINES: usize = 200;
/// Hunks visible at once in the main-area preview tab. The cursor can
/// range over the full render window; this viewport follows it, and the
/// wheel scrolls it. Keeps huge diffs off the GPUI tree (M15 virtualized
/// rendering goal) without per-line geometry math.
pub const MAX_DIFF_PREVIEW_HUNKS: usize = 8;

#[derive(Default)]
pub struct DiffPanel {
    diffs: HashMap<(ProjectId, bool), DiffInfo>,
    empties: HashMap<(ProjectId, bool), DiffEmpty>,
    selected_file: HashMap<ProjectId, PathBuf>,
    selected_hunk: HashMap<ProjectId, usize>,
    show_staged: HashMap<ProjectId, bool>,
    /// View-local preview tab in the main area (M15): which projects have
    /// the diff preview open. Never persisted, never on the wire — the
    /// core tab model stays terminal-only (M13/M17 scope).
    preview_open: HashMap<ProjectId, bool>,
    /// Top hunk of the preview viewport, per project.
    hunk_offset: HashMap<ProjectId, usize>,
    /// Split/Inline detail mode per project. Split by default (mock);
    /// view-local, never persisted.
    diff_mode: HashMap<ProjectId, DiffMode>,
}

impl DiffPanel {
    pub fn diff_for(&self, project: ProjectId, staged: bool) -> Option<&DiffInfo> {
        self.diffs.get(&(project, staged))
    }

    pub fn empty_for(&self, project: ProjectId, staged: bool) -> Option<&DiffEmpty> {
        self.empties.get(&(project, staged))
    }

    /// Whether the panel shows the staged (`--cached`) side for a project.
    /// Unstaged by default, every launch (view chrome, never persisted).
    pub fn show_staged(&self, project: ProjectId) -> bool {
        self.show_staged.get(&project).copied().unwrap_or(false)
    }

    /// Detail-view mode for a project. Split by default, every launch
    /// (view chrome, never persisted).
    pub fn diff_mode(&self, project: ProjectId) -> DiffMode {
        self.diff_mode.get(&project).copied().unwrap_or_default()
    }

    pub fn set_diff_mode(&mut self, project: ProjectId, mode: DiffMode) {
        self.diff_mode.insert(project, mode);
    }

    pub fn set_show_staged(&mut self, project: ProjectId, staged: bool) {
        self.show_staged.insert(project, staged);
    }

    /// Open the main-area preview tab for a project. View-local only:
    /// never persisted, never sent over IPC (core tabs stay terminal).
    pub fn open_preview(&mut self, project: ProjectId) {
        self.preview_open.insert(project, true);
    }

    /// Close the main-area preview tab for a project.
    pub fn close_preview(&mut self, project: ProjectId) {
        self.preview_open.remove(&project);
    }

    /// Whether the main area shows the diff preview for a project.
    pub fn preview_open(&self, project: ProjectId) -> bool {
        self.preview_open.get(&project).copied().unwrap_or(false)
    }

    /// Top hunk of the preview viewport for a project.
    pub fn hunk_offset(&self, project: ProjectId) -> usize {
        self.hunk_offset.get(&project).copied().unwrap_or(0)
    }

    /// Scroll the preview viewport by `steps` hunks (positive scrolls
    /// toward later hunks), clamped so the last hunk can sit at the
    /// viewport bottom. No-op without a selection. Returns the new offset.
    #[cfg(test)]
    pub fn scroll_preview(&mut self, project: ProjectId, steps: i32) -> usize {
        let max = self
            .hunk_count_for(project)
            .saturating_sub(MAX_DIFF_PREVIEW_HUNKS) as i32;
        let next = (self.hunk_offset(project) as i32 + steps).clamp(0, max.max(0)) as usize;
        self.hunk_offset.insert(project, next);
        next
    }

    /// Keep the cursor inside the preview viewport after cursor moves.
    fn ensure_cursor_visible(&mut self, project: ProjectId) {
        let cursor = self.selected_hunk(project);
        let mut offset = self.hunk_offset(project);
        if cursor < offset {
            offset = cursor;
        } else if cursor >= offset + MAX_DIFF_PREVIEW_HUNKS {
            offset = cursor + 1 - MAX_DIFF_PREVIEW_HUNKS;
        }
        let max = self
            .hunk_count_for(project)
            .saturating_sub(MAX_DIFF_PREVIEW_HUNKS);
        self.hunk_offset.insert(project, offset.min(max));
    }

    /// Record a landed refresh: diffs replace any error and vice versa.
    pub fn apply_refresh(&mut self, project: ProjectId, staged: bool, refresh: DiffRefresh) {
        match refresh.result {
            Ok(info) => {
                self.empties.remove(&(project, staged));
                self.diffs.insert((project, staged), info);
                self.prune_selection(project, staged);
            }
            Err(empty) => {
                self.diffs.remove(&(project, staged));
                self.empties.insert((project, staged), empty);
                // The errored side is the visible one: its selection
                // no longer names anything real.
                if self.show_staged(project) == staged {
                    self.selected_file.remove(&project);
                    self.selected_hunk.remove(&project);
                    self.hunk_offset.remove(&project);
                }
            }
        }
    }

    /// Drop cached state for a project (switch-away memory bound is owned
    /// by the caller, like the git panel).
    pub fn clear_project(&mut self, project: ProjectId) {
        self.diffs.remove(&(project, false));
        self.diffs.remove(&(project, true));
        self.empties.remove(&(project, false));
        self.empties.remove(&(project, true));
        self.selected_file.remove(&project);
        self.selected_hunk.remove(&project);
        self.show_staged.remove(&project);
        self.preview_open.remove(&project);
        self.hunk_offset.remove(&project);
        self.diff_mode.remove(&project);
    }

    /// Select a file and reset its hunk cursor. Called by file-row clicks
    /// and by diff-on-select from the Source Control panel.
    pub fn select_file(&mut self, project: ProjectId, path: PathBuf) {
        self.selected_file.insert(project, path);
        self.selected_hunk.insert(project, 0);
        self.hunk_offset.insert(project, 0);
    }

    pub fn selected_file(&self, project: ProjectId) -> Option<&PathBuf> {
        self.selected_file.get(&project)
    }

    pub fn selected_hunk(&self, project: ProjectId) -> usize {
        self.selected_hunk.get(&project).copied().unwrap_or(0)
    }

    /// Renderable hunks for the selected file (bounded window).
    pub fn hunk_count_for(&self, project: ProjectId) -> usize {
        let staged = self.show_staged(project);
        let selected = self.selected_file.get(&project);
        self.diffs
            .get(&(project, staged))
            .and_then(|info| {
                selected.and_then(|path| {
                    info.files
                        .iter()
                        .find(|file| &file.path == path)
                        .map(|file| file.hunks.len().min(MAX_DIFF_RENDER_HUNKS))
                })
            })
            .unwrap_or(0)
    }

    /// Advance the hunk cursor, wrapping within the rendered window so
    /// keyboard navigation never walks off the end. Empty selection is a
    /// no-op. The preview viewport follows the cursor. Returns the new
    /// cursor.
    pub fn next_hunk(&mut self, project: ProjectId) -> usize {
        let count = self.hunk_count_for(project);
        let next = if count == 0 {
            0
        } else {
            (self.selected_hunk(project) + 1) % count
        };
        self.selected_hunk.insert(project, next);
        self.ensure_cursor_visible(project);
        next
    }

    /// Move the hunk cursor back, wrapping to the last rendered hunk.
    /// The preview viewport follows the cursor. Returns the new cursor.
    pub fn prev_hunk(&mut self, project: ProjectId) -> usize {
        let count = self.hunk_count_for(project);
        let cursor = self.selected_hunk(project);
        let next = if count == 0 {
            0
        } else {
            cursor.checked_sub(1).unwrap_or(count - 1)
        };
        self.selected_hunk.insert(project, next);
        self.ensure_cursor_visible(project);
        next
    }

    /// Drop the file selection when it leaves the refreshed side, and
    /// clamp the hunk cursor into the rendered window.
    fn prune_selection(&mut self, project: ProjectId, staged: bool) {
        if self.show_staged(project) != staged {
            return;
        }
        let visible = self
            .diffs
            .get(&(project, staged))
            .and_then(|info| {
                self.selected_file
                    .get(&project)
                    .map(|selected| info.files.iter().any(|file| &file.path == selected))
            })
            .unwrap_or(false);
        if !visible {
            self.selected_file.remove(&project);
            self.selected_hunk.remove(&project);
            self.hunk_offset.remove(&project);
            return;
        }
        let count = self.hunk_count_for(project);
        if self.selected_hunk(project) >= count.max(1) {
            self.selected_hunk.insert(project, 0);
        }
        self.ensure_cursor_visible(project);
    }
}

/// Spawn parameters for [`spawn_diff_thread`]: bundling keeps the worker
/// entry under the argument-count lint.
pub struct DiffSpawn {
    pub project: ProjectId,
    pub path: Option<PathBuf>,
    pub staged: bool,
    pub generation: u64,
    pub pinned: Option<PathBuf>,
    pub active_cwd: Option<PathBuf>,
    pub context_lines: u8,
    pub tx: std::sync::mpsc::Sender<(u64, ProjectId, bool, DiffRefresh)>,
}

/// Spawn the background diff worker. The worker resolves the M12 root
/// off-thread and runs `git diff` (or `--cached`); the result carries the
/// worker thread id so tests pin the off-UI-thread contract without timing
/// flakes. Cancelled by generation on project switch (caller drops
/// landings, like the git poller).
pub fn spawn_diff_thread(caller: ThreadId, spawn: DiffSpawn) {
    std::thread::spawn(move || {
        let worker = std::thread::current().id();
        debug_assert_ne!(worker, caller, "diff worker must not be the caller");
        let result = refresh_off_thread(
            spawn.pinned.as_deref(),
            spawn.active_cwd.as_deref(),
            spawn.staged,
            spawn.context_lines,
            spawn.path,
        );
        let _ = spawn.tx.send((
            spawn.generation,
            spawn.project,
            spawn.staged,
            DiffRefresh { worker, result },
        ));
    });
}

fn refresh_off_thread(
    pinned: Option<&std::path::Path>,
    active_cwd: Option<&std::path::Path>,
    staged: bool,
    context_lines: u8,
    path: Option<PathBuf>,
) -> Result<DiffInfo, DiffEmpty> {
    let resolved = omaterm_context::resolve_root(pinned, active_cwd);
    let Some(root) = resolved.root else {
        return Err(DiffEmpty::NoRoot);
    };
    let request = omaterm_context::DiffRequest {
        staged,
        path,
        context_lines,
        files_only: false,
    };
    match omaterm_context::git_diff(&root, &request) {
        Ok(info) => Ok(info),
        Err(omaterm_context::GitError::NotARepo) => Err(DiffEmpty::NotRepo),
        Err(omaterm_context::GitError::GitUnavailable(message)) => {
            Err(DiffEmpty::Unavailable(message))
        }
        Err(omaterm_context::GitError::Timeout) => {
            Err(DiffEmpty::Failed("git diff timed out".into()))
        }
        Err(omaterm_context::GitError::GitFailed(message)) => Err(DiffEmpty::Failed(message)),
        Err(omaterm_context::GitError::PathOutsideRoot) => {
            Err(DiffEmpty::Failed("path escapes the project root".into()))
        }
        Err(omaterm_context::GitError::Io(error)) => Err(DiffEmpty::Failed(error.to_string())),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use omaterm_core::{DiffFileInfo, DiffFileStatus, DiffHunkInfo, DiffLineInfo, DiffLineKind};

    fn hunk(lines: usize) -> DiffHunkInfo {
        DiffHunkInfo {
            id: 0,
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

    fn panel_with(project: ProjectId, files: Vec<DiffFileInfo>) -> DiffPanel {
        let mut panel = DiffPanel::default();
        panel.apply_refresh(
            project,
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

    #[test]
    fn align_hunk_pairs_context_and_splits_add_delete() {
        use omaterm_core::{DiffLineInfo, DiffLineKind};
        let hunk = DiffHunkInfo {
            id: 0,
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
        assert_eq!(rows[0].old.as_ref().map(|cell| cell.line_no), Some(10));
        assert_eq!(rows[0].new.as_ref().map(|cell| cell.line_no), Some(20));
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
        assert_eq!(rows[4].old.as_ref().map(|cell| cell.line_no), Some(13));
        assert_eq!(rows[4].new.as_ref().map(|cell| cell.line_no), Some(24));
    }

    #[test]
    fn diff_mode_defaults_split_and_clears_with_project() {
        let project = ProjectId::new();
        let mut panel = DiffPanel::default();
        assert_eq!(panel.diff_mode(project), DiffMode::Split);
        panel.set_diff_mode(project, DiffMode::Inline);
        assert_eq!(panel.diff_mode(project), DiffMode::Inline);
        panel.clear_project(project);
        assert_eq!(panel.diff_mode(project), DiffMode::Split);
    }

    #[test]
    fn hunk_cursor_wraps_within_the_rendered_window() {
        let project = ProjectId::new();
        let mut panel = panel_with(project, vec![file("a.txt", 3)]);
        panel.select_file(project, PathBuf::from("a.txt"));
        assert_eq!(panel.selected_hunk(project), 0);
        assert_eq!(panel.next_hunk(project), 1);
        assert_eq!(panel.next_hunk(project), 2);
        assert_eq!(panel.next_hunk(project), 0);
        assert_eq!(panel.prev_hunk(project), 2);
        assert_eq!(panel.prev_hunk(project), 1);
    }

    #[test]
    fn hunk_cursor_is_a_no_op_without_selection() {
        let project = ProjectId::new();
        let mut panel = panel_with(project, vec![file("a.txt", 2)]);
        assert_eq!(panel.next_hunk(project), 0);
        assert_eq!(panel.prev_hunk(project), 0);
        assert_eq!(panel.hunk_count_for(project), 0);
    }

    #[test]
    fn select_file_resets_the_hunk_cursor() {
        let project = ProjectId::new();
        let mut panel = panel_with(project, vec![file("a.txt", 3), file("b.txt", 1)]);
        panel.select_file(project, PathBuf::from("a.txt"));
        panel.next_hunk(project);
        panel.next_hunk(project);
        assert_eq!(panel.selected_hunk(project), 2);
        panel.select_file(project, PathBuf::from("b.txt"));
        assert_eq!(panel.selected_hunk(project), 0);
        assert_eq!(panel.hunk_count_for(project), 1);
    }

    #[test]
    fn refresh_prunes_gone_files_and_clamps_the_cursor() {
        let project = ProjectId::new();
        let mut panel = panel_with(project, vec![file("a.txt", 3)]);
        panel.select_file(project, PathBuf::from("a.txt"));
        panel.next_hunk(project);
        // Same file with fewer hunks: cursor clamps back to zero.
        panel.apply_refresh(
            project,
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
        assert_eq!(panel.selected_hunk(project), 0);
        // File gone entirely: selection clears.
        panel.apply_refresh(
            project,
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
        assert!(panel.selected_file(project).is_none());
    }

    #[test]
    fn staged_toggle_defaults_unstaged_and_refreshes_swap_sides() {
        let project = ProjectId::new();
        let mut panel = DiffPanel::default();
        assert!(!panel.show_staged(project));
        panel.set_show_staged(project, true);
        assert!(panel.show_staged(project));
        // A landing refresh for the hidden side never disturbs the
        // visible selection.
        panel.select_file(project, PathBuf::from("a.txt"));
        panel.apply_refresh(
            project,
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
        assert_eq!(panel.selected_file(project), Some(&PathBuf::from("a.txt")));
    }

    #[test]
    fn error_refresh_clears_the_visible_side_only() {
        let project = ProjectId::new();
        let mut panel = panel_with(project, vec![file("a.txt", 1)]);
        panel.select_file(project, PathBuf::from("a.txt"));
        panel.apply_refresh(
            project,
            false,
            DiffRefresh {
                worker: std::thread::current().id(),
                result: Err(DiffEmpty::Failed("boom".into())),
            },
        );
        assert!(panel.diff_for(project, false).is_none());
        assert_eq!(
            panel.empty_for(project, false),
            Some(&DiffEmpty::Failed("boom".into()))
        );
        assert!(panel.selected_file(project).is_none());
    }

    #[test]
    fn preview_open_close_is_view_local_per_project() {
        let project = ProjectId::new();
        let other = ProjectId::new();
        let mut panel = DiffPanel::default();
        assert!(!panel.preview_open(project));
        panel.open_preview(project);
        assert!(panel.preview_open(project));
        assert!(!panel.preview_open(other));
        panel.close_preview(project);
        assert!(!panel.preview_open(project));
        // Clearing the project closes its preview and viewport.
        panel.open_preview(project);
        panel.select_file(project, PathBuf::from("a.txt"));
        panel.clear_project(project);
        assert!(!panel.preview_open(project));
        assert_eq!(panel.hunk_offset(project), 0);
    }

    #[test]
    fn preview_viewport_follows_cursor_and_wheel() {
        let project = ProjectId::new();
        // 10 hunks render (under the 32 cap); viewport shows 8.
        let mut panel = panel_with(project, vec![file("a.txt", 10)]);
        panel.select_file(project, PathBuf::from("a.txt"));
        panel.open_preview(project);
        assert_eq!(panel.hunk_offset(project), 0);
        for _ in 0..8 {
            panel.next_hunk(project);
        }
        // Cursor 8 sits outside [0, 8): viewport slides to [1, 9).
        assert_eq!(panel.selected_hunk(project), 8);
        assert_eq!(panel.hunk_offset(project), 1);
        panel.prev_hunk(project);
        panel.prev_hunk(project);
        // Cursor 6 is inside [1, 9): viewport stays.
        assert_eq!(panel.hunk_offset(project), 1);
        // Wheel clamps at both ends.
        assert_eq!(panel.scroll_preview(project, 100), 2);
        assert_eq!(panel.scroll_preview(project, -100), 0);
        assert_eq!(panel.scroll_preview(project, 1), 1);
        // Cursor wrap keeps the viewport valid.
        panel.select_file(project, PathBuf::from("a.txt"));
        assert_eq!(panel.hunk_offset(project), 0);
    }

    #[test]
    fn diff_worker_runs_off_the_calling_thread() {
        let (tx, rx) = std::sync::mpsc::channel();
        let project = ProjectId::new();
        let caller = std::thread::current().id();
        spawn_diff_thread(
            caller,
            DiffSpawn {
                project,
                path: None,
                staged: false,
                generation: 7,
                pinned: Some(std::env::temp_dir()),
                active_cwd: None,
                context_lines: 3,
                tx,
            },
        );
        let (generation, landed_project, staged, refresh) = rx
            .recv_timeout(std::time::Duration::from_secs(10))
            .expect("diff worker must answer");
        assert_eq!((generation, landed_project, staged), (7, project, false));
        assert_ne!(refresh.worker, caller);
        // A plain temp dir resolves (pinned) but is not a repo.
        assert_eq!(refresh.result.unwrap_err(), DiffEmpty::NotRepo);
    }
}
