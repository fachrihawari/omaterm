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
//!
//! M20 adds multi-repo support: every per-repo map is keyed by
//! [`RepoKey`] (project + repo directory name, `None` for the project-root
//! repo). A project with several depth-1 repos renders one VS Code-style
//! section per repo, each with its own status, selection, collapse state,
//! stash list and commit draft.

use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::thread::ThreadId;
use std::time::{Duration, Instant};

use omaterm_core::{GitStatusInfo, ProjectId};

/// Identity of one repository inside a project (M20).
///
/// `repo` is the child directory name discovered by the depth-1 scan, or
/// `None` when the resolved project root is itself the repository. This is
/// state identity only — filesystem paths are resolved from the live scan
/// at use time, so a renamed or moved project directory never desyncs it.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct RepoKey {
    pub project: ProjectId,
    pub repo: Option<String>,
}

impl RepoKey {
    /// A named child repository (`root/api` → `Some("api")`).
    pub fn named(project: ProjectId, repo: &str) -> Self {
        Self {
            project,
            repo: Some(repo.to_string()),
        }
    }

    /// The repository at the project root (`root` itself holds `.git`).
    pub fn root(project: ProjectId) -> Self {
        Self {
            project,
            repo: None,
        }
    }

    /// Every repo this state was captured for, in older API shape: an
    /// iterator-free helper used by cleanup paths.
    pub fn matches_project(&self, project: ProjectId) -> bool {
        self.project == project
    }
}

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

/// Rows rendered per group at most; the footer names the truncation.
pub const MAX_GIT_RENDER_ROWS: usize = 150;

/// Which change group a panel row belongs to (drives the action set).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum GitGroup {
    Staged,
    Unstaged,
    Untracked,
}

/// Visibility of the stash group in the Git tab.
///
/// - `Hidden`: no stash entries and (when unloaded) nothing to assume —
///   the group takes no space. A background fetch may still be in flight
///   so entries can appear once loaded.
/// - `PushOnly`: dirty tree with no entries yet — push input only, no
///   count pill and no `No stashes.` noise, so the first stash stays
///   reachable.
/// - `Full`: entries exist — header + push input + rows.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StashGroupMode {
    Hidden,
    PushOnly,
    Full,
}

/// Pure visibility predicate for the stash group. `loaded_count` is
/// `None` while the on-demand list has not landed yet.
pub const fn stash_group_mode(dirty: bool, loaded_count: Option<usize>) -> StashGroupMode {
    match loaded_count {
        Some(count) if count > 0 => StashGroupMode::Full,
        _ if dirty => StashGroupMode::PushOnly,
        _ => StashGroupMode::Hidden,
    }
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
    statuses: HashMap<RepoKey, GitStatusInfo>,
    empties: HashMap<RepoKey, GitEmpty>,
    selected: HashMap<RepoKey, PathBuf>,
    /// Collapsed change groups per repo (`true` = staged group,
    /// `false` = working-tree group). View-local, never persisted.
    collapsed: HashSet<(RepoKey, bool)>,
    /// Collapsed stash groups per repo. View-local, never persisted.
    stash_collapsed: HashSet<RepoKey>,
    /// Last-good stash lists per repo (fetched on demand, not polled).
    stashes: HashMap<RepoKey, omaterm_core::GitStashList>,
    /// Selected stash index per repo (row highlight + actions).
    stash_selected: HashMap<RepoKey, usize>,
    /// Commit message drafts, one per repo so switching between repos
    /// never loses typed text. Single-line (the desktop input submits
    /// on Enter).
    commit_drafts: HashMap<RepoKey, crate::git_input::GitInput>,
    /// Whether the commit input owns the keyboard (focused by clicking
    /// it; Esc, submit, repo switch, tab switch, or a terminal click
    /// releases it).
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

    /// Only the explicit destructive answer may drop a captured stash
    /// entry. Extends [`GitPanel::discard_confirmed`] with a repo-identity
    /// check: the captured multi-repo key must still be the active repo,
    /// otherwise the dialog went stale (repo switch / rescan) and the drop
    /// must not fire against a different repository's reflog.
    pub fn stash_drop_confirmed(
        answer: Option<usize>,
        captured_project: ProjectId,
        captured_repo: &RepoKey,
        selected_project: Option<ProjectId>,
        active_repo: &RepoKey,
        shutting_down: bool,
    ) -> bool {
        answer == Some(1)
            && selected_project == Some(captured_project)
            && active_repo == captured_repo
            && !shutting_down
    }

    pub fn status_for(&self, key: &RepoKey) -> Option<&GitStatusInfo> {
        self.statuses.get(key)
    }

    pub fn empty_for(&self, key: &RepoKey) -> Option<&GitEmpty> {
        self.empties.get(key)
    }

    pub fn selected_path(&self, key: &RepoKey) -> Option<&PathBuf> {
        self.selected.get(key)
    }

    /// Record a landed refresh: status replaces any error and vice versa.
    pub fn apply_refresh(&mut self, key: &RepoKey, refresh: GitRefresh) {
        match refresh.result {
            Ok(status) => {
                self.empties.remove(key);
                self.statuses.insert(key.clone(), status);
                self.prune_selection(key);
            }
            Err(empty) => {
                self.statuses.remove(key);
                self.empties.insert(key.clone(), empty);
                self.selected.remove(key);
            }
        }
    }

    /// Drop all cached state for one repo (project switch, deleting a
    /// project, or a repo leaving the depth-1 scan).
    pub fn clear_project(&mut self, key: &RepoKey) {
        self.statuses.remove(key);
        self.empties.remove(key);
        self.selected.remove(key);
        self.commit_drafts.remove(key);
        self.collapsed.retain(|(owner, _)| owner != key);
        self.stash_collapsed.remove(key);
        self.stashes.remove(key);
        self.stash_selected.remove(key);
    }

    /// Last-good stash list for a repo, if fetched.
    pub fn stashes_for(&self, key: &RepoKey) -> Option<&omaterm_core::GitStashList> {
        self.stashes.get(key)
    }

    /// Install a freshly fetched stash list (loadings clears on error).
    pub fn set_stashes(&mut self, key: &RepoKey, list: omaterm_core::GitStashList) {
        // Keep the selection on a surviving index; default to newest.
        if self
            .stash_selected
            .get(key)
            .is_none_or(|selected| *selected >= list.stashes.len() && !list.stashes.is_empty())
        {
            if list.stashes.is_empty() {
                self.stash_selected.remove(key);
            } else {
                self.stash_selected.insert(key.clone(), 0);
            }
        }
        self.stashes.insert(key.clone(), list);
    }

    /// Drop the stash list (fetch failures keep last-good rows elsewhere;
    /// here an explicit clear precedes a refetch after mutations).
    pub fn clear_stashes(&mut self, key: &RepoKey) {
        self.stashes.remove(key);
    }

    /// Selected stash index for a repo, if any rows exist.
    pub fn stash_selection(&self, key: &RepoKey) -> Option<usize> {
        self.stash_selected.get(key).copied()
    }

    /// Select a stash row by index (clamped to the loaded list).
    pub fn select_stash(&mut self, key: &RepoKey, index: usize) {
        let len = self
            .stashes
            .get(key)
            .map(|list| list.stashes.len())
            .unwrap_or(0);
        if len == 0 {
            self.stash_selected.remove(key);
        } else {
            self.stash_selected.insert(key.clone(), index.min(len - 1));
        }
    }

    /// Whether the stash group is collapsed for a repo.
    pub fn is_stash_collapsed(&self, key: &RepoKey) -> bool {
        self.stash_collapsed.contains(key)
    }

    /// Toggle the stash group collapse.
    pub fn toggle_stash_collapsed(&mut self, key: &RepoKey) -> bool {
        if self.stash_collapsed.remove(key) {
            false
        } else {
            self.stash_collapsed.insert(key.clone());
            true
        }
    }

    /// Whether the change group is collapsed (`staged` selects the staged
    /// group, otherwise the working-tree group).
    pub fn is_collapsed(&self, key: &RepoKey, staged: bool) -> bool {
        self.collapsed.contains(&(key.clone(), staged))
    }

    /// Toggle a change group's collapse. Returns the new collapsed state.
    pub fn toggle_collapsed(&mut self, key: &RepoKey, staged: bool) -> bool {
        if self.collapsed.remove(&(key.clone(), staged)) {
            false
        } else {
            self.collapsed.insert((key.clone(), staged));
            true
        }
    }

    pub fn select(&mut self, key: &RepoKey, path: PathBuf) {
        self.selected.insert(key.clone(), path);
    }

    /// Drop the row selection so keyboard navigation can hand the cursor
    /// to the history Graph (single cursor across the Git tab).
    pub fn clear_selection(&mut self, key: &RepoKey) {
        self.selected.remove(key);
    }

    /// Move the row selection by `delta` (clamped, no wrap). Selects the
    /// first row when nothing is selected. Returns the newly selected row,
    /// if any. Drives Alt+Up/Down keyboard navigation.
    pub fn move_selection(&mut self, key: &RepoKey, delta: i32) -> Option<GitRow> {
        let rows = self.rows_for(key);
        if rows.is_empty() {
            return None;
        }
        let next = match self
            .selected
            .get(key)
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
        self.selected.insert(key.clone(), row.path.clone());
        Some(row)
    }

    /// Flat render rows in group order (staged, unstaged, untracked).
    pub fn rows_for(&self, key: &RepoKey) -> Vec<GitRow> {
        let Some(status) = self.statuses.get(key) else {
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
    fn prune_selection(&mut self, key: &RepoKey) {
        let Some(selected) = self.selected.get(key) else {
            return;
        };
        let visible = self.rows_for(key).iter().any(|row| &row.path == selected);
        if !visible {
            self.selected.remove(key);
        }
    }

    /// Current commit draft for a repo (empty when nothing typed).
    #[cfg(test)]
    pub fn commit_draft(&self, key: &RepoKey) -> &str {
        self.commit_drafts
            .get(key)
            .map(crate::git_input::GitInput::text)
            .unwrap_or_default()
    }

    pub fn commit_input(&self, key: &RepoKey) -> Option<&crate::git_input::GitInput> {
        self.commit_drafts.get(key)
    }

    pub fn commit_input_mut(&mut self, key: &RepoKey) -> &mut crate::git_input::GitInput {
        self.commit_drafts.entry(key.clone()).or_default()
    }

    /// Take the draft for submission, leaving an empty one behind.
    pub fn take_commit_draft(&mut self, key: &RepoKey) -> String {
        self.commit_drafts
            .remove(key)
            .unwrap_or_default()
            .into_text()
    }

    /// Restore a draft (failed submissions put the message back so the
    /// user can fix and retry instead of retyping).
    pub fn restore_commit_draft(&mut self, key: &RepoKey, message: String) {
        if message.is_empty() {
            return;
        }
        let mut draft = crate::git_input::GitInput::default();
        draft.insert(&message);
        self.commit_drafts.insert(key.clone(), draft);
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
///
/// `repo_root` is the M20 multi-repo override: when the caller already
/// knows which repository under the project root to poll (a named child
/// of the depth-1 scan, or the project root itself), it is used directly
/// after the same directory check the resolver applies. It must live
/// inside the project root — the worker refuses anything else.
pub fn spawn_status_thread(_caller: ThreadId, request: StatusRequest, tx: StatusSender) {
    let StatusRequest {
        key,
        generation,
        repo_root,
        pinned,
        active_cwd,
        limit,
    } = request;
    std::thread::spawn(move || {
        let worker = std::thread::current().id();
        debug_assert_ne!(worker, _caller, "status worker must not be the caller");
        let result = refresh_off_thread(
            repo_root.as_deref(),
            pinned.as_deref(),
            active_cwd.as_deref(),
            limit,
        );
        let _ = tx.send((generation, key, GitRefresh { worker, result }));
    });
}

/// One bounded status query: which repository, its generation guard, the
/// path to poll directly (M20 override) and the fallback roots for the
/// project-root resolution.
#[derive(Debug, Clone)]
pub struct StatusRequest {
    pub key: RepoKey,
    pub generation: u64,
    /// M20 multi-repo override: poll this repository rather than resolve
    /// the project root.
    pub repo_root: Option<PathBuf>,
    pub pinned: Option<PathBuf>,
    pub active_cwd: Option<PathBuf>,
    pub limit: usize,
}

/// Completion channel for [`spawn_status_thread`].
pub type StatusSender = std::sync::mpsc::Sender<(u64, RepoKey, GitRefresh)>;

fn refresh_off_thread(
    repo_root: Option<&std::path::Path>,
    pinned: Option<&std::path::Path>,
    active_cwd: Option<&std::path::Path>,
    limit: usize,
) -> Result<GitStatusInfo, GitEmpty> {
    // An explicit repo root wins: M20 sections poll the repository the
    // UI is showing, not the project-root resolution. Anything that is
    // not a directory falls through to the resolver's empty state.
    let root = match repo_root {
        Some(root) if root.is_dir() => Some(root.to_path_buf()),
        Some(_) => None,
        None => omaterm_context::resolve_root(pinned, active_cwd).root,
    };
    let Some(root) = root else {
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
        let key = RepoKey::root(ProjectId::new());
        let mut panel = GitPanel::default();
        panel.apply_refresh(
            &key,
            GitRefresh {
                worker: std::thread::current().id(),
                result: Ok(status_with(1, 1, 1)),
            },
        );
        let rows = panel.rows_for(&key);
        assert_eq!(rows.len(), 3);
        assert_eq!(rows[0].group, GitGroup::Staged);
        assert_eq!(rows[1].group, GitGroup::Unstaged);
        assert_eq!(rows[2].group, GitGroup::Untracked);

        panel.select(&key, PathBuf::from("gone.txt"));
        panel.apply_refresh(
            &key,
            GitRefresh {
                worker: std::thread::current().id(),
                result: Ok(status_with(0, 0, 0)),
            },
        );
        assert!(panel.selected_path(&key).is_none());

        panel.apply_refresh(
            &key,
            GitRefresh {
                worker: std::thread::current().id(),
                result: Err(GitEmpty::NotRepo),
            },
        );
        assert!(panel.status_for(&key).is_none());
        assert_eq!(panel.empty_for(&key), Some(&GitEmpty::NotRepo));
    }

    #[test]
    fn commit_draft_push_pop_take_and_restore() {
        let key = RepoKey::root(ProjectId::new());
        let mut panel = GitPanel::default();
        assert_eq!(panel.commit_draft(&key), "");
        // Control characters never enter; printable text does.
        assert!(!panel.commit_input_mut(&key).insert("\0"));
        assert!(panel.commit_input_mut(&key).insert("f"));
        assert!(panel.commit_input_mut(&key).insert("i"));
        assert!(panel.commit_input_mut(&key).insert("x"));
        assert_eq!(panel.commit_draft(&key), "fix");
        assert!(panel.commit_input_mut(&key).delete(false));
        assert_eq!(panel.commit_draft(&key), "fi");
        // Take leaves emptiness behind; restore puts a failed message
        // back, but never an empty one.
        assert_eq!(panel.take_commit_draft(&key), "fi");
        assert_eq!(panel.commit_draft(&key), "");
        assert!(!panel.commit_input_mut(&key).delete(false));
        panel.restore_commit_draft(&key, String::new());
        assert_eq!(panel.commit_draft(&key), "");
        panel.restore_commit_draft(&key, "retry me".into());
        assert_eq!(panel.commit_draft(&key), "retry me");
        // Clearing the project drops the draft with everything else.
        panel.clear_project(&key);
        assert_eq!(panel.commit_draft(&key), "");
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
    fn stash_drop_dialog_cancellation_and_stale_context_never_confirm() {
        let project = ProjectId::new();
        let repo = RepoKey::root(project);
        for answer in [None, Some(0), Some(2)] {
            assert!(!GitPanel::stash_drop_confirmed(
                answer,
                project,
                &repo,
                Some(project),
                &repo,
                false
            ));
        }
        assert!(GitPanel::stash_drop_confirmed(
            Some(1),
            project,
            &repo,
            Some(project),
            &repo,
            false
        ));
        // Wrong project: dialog went stale across a project switch.
        assert!(!GitPanel::stash_drop_confirmed(
            Some(1),
            project,
            &repo,
            Some(ProjectId::new()),
            &repo,
            false
        ));
        assert!(!GitPanel::stash_drop_confirmed(
            Some(1),
            project,
            &repo,
            None,
            &repo,
            false
        ));
        // Stale repo: the active repo moved (multi-repo switch/rescan)
        // while the dialog was open — never drop against another reflog.
        let other = RepoKey::named(project, "other");
        assert!(!GitPanel::stash_drop_confirmed(
            Some(1),
            project,
            &repo,
            Some(project),
            &other,
            false
        ));
        // Shutdown: the view is tearing down.
        assert!(!GitPanel::stash_drop_confirmed(
            Some(1),
            project,
            &repo,
            Some(project),
            &repo,
            true
        ));
    }

    #[test]
    fn move_selection_walks_flat_rows_clamped() {
        let key = RepoKey::root(ProjectId::new());
        let mut panel = GitPanel::default();
        assert_eq!(panel.move_selection(&key, 1), None);
        panel.apply_refresh(
            &key,
            GitRefresh {
                worker: std::thread::current().id(),
                result: Ok(status_with(1, 1, 1)),
            },
        );
        // Nothing selected: positive lands first, negative lands last.
        assert_eq!(
            panel.move_selection(&key, 1).map(|row| row.path),
            Some(PathBuf::from("s0"))
        );
        panel.selected.remove(&key);
        assert_eq!(
            panel.move_selection(&key, -1).map(|row| row.path),
            Some(PathBuf::from("n0"))
        );
        // Walk and clamp at both ends.
        panel.selected.remove(&key);
        panel.move_selection(&key, 1);
        assert_eq!(panel.selected_path(&key), Some(&PathBuf::from("s0")));
        panel.move_selection(&key, 1);
        assert_eq!(panel.selected_path(&key), Some(&PathBuf::from("u0")));
        panel.move_selection(&key, 1);
        panel.move_selection(&key, 1);
        assert_eq!(panel.selected_path(&key), Some(&PathBuf::from("n0")));
        panel.move_selection(&key, -10);
        assert_eq!(panel.selected_path(&key), Some(&PathBuf::from("s0")));
    }

    #[test]
    fn stash_group_mode_hides_empty_shows_push_when_dirty() {
        use StashGroupMode::{Full, Hidden, PushOnly};
        // Empty list: hidden when clean, push-only when dirty.
        assert_eq!(stash_group_mode(false, Some(0)), Hidden);
        assert_eq!(stash_group_mode(true, Some(0)), PushOnly);
        // Unloaded list: same as empty (fetch happens silently).
        assert_eq!(stash_group_mode(false, None), Hidden);
        assert_eq!(stash_group_mode(true, None), PushOnly);
        // Any entries: full regardless of dirtiness.
        assert_eq!(stash_group_mode(false, Some(1)), Full);
        assert_eq!(stash_group_mode(true, Some(3)), Full);
    }

    #[test]
    fn group_collapse_toggles_per_repo_and_side() {
        let project = ProjectId::new();
        let key = RepoKey::root(project);
        let other = RepoKey::root(ProjectId::new());
        let named = RepoKey::named(project, "api");
        let mut panel = GitPanel::default();
        assert!(!panel.is_collapsed(&key, true));
        assert!(panel.toggle_collapsed(&key, true));
        assert!(panel.is_collapsed(&key, true));
        assert!(!panel.is_collapsed(&key, false));
        assert!(!panel.is_collapsed(&other, true));
        assert!(!panel.is_collapsed(&named, true));
        assert!(!panel.toggle_collapsed(&key, true));
        assert!(!panel.is_collapsed(&key, true));
        panel.toggle_collapsed(&key, false);
        panel.clear_project(&key);
        assert!(!panel.is_collapsed(&key, false));
        // A sibling repo's collapse state survives its neighbour's clear.
        assert!(panel.toggle_collapsed(&named, true));
        panel.clear_project(&key);
        assert!(panel.is_collapsed(&named, true));
    }

    /// M20: section state is per repository, so two repos under one
    /// project never share a status, selection, or draft.
    #[test]
    fn per_repo_state_is_isolated() {
        let project = ProjectId::new();
        let root = RepoKey::root(project);
        let api = RepoKey::named(project, "api");
        let web = RepoKey::named(project, "web");
        let mut panel = GitPanel::default();
        let ok = |staged: usize| GitRefresh {
            worker: std::thread::current().id(),
            result: Ok(status_with(staged, 0, 0)),
        };
        panel.apply_refresh(&api, ok(2));
        panel.apply_refresh(&web, ok(5));
        assert_eq!(panel.rows_for(&api).len(), 2);
        assert_eq!(panel.rows_for(&web).len(), 5);
        panel.select(&api, PathBuf::from("a.txt"));
        assert_eq!(panel.selected_path(&api), Some(&PathBuf::from("a.txt")));
        assert!(panel.selected_path(&web).is_none());
        assert!(panel.status_for(&root).is_none());
        // Clearing one repo leaves the others intact.
        panel.clear_project(&api);
        assert!(panel.status_for(&api).is_none());
        assert_eq!(panel.rows_for(&web).len(), 5);
        // Commit drafts are per repo too.
        assert!(panel.commit_input_mut(&web).insert("wip"));
        assert_eq!(panel.commit_draft(&web), "wip");
        assert!(panel.commit_input_mut(&api).insert("api"));
        assert_eq!(panel.commit_draft(&api), "api");
        assert_eq!(panel.take_commit_draft(&web), "wip");
        assert_eq!(panel.commit_draft(&web), "");
        assert_eq!(panel.commit_draft(&api), "api");
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
        let key = RepoKey::root(project);
        // M20: the explicit repo root is the polled repository; the pin
        // and shell CWD stay as the fallback for the project root itself.
        spawn_status_thread(
            caller,
            StatusRequest {
                key: key.clone(),
                generation: 7,
                repo_root: Some(repo.clone()),
                pinned: Some(repo.clone()),
                active_cwd: None,
                limit: 5000,
            },
            tx,
        );
        let (generation, landed_key, refresh) = rx
            .recv_timeout(Duration::from_secs(30))
            .expect("worker must answer");
        assert_eq!((generation, landed_key), (7, key));
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
        let key = RepoKey::root(project);
        spawn_status_thread(
            caller,
            StatusRequest {
                key,
                generation: 8,
                repo_root: Some(plain.clone()),
                pinned: Some(plain.clone()),
                active_cwd: None,
                limit: 5000,
            },
            tx,
        );
        let (_, _, refresh) = rx.recv_timeout(Duration::from_secs(30)).unwrap();
        assert_ne!(refresh.worker, caller);
        assert_eq!(refresh.result, Err(GitEmpty::NotRepo));

        // M20: a repo override outside the project root is refused by the
        // directory gate rather than polled silently.
        let outside =
            std::env::temp_dir().join(format!("omaterm-m20-outside-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&outside);
        std::fs::create_dir_all(&outside).unwrap();
        std::fs::create_dir_all(outside.join(".git")).unwrap();
        let (tx, rx) = std::sync::mpsc::channel();
        let named = RepoKey::named(project, "api");
        spawn_status_thread(
            caller,
            StatusRequest {
                key: named,
                generation: 9,
                repo_root: None,
                pinned: Some(plain.clone()),
                active_cwd: None,
                limit: 5000,
            },
            tx,
        );
        let (_, _, refresh) = rx.recv_timeout(Duration::from_secs(30)).unwrap();
        assert_eq!(
            refresh.result,
            Err(GitEmpty::NotRepo),
            "a non-repo project root keeps the explicit empty state"
        );
        assert!(outside.join(".git").is_dir());
        let _ = std::fs::remove_dir_all(&outside);

        let _ = std::fs::remove_dir_all(&repo);
        let _ = std::fs::remove_dir_all(&plain);
    }
}
