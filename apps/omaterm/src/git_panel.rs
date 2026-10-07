//! M14 Source Control panel state: contextual-sidebar git status rows plus the
//! project-local selection and commit drafts.
//!
//! GPUI-free. Rendering and key/mouse wiring live in `main.rs`, which owns
//! the worker threads: every git subprocess (root resolution's `rev-parse`
//! and `status` itself) runs on a background worker spawned through
//! [`spawn_status_thread`], never on the UI thread (M14 acceptance). This
//! panel only tracks last-good statuses, explicit empty/error states, the
//! selected row, and the discard arm. Mutations (stage/unstage/discard)
//! go through the `GitCommand` dispatcher — the same path as IPC/CLI
//! (blueprint §62) — and set a refresh hint the poller consumes.
//!
//! Status payloads are `omaterm_core::GitStatusInfo` (root-relative paths,
//! bounded entries, accurate `truncated`).

use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::thread::ThreadId;
use std::time::{Duration, Instant};

use omaterm_core::{GitStatusInfo, ProjectId};

/// Explicit non-data state for a project. `NoRoot` (M12 `none`) and
/// `NotRepo` render the empty state, never an error; failures name the
/// cause without paths or contents (blueprint §45).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum GitEmpty {
    NoRoot,
    NotRepo,
    Unavailable(String),
    Failed(String),
}

/// Outcome of one background status refresh, tagged with the worker
/// thread that ran git. The UI thread drains these; it never spawns git
/// itself — the `worker != caller` test below pins that contract.
#[derive(Debug)]
pub struct GitRefresh {
    pub worker: ThreadId,
    pub result: Result<GitStatusInfo, GitEmpty>,
}

/// Largest commit message the panel input accepts (mirrors core
/// validation; the router re-validates).
pub const MAX_COMMIT_MESSAGE_LEN: usize = 4 * 1024;

/// Rows rendered per group at most; the footer names the truncation.
pub const MAX_GIT_RENDER_ROWS: usize = 150;

/// Which change group a panel row belongs to (drives the action set).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum GitGroup {
    Staged,
    Unstaged,
    Untracked,
}

/// One render row: group + entry path.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitRow {
    pub group: GitGroup,
    pub path: PathBuf,
    pub renamed_from: Option<PathBuf>,
}

#[derive(Default)]
pub struct GitPanel {
    statuses: HashMap<ProjectId, GitStatusInfo>,
    empties: HashMap<ProjectId, GitEmpty>,
    selected: HashMap<ProjectId, PathBuf>,
    /// Collapsed change groups per project (`true` = staged group,
    /// `false` = working-tree group). View-local, never persisted.
    collapsed: HashSet<(ProjectId, bool)>,
    /// Commit message drafts, one per project so switching never loses
    /// typed text. Single-line (the desktop input submits on Enter).
    commit_drafts: HashMap<ProjectId, String>,
    /// Whether the commit input owns the keyboard (focused by clicking
    /// it; Esc, submit, tab switch, or a terminal click releases it).
    commit_focused: bool,
}

impl GitPanel {
    /// Only the explicit destructive answer may commit a captured dialog target.
    pub fn discard_confirmed(
        answer: Option<usize>,
        captured_project: ProjectId,
        selected_project: Option<ProjectId>,
        shutting_down: bool,
    ) -> bool {
        answer == Some(1) && selected_project == Some(captured_project) && !shutting_down
    }

    pub fn status_for(&self, project: ProjectId) -> Option<&GitStatusInfo> {
        self.statuses.get(&project)
    }

    pub fn empty_for(&self, project: ProjectId) -> Option<&GitEmpty> {
        self.empties.get(&project)
    }

    pub fn selected_path(&self, project: ProjectId) -> Option<&PathBuf> {
        self.selected.get(&project)
    }

    /// Record a landed refresh: status replaces any error and vice versa.
    pub fn apply_refresh(&mut self, project: ProjectId, refresh: GitRefresh) {
        match refresh.result {
            Ok(status) => {
                self.empties.remove(&project);
                self.statuses.insert(project, status);
                self.prune_selection(project);
            }
            Err(empty) => {
                self.statuses.remove(&project);
                self.empties.insert(project, empty);
                self.selected.remove(&project);
            }
        }
    }

    /// Drop cached state for a project (switch-away memory bound is owned
    /// by the caller; this clears on demand).
    pub fn clear_project(&mut self, project: ProjectId) {
        self.statuses.remove(&project);
        self.empties.remove(&project);
        self.selected.remove(&project);
        self.commit_drafts.remove(&project);
        self.collapsed.retain(|(owner, _)| *owner != project);
    }

    /// Whether a change group is collapsed (`staged` selects the staged
    /// group, otherwise the working-tree group).
    pub fn is_collapsed(&self, project: ProjectId, staged: bool) -> bool {
        self.collapsed.contains(&(project, staged))
    }

    /// Toggle a change group's collapse. Returns the new collapsed state.
    pub fn toggle_collapsed(&mut self, project: ProjectId, staged: bool) -> bool {
        if self.collapsed.remove(&(project, staged)) {
            false
        } else {
            self.collapsed.insert((project, staged));
            true
        }
    }

    pub fn select(&mut self, project: ProjectId, path: PathBuf) {
        self.selected.insert(project, path);
    }

    /// Drop the row selection so keyboard navigation can hand the cursor
    /// to the history Graph (single cursor across the Git tab).
    pub fn clear_selection(&mut self, project: ProjectId) {
        self.selected.remove(&project);
    }

    /// Move the row selection by `delta` (clamped, no wrap). Selects the
    /// first row when nothing is selected. Returns the newly selected row,
    /// if any. Drives Alt+Up/Down keyboard navigation.
    pub fn move_selection(&mut self, project: ProjectId, delta: i32) -> Option<GitRow> {
        let rows = self.rows_for(project);
        if rows.is_empty() {
            return None;
        }
        let next = match self
            .selected
            .get(&project)
            .and_then(|selected| rows.iter().position(|row| &row.path == selected))
        {
            // Nothing (or stale) selected: land on the leading edge.
            None => {
                if delta >= 0 {
                    0
                } else {
                    rows.len() - 1
                }
            }
            Some(at) => (at as i32 + delta).clamp(0, rows.len() as i32 - 1) as usize,
        };
        let row = rows[next].clone();
        self.selected.insert(project, row.path.clone());
        Some(row)
    }

    /// Flat render rows in group order (staged, unstaged, untracked).
    pub fn rows_for(&self, project: ProjectId) -> Vec<GitRow> {
        let Some(status) = self.statuses.get(&project) else {
            return Vec::new();
        };
        let mut rows = Vec::new();
        for entry in &status.staged {
            rows.push(GitRow {
                group: GitGroup::Staged,
                path: entry.path.clone(),
                renamed_from: entry.renamed_from.clone(),
            });
        }
        for entry in &status.unstaged {
            rows.push(GitRow {
                group: GitGroup::Unstaged,
                path: entry.path.clone(),
                renamed_from: entry.renamed_from.clone(),
            });
        }
        for entry in &status.untracked {
            rows.push(GitRow {
                group: GitGroup::Untracked,
                path: entry.path.clone(),
                renamed_from: None,
            });
        }
        rows
    }

    /// Drop a path from the selection when it leaves the status.
    fn prune_selection(&mut self, project: ProjectId) {
        let Some(selected) = self.selected.get(&project) else {
            return;
        };
        let visible = self
            .rows_for(project)
            .iter()
            .any(|row| &row.path == selected);
        if !visible {
            self.selected.remove(&project);
        }
    }

    /// Current commit draft for a project (empty when nothing typed).
    pub fn commit_draft(&self, project: ProjectId) -> &str {
        self.commit_drafts
            .get(&project)
            .map(String::as_str)
            .unwrap_or_default()
    }

    /// Append a typed character to the draft. Control characters never
    /// enter through the single-line input; over-cap input is dropped.
    /// Returns true when the draft changed.
    pub fn push_commit_char(&mut self, project: ProjectId, char: char) -> bool {
        if char.is_control() {
            return false;
        }
        let draft = self.commit_drafts.entry(project).or_default();
        if draft.len() >= MAX_COMMIT_MESSAGE_LEN {
            return false;
        }
        draft.push(char);
        true
    }

    /// Delete the last draft character. Returns true when one was removed.
    pub fn pop_commit_char(&mut self, project: ProjectId) -> bool {
        let remove = self
            .commit_drafts
            .get(&project)
            .is_some_and(|draft| !draft.is_empty());
        if remove && let Some(draft) = self.commit_drafts.get_mut(&project) {
            draft.pop();
        }
        remove
    }

    /// Take the draft for submission, leaving an empty one behind.
    pub fn take_commit_draft(&mut self, project: ProjectId) -> String {
        self.commit_drafts.remove(&project).unwrap_or_default()
    }

    /// Restore a draft (failed submissions put the message back so the
    /// user can fix and retry instead of retyping).
    pub fn restore_commit_draft(&mut self, project: ProjectId, message: String) {
        if message.is_empty() {
            return;
        }
        self.commit_drafts.insert(project, message);
    }

    pub const fn commit_focused(&self) -> bool {
        self.commit_focused
    }

    pub const fn set_commit_focused(&mut self, focused: bool) {
        self.commit_focused = focused;
    }
}

/// Decide whether the poller should spawn a status refresh: first sight
/// of a project, an explicit dirty hint (manual refresh, mutation, or a
/// post-`terminal.run` submission landing), or the configured interval
/// elapsing. Pure and unit-tested, including the timing boundaries.
pub fn should_refresh(
    known: bool,
    dirty_hint: bool,
    last: Option<Instant>,
    interval: Duration,
    now: Instant,
) -> bool {
    if !known || dirty_hint {
        return true;
    }
    last.is_none_or(|at| now.duration_since(at) >= interval)
}

/// Spawn the background status worker. The worker resolves the M12 root
/// off-thread (pin wins; else bounded `rev-parse` of the active shell
/// CWD) and runs `git status`; the result carries the worker thread id
/// so tests pin the off-UI-thread contract without timing flakes.
pub fn spawn_status_thread(
    _caller: ThreadId,
    project: ProjectId,
    generation: u64,
    pinned: Option<PathBuf>,
    active_cwd: Option<PathBuf>,
    limit: usize,
    tx: std::sync::mpsc::Sender<(u64, ProjectId, GitRefresh)>,
) {
    std::thread::spawn(move || {
        let worker = std::thread::current().id();
        debug_assert_ne!(worker, _caller, "status worker must not be the caller");
        let result = refresh_off_thread(pinned.as_deref(), active_cwd.as_deref(), limit);
        let _ = tx.send((generation, project, GitRefresh { worker, result }));
    });
}

fn refresh_off_thread(
    pinned: Option<&std::path::Path>,
    active_cwd: Option<&std::path::Path>,
    limit: usize,
) -> Result<GitStatusInfo, GitEmpty> {
    let resolved = omaterm_context::resolve_root(pinned, active_cwd);
    let Some(root) = resolved.root else {
        return Err(GitEmpty::NoRoot);
    };
    match omaterm_context::git_status(&root, limit) {
        Ok(status) => Ok(status),
        Err(omaterm_context::GitError::NotARepo) => Err(GitEmpty::NotRepo),
        Err(omaterm_context::GitError::GitUnavailable(message)) => {
            Err(GitEmpty::Unavailable(message))
        }
        Err(omaterm_context::GitError::Timeout) => {
            Err(GitEmpty::Failed("git status timed out".into()))
        }
        Err(omaterm_context::GitError::Cancelled) => {
            Err(GitEmpty::Failed("git status cancelled".into()))
        }
        Err(omaterm_context::GitError::GitFailed(message)) => Err(GitEmpty::Failed(message)),
        Err(omaterm_context::GitError::PathOutsideRoot) => {
            Err(GitEmpty::Failed("path escapes the project root".into()))
        }
        // Unreachable on the read-only status path (kept for exhaustiveness).
        Err(
            omaterm_context::GitError::DirtyWorktree
            | omaterm_context::GitError::CurrentBranch
            | omaterm_context::GitError::AuthFailed
            | omaterm_context::GitError::Offline
            | omaterm_context::GitError::Diverged
            | omaterm_context::GitError::NonFastForward
            | omaterm_context::GitError::NoUpstream,
        ) => Err(GitEmpty::Failed("branch state blocks git status".into())),
        Err(omaterm_context::GitError::Io(error)) => Err(GitEmpty::Failed(error.to_string())),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn status_with(staged: usize, unstaged: usize, untracked: usize) -> GitStatusInfo {
        let entry = |name: &str| omaterm_core::GitEntry {
            path: PathBuf::from(name),
            renamed_from: None,
            x: 'M',
            y: '.',
        };
        GitStatusInfo {
            branch: Some("main".into()),
            upstream: None,
            ahead: 0,
            behind: 0,
            staged: (0..staged).map(|i| entry(&format!("s{i}"))).collect(),
            unstaged: (0..unstaged).map(|i| entry(&format!("u{i}"))).collect(),
            untracked: (0..untracked).map(|i| entry(&format!("n{i}"))).collect(),
            truncated: false,
        }
    }

    #[test]
    fn apply_refresh_rows_and_prune_selection() {
        let project = ProjectId::new();
        let mut panel = GitPanel::default();
        panel.apply_refresh(
            project,
            GitRefresh {
                worker: std::thread::current().id(),
                result: Ok(status_with(1, 1, 1)),
            },
        );
        let rows = panel.rows_for(project);
        assert_eq!(rows.len(), 3);
        assert_eq!(rows[0].group, GitGroup::Staged);
        assert_eq!(rows[1].group, GitGroup::Unstaged);
        assert_eq!(rows[2].group, GitGroup::Untracked);

        panel.select(project, PathBuf::from("gone.txt"));
        panel.apply_refresh(
            project,
            GitRefresh {
                worker: std::thread::current().id(),
                result: Ok(status_with(0, 0, 0)),
            },
        );
        assert!(panel.selected_path(project).is_none());

        panel.apply_refresh(
            project,
            GitRefresh {
                worker: std::thread::current().id(),
                result: Err(GitEmpty::NotRepo),
            },
        );
        assert!(panel.status_for(project).is_none());
        assert_eq!(panel.empty_for(project), Some(&GitEmpty::NotRepo));
    }

    #[test]
    fn commit_draft_push_pop_take_and_restore() {
        let project = ProjectId::new();
        let mut panel = GitPanel::default();
        assert_eq!(panel.commit_draft(project), "");
        // Control characters never enter; printable text does.
        assert!(!panel.push_commit_char(project, '\n'));
        assert!(panel.push_commit_char(project, 'f'));
        assert!(panel.push_commit_char(project, 'i'));
        assert!(panel.push_commit_char(project, 'x'));
        assert_eq!(panel.commit_draft(project), "fix");
        assert!(panel.pop_commit_char(project));
        assert_eq!(panel.commit_draft(project), "fi");
        // Take leaves emptiness behind; restore puts a failed message
        // back, but never an empty one.
        assert_eq!(panel.take_commit_draft(project), "fi");
        assert_eq!(panel.commit_draft(project), "");
        assert!(!panel.pop_commit_char(project));
        panel.restore_commit_draft(project, String::new());
        assert_eq!(panel.commit_draft(project), "");
        panel.restore_commit_draft(project, "retry me".into());
        assert_eq!(panel.commit_draft(project), "retry me");
        // Clearing the project drops the draft with everything else.
        panel.clear_project(project);
        assert_eq!(panel.commit_draft(project), "");
        // Focus flag round-trips.
        assert!(!panel.commit_focused());
        panel.set_commit_focused(true);
        assert!(panel.commit_focused());
    }

    #[test]
    fn discard_dialog_cancellation_and_stale_context_never_confirm() {
        let project = ProjectId::new();
        for answer in [None, Some(0), Some(2)] {
            assert!(!GitPanel::discard_confirmed(
                answer,
                project,
                Some(project),
                false
            ));
        }
        assert!(GitPanel::discard_confirmed(
            Some(1),
            project,
            Some(project),
            false
        ));
        assert!(!GitPanel::discard_confirmed(
            Some(1),
            project,
            Some(ProjectId::new()),
            false
        ));
        assert!(!GitPanel::discard_confirmed(Some(1), project, None, false));
        assert!(!GitPanel::discard_confirmed(
            Some(1),
            project,
            Some(project),
            true
        ));
    }

    #[test]
    fn move_selection_walks_flat_rows_clamped() {
        let project = ProjectId::new();
        let mut panel = GitPanel::default();
        assert_eq!(panel.move_selection(project, 1), None);
        panel.apply_refresh(
            project,
            GitRefresh {
                worker: std::thread::current().id(),
                result: Ok(status_with(1, 1, 1)),
            },
        );
        // Nothing selected: positive lands first, negative lands last.
        assert_eq!(
            panel.move_selection(project, 1).map(|row| row.path),
            Some(PathBuf::from("s0"))
        );
        panel.selected.remove(&project);
        assert_eq!(
            panel.move_selection(project, -1).map(|row| row.path),
            Some(PathBuf::from("n0"))
        );
        // Walk and clamp at both ends.
        panel.selected.remove(&project);
        panel.move_selection(project, 1);
        assert_eq!(panel.selected_path(project), Some(&PathBuf::from("s0")));
        panel.move_selection(project, 1);
        assert_eq!(panel.selected_path(project), Some(&PathBuf::from("u0")));
        panel.move_selection(project, 1);
        panel.move_selection(project, 1);
        assert_eq!(panel.selected_path(project), Some(&PathBuf::from("n0")));
        panel.move_selection(project, -10);
        assert_eq!(panel.selected_path(project), Some(&PathBuf::from("s0")));
    }

    #[test]
    fn group_collapse_toggles_per_project_and_side() {
        let project = ProjectId::new();
        let other = ProjectId::new();
        let mut panel = GitPanel::default();
        assert!(!panel.is_collapsed(project, true));
        assert!(panel.toggle_collapsed(project, true));
        assert!(panel.is_collapsed(project, true));
        assert!(!panel.is_collapsed(project, false));
        assert!(!panel.is_collapsed(other, true));
        assert!(!panel.toggle_collapsed(project, true));
        assert!(!panel.is_collapsed(project, true));
        panel.toggle_collapsed(project, false);
        panel.clear_project(project);
        assert!(!panel.is_collapsed(project, false));
    }

    #[test]
    fn should_refresh_covers_first_sight_dirty_and_interval() {
        let now = Instant::now();
        let interval = Duration::from_secs(5);
        assert!(should_refresh(false, false, None, interval, now));
        assert!(should_refresh(true, true, Some(now), interval, now));
        assert!(should_refresh(true, false, None, interval, now));
        assert!(!should_refresh(true, false, Some(now), interval, now));
        assert!(should_refresh(
            true,
            false,
            Some(now - interval),
            interval,
            now
        ));
    }

    /// The M14 off-UI-thread contract, pinned without timing flakes: the
    /// worker that runs git is never the calling thread, and a real repo
    /// still reports through it.
    #[test]
    fn status_worker_runs_off_the_caller_thread() {
        let caller = std::thread::current().id();
        let repo: PathBuf =
            std::env::temp_dir().join(format!("omaterm-m14-panel-{}", std::process::id()));
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
        git(&["config", "user.email", "m14@test"]);
        git(&["config", "user.name", "m14"]);
        git(&["config", "commit.gpgsign", "false"]);
        std::fs::write(repo.join("a.txt"), b"v1\n").unwrap();
        git(&["add", "-A"]);
        git(&["commit", "-qm", "init"]);
        std::fs::write(repo.join("a.txt"), b"v2\n").unwrap();

        let (tx, rx) = std::sync::mpsc::channel();
        let project = ProjectId::new();
        spawn_status_thread(caller, project, 7, Some(repo.clone()), None, 5000, tx);
        let (generation, landed_project, refresh) = rx
            .recv_timeout(Duration::from_secs(30))
            .expect("worker must answer");
        assert_eq!((generation, landed_project), (7, project));
        assert_ne!(refresh.worker, caller, "git must run off the caller thread");
        let status = refresh.result.expect("repo status");
        assert_eq!(status.unstaged.len(), 1);
        assert!(status.branch.is_some());

        // A non-repo pin reports the empty state through the same path.
        let plain: PathBuf =
            std::env::temp_dir().join(format!("omaterm-m14-plain-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&plain);
        std::fs::create_dir_all(&plain).unwrap();
        let (tx, rx) = std::sync::mpsc::channel();
        spawn_status_thread(caller, project, 8, Some(plain.clone()), None, 5000, tx);
        let (_, _, refresh) = rx.recv_timeout(Duration::from_secs(30)).unwrap();
        assert_ne!(refresh.worker, caller);
        assert_eq!(refresh.result, Err(GitEmpty::NotRepo));

        let _ = std::fs::remove_dir_all(&repo);
        let _ = std::fs::remove_dir_all(&plain);
    }
}
