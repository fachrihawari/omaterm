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

/// Hunks rendered per selected file at most; prev/next navigation cycles
/// within the rendered window.
pub const MAX_DIFF_RENDER_HUNKS: usize = 32;
/// Body lines rendered per hunk at most.
pub const MAX_DIFF_RENDER_LINES: usize = 200;

#[derive(Default)]
pub struct DiffPanel {
    diffs: HashMap<(ProjectId, bool), DiffInfo>,
    empties: HashMap<(ProjectId, bool), DiffEmpty>,
    selected_file: HashMap<ProjectId, PathBuf>,
    selected_hunk: HashMap<ProjectId, usize>,
    show_staged: HashMap<ProjectId, bool>,
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

    pub fn set_show_staged(&mut self, project: ProjectId, staged: bool) {
        self.show_staged.insert(project, staged);
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
    }

    /// Select a file and reset its hunk cursor. Called by file-row clicks
    /// and by diff-on-select from the Source Control panel.
    pub fn select_file(&mut self, project: ProjectId, path: PathBuf) {
        self.selected_file.insert(project, path);
        self.selected_hunk.insert(project, 0);
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
    /// no-op. Returns the new cursor.
    pub fn next_hunk(&mut self, project: ProjectId) -> usize {
        let count = self.hunk_count_for(project);
        let next = if count == 0 {
            0
        } else {
            (self.selected_hunk(project) + 1) % count
        };
        self.selected_hunk.insert(project, next);
        next
    }

    /// Move the hunk cursor back, wrapping to the last rendered hunk.
    /// Returns the new cursor.
    pub fn prev_hunk(&mut self, project: ProjectId) -> usize {
        let count = self.hunk_count_for(project);
        let cursor = self.selected_hunk(project);
        let next = if count == 0 {
            0
        } else {
            cursor.checked_sub(1).unwrap_or(count - 1)
        };
        self.selected_hunk.insert(project, next);
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
            return;
        }
        let count = self.hunk_count_for(project);
        if self.selected_hunk(project) >= count.max(1) {
            self.selected_hunk.insert(project, 0);
        }
    }
}

/// Spawn parameters for [`spawn_diff_thread`]: bundling keeps the worker
/// entry under the argument-count lint.
pub struct DiffSpawn {
    pub project: ProjectId,
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
) -> Result<DiffInfo, DiffEmpty> {
    let resolved = omaterm_context::resolve_root(pinned, active_cwd);
    let Some(root) = resolved.root else {
        return Err(DiffEmpty::NoRoot);
    };
    let request = omaterm_context::DiffRequest {
        staged,
        path: None,
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
            old_start: 1,
            old_lines: lines as u32,
            new_start: 1,
            new_lines: lines as u32,
            lines: (0..lines)
                .map(|i| DiffLineInfo {
                    kind: DiffLineKind::Context,
                    text: format!("line {i}"),
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
    fn diff_worker_runs_off_the_calling_thread() {
        let (tx, rx) = std::sync::mpsc::channel();
        let project = ProjectId::new();
        let caller = std::thread::current().id();
        spawn_diff_thread(
            caller,
            DiffSpawn {
                project,
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
