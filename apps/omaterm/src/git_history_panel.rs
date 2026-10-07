//! Git history Graph state: per-project commit pages, expansion, changed-file
//! metadata and selection for the GRAPH section inside the Git tab.
//!
//! GPUI-free. Rendering and worker ownership live in `main.rs`, which drains
//! the channels below on its 250ms poller: every git subprocess (root
//! resolution's `rev-parse`, `log`, `rev-list`, `diff --raw`) runs on a
//! background thread spawned through [`spawn_history_thread`] and
//! [`spawn_commit_files_thread`], never on the UI thread (M14 acceptance).
//! This panel only tracks last-good pages, explicit empty/error states,
//! expanded commits, lazy file metadata and the selected row.
//!
//! Page payloads are `omaterm_core::GitHistoryPage` (newest-first summaries,
//! accurate `has_more`); expanded file listings are
//! `omaterm_core::GitCommitFiles` against the commit's first parent (or the
//! resolved empty tree for root commits). Merge parent switching is a
//! recorded follow-up: every comparison here names its base explicitly so a
//! selector can reuse the same states.

use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::thread::ThreadId;
use std::time::{Duration, Instant};

use omaterm_core::{
    GitCommitFiles, GitCommitSummary, GitComparisonBase, GitHistoryPage, GitHistoryScope,
    GitObjectId, ProjectId,
};

/// Commits requested for the first Graph page (plan budget: 50 default).
pub const HISTORY_PAGE_SIZE: usize = 50;
/// Commits requested after `Load more` (context item cap; reaching it with
/// `has_more` set renders the explicit limit notice).
pub const HISTORY_MAX_LOADED: usize = 100;
/// Commit rows rendered at most; the section footer names the truncation.
pub const MAX_HISTORY_RENDER_COMMITS: usize = 100;
/// Expanded file rows rendered per commit at most; expansion names the rest.
pub const MAX_HISTORY_RENDER_FILES: usize = 150;
/// Failure detail retained per request (renderers truncate further).
pub const MAX_HISTORY_ERROR_CHARS: usize = 256;

/// Explicit non-data state for a project's Graph. `NoCommits` is the unborn
/// HEAD state, never an error; failures name the cause without paths or
/// contents (blueprint §45).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum HistoryEmpty {
    NoRoot,
    NotRepo,
    NoCommits,
    Unavailable(String),
    Failed(String),
}

/// Outcome of one background history-page fetch, tagged with the worker
/// thread that ran git. The UI thread drains these; it never spawns git
/// itself — the `worker != caller` test below pins that contract.
#[derive(Debug)]
pub struct HistoryRefresh {
    pub worker: ThreadId,
    pub result: Result<GitHistoryPage, HistoryEmpty>,
}

/// Outcome of one background changed-file fetch for an expanded commit.
/// `commit` echoes the request so stale landings drop even when the
/// generation is current (expansion toggled twice quickly).
#[derive(Debug)]
pub struct CommitFilesRefresh {
    pub worker: ThreadId,
    pub commit: GitObjectId,
    pub result: Result<GitCommitFiles, String>,
}

/// Lazy changed-file metadata for one expanded commit.
#[derive(Debug, Clone)]
pub enum CommitFilesState {
    Loading,
    Loaded(GitCommitFiles),
    Failed(String),
}

/// One visible Graph row: a commit, or a changed file inside an expansion.
/// File rows carry the commit OID so keyboard walks stay unambiguous.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum HistoryRow {
    Commit(String),
    File { commit: String, path: PathBuf },
}

#[derive(Default)]
struct ProjectHistory {
    scope: GitHistoryScope,
    commits: Vec<GitCommitSummary>,
    has_more: bool,
    truncated: bool,
    /// Currently loaded limit (`HISTORY_PAGE_SIZE`, then `HISTORY_MAX_LOADED`).
    limit: usize,
    loading_more: bool,
    /// Set when a refresh fails while last-good rows exist; cleared on
    /// success. The section keeps rendering rows with a small notice.
    last_error: Option<String>,
    empty: Option<HistoryEmpty>,
    expanded: HashSet<String>,
    files: HashMap<String, CommitFilesState>,
    selected: Option<HistoryRow>,
    graph_collapsed: bool,
}

#[derive(Default)]
pub struct HistoryPanel {
    projects: HashMap<ProjectId, ProjectHistory>,
}

impl HistoryPanel {
    fn project_mut(&mut self, project: ProjectId) -> &mut ProjectHistory {
        self.projects.entry(project).or_default()
    }

    fn project(&self, project: ProjectId) -> Option<&ProjectHistory> {
        self.projects.get(&project)
    }

    /// Drop cached state for a project (switch-away memory bound is owned
    /// by the caller; this clears on demand).
    pub fn clear_project(&mut self, project: ProjectId) {
        self.projects.remove(&project);
    }

    /// Explicit empty/error state for a project, if any.
    pub fn empty_for(&self, project: ProjectId) -> Option<&HistoryEmpty> {
        self.project(project)?.empty.as_ref()
    }

    /// Last-good commits, newest-first. Empty while loading or when the
    /// repository has no commits yet (see [`HistoryPanel::empty_for`]).
    pub fn commits_for(&self, project: ProjectId) -> &[GitCommitSummary] {
        self.project(project)
            .map(|state| state.commits.as_slice())
            .unwrap_or_default()
    }

    /// Whether another page exists beyond the loaded commits.
    pub fn has_more(&self, project: ProjectId) -> bool {
        self.project(project).is_some_and(|state| state.has_more)
    }

    /// Whether the loaded page hit a byte/item cap (`truncated`).
    pub fn truncated(&self, project: ProjectId) -> bool {
        self.project(project).is_some_and(|state| state.truncated)
    }

    /// Currently loaded limit for a project (0 when never fetched).
    pub fn loaded_limit(&self, project: ProjectId) -> usize {
        self.project(project).map(|state| state.limit).unwrap_or(0)
    }

    /// Whether a `Load more` request is in flight.
    pub fn loading_more(&self, project: ProjectId) -> bool {
        self.project(project)
            .is_some_and(|state| state.loading_more)
    }

    /// `Load more` was consumed and the backend still reports more: the
    /// section renders the explicit loaded-history limit notice.
    pub fn limit_reached(&self, project: ProjectId) -> bool {
        self.project(project).is_some_and(|state| {
            state.limit >= HISTORY_MAX_LOADED && state.has_more && !state.loading_more
        })
    }

    /// Non-fatal refresh failure kept alongside last-good rows.
    pub fn last_error(&self, project: ProjectId) -> Option<&str> {
        self.project(project)
            .and_then(|state| state.last_error.as_deref())
    }

    /// Functional scope for a project (current HEAD by default).
    pub fn scope_for(&self, project: ProjectId) -> GitHistoryScope {
        self.project(project)
            .map(|state| state.scope)
            .unwrap_or_default()
    }

    /// Switch scope and invalidate the page so the poller refetches.
    /// Returns true when the scope changed.
    pub fn set_scope(&mut self, project: ProjectId, scope: GitHistoryScope) -> bool {
        let state = self.project_mut(project);
        if state.scope == scope {
            return false;
        }
        state.scope = scope;
        state.commits.clear();
        state.expanded.clear();
        state.files.clear();
        state.selected = None;
        state.empty = None;
        state.last_error = None;
        state.has_more = false;
        state.truncated = false;
        state.limit = 0;
        state.loading_more = false;
        true
    }

    /// Whether the GRAPH section is collapsed (project-local, view-only).
    pub fn is_graph_collapsed(&self, project: ProjectId) -> bool {
        self.project(project)
            .is_some_and(|state| state.graph_collapsed)
    }

    /// Toggle the GRAPH section collapse. Returns the new collapsed state.
    pub fn toggle_graph_collapsed(&mut self, project: ProjectId) -> bool {
        let state = self.project_mut(project);
        state.graph_collapsed = !state.graph_collapsed;
        state.graph_collapsed
    }

    /// Whether a commit's files are expanded.
    pub fn is_expanded(&self, project: ProjectId, commit: &str) -> bool {
        self.project(project)
            .is_some_and(|state| state.expanded.contains(commit))
    }

    /// Toggle a commit's expansion. Returns true when the caller must fetch
    /// file metadata (newly expanded without cached files).
    pub fn toggle_expanded(&mut self, project: ProjectId, commit: GitObjectId) -> bool {
        let key = commit.as_str().to_owned();
        let state = self.project_mut(project);
        if state.expanded.remove(&key) {
            return false;
        }
        state.expanded.insert(key.clone());
        if state
            .selected
            .as_ref()
            .is_none_or(|row| !row_belongs_to(row, &key))
        {
            state.selected = Some(HistoryRow::Commit(key.clone()));
        }
        !matches!(
            state.files.get(&key),
            Some(CommitFilesState::Loaded(_) | CommitFilesState::Loading)
        )
    }

    /// Changed-file metadata for an expanded commit, if landed.
    pub fn files_for(&self, project: ProjectId, commit: &str) -> Option<&CommitFilesState> {
        self.project(project)?.files.get(commit)
    }

    /// Mark an expansion loading (spawned fetch) without dropping last-good
    /// files on retry.
    pub fn mark_files_loading(&mut self, project: ProjectId, commit: &GitObjectId) {
        let key = commit.as_str().to_owned();
        let state = self.project_mut(project);
        if !matches!(state.files.get(&key), Some(CommitFilesState::Loaded(_))) {
            state.files.insert(key, CommitFilesState::Loading);
        }
    }

    /// Currently selected Graph row, if any.
    pub fn selected_row(&self, project: ProjectId) -> Option<&HistoryRow> {
        self.project(project)?.selected.as_ref()
    }

    pub fn select(&mut self, project: ProjectId, row: HistoryRow) {
        self.project_mut(project).selected = Some(row);
    }

    /// Drop the Graph row selection so keyboard navigation can hand the
    /// cursor back to the change lists (single cursor across the Git tab).
    pub fn clear_selection(&mut self, project: ProjectId) {
        self.project_mut(project).selected = None;
    }

    /// Flat visible rows in render order (commit, then its file rows when
    /// expanded with loaded metadata). Drives keyboard navigation.
    pub fn rows_for(&self, project: ProjectId) -> Vec<HistoryRow> {
        let Some(state) = self.project(project) else {
            return Vec::new();
        };
        let mut rows = Vec::new();
        for commit in state.commits.iter().take(MAX_HISTORY_RENDER_COMMITS) {
            let key = commit.id.as_str().to_owned();
            rows.push(HistoryRow::Commit(key.clone()));
            if !state.expanded.contains(&key) {
                continue;
            }
            if let Some(CommitFilesState::Loaded(files)) = state.files.get(&key) {
                rows.extend(
                    files
                        .files
                        .iter()
                        .take(MAX_HISTORY_RENDER_FILES)
                        .map(|file| HistoryRow::File {
                            commit: key.clone(),
                            path: file.path.clone(),
                        }),
                );
            }
        }
        rows
    }

    /// Move the row selection by `delta` (clamped, no wrap). Selects the
    /// first row when nothing is selected. Returns the newly selected row.
    pub fn move_selection(&mut self, project: ProjectId, delta: i32) -> Option<HistoryRow> {
        let rows = self.rows_for(project);
        if rows.is_empty() {
            return None;
        }
        let next = match self
            .project(project)
            .and_then(|state| state.selected.clone())
            .and_then(|selected| rows.iter().position(|row| row == &selected))
        {
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
        self.project_mut(project).selected = Some(row.clone());
        Some(row)
    }

    /// Record a landed page fetch. Success replaces rows but preserves
    /// surviving expansions, file metadata and selection; failure keeps
    /// last-good rows with a notice (or records the empty state when
    /// nothing was ever loaded).
    pub fn apply_history_refresh(&mut self, project: ProjectId, refresh: HistoryRefresh) {
        let state = self.project_mut(project);
        match refresh.result {
            Ok(page) => {
                state.commits = page.commits;
                state.has_more = page.has_more;
                state.truncated = page.truncated;
                state.loading_more = false;
                state.last_error = None;
                if state.commits.is_empty() {
                    state.empty = Some(HistoryEmpty::NoCommits);
                } else {
                    state.empty = None;
                }
                state
                    .expanded
                    .retain(|key| state.commits.iter().any(|commit| commit.id.as_str() == key));
                state.files.retain(|key, _| state.expanded.contains(key));
                if let Some(selected) = state.selected.clone()
                    && !self_row_visible(state, &selected)
                {
                    state.selected = None;
                }
            }
            Err(empty) => {
                state.loading_more = false;
                if state.commits.is_empty() {
                    state.empty = Some(empty);
                } else {
                    let detail = match &empty {
                        HistoryEmpty::Unavailable(detail) | HistoryEmpty::Failed(detail) => {
                            detail.clone()
                        }
                        HistoryEmpty::NoRoot => "no project root".to_owned(),
                        HistoryEmpty::NotRepo => "not a git repository".to_owned(),
                        HistoryEmpty::NoCommits => "no commits yet".to_owned(),
                    };
                    state.last_error = Some(detail);
                }
            }
        }
    }

    /// Record a landed file-metadata fetch. Applies only while the commit
    /// is still expanded; a collapsed-then-landing result is dropped so a
    /// stale retry cannot reopen a row the user closed.
    pub fn apply_files_refresh(&mut self, project: ProjectId, refresh: CommitFilesRefresh) {
        let key = refresh.commit.as_str().to_owned();
        let state = self.project_mut(project);
        if !state.expanded.contains(&key) {
            return;
        }
        match refresh.result {
            Ok(files) => {
                state.files.insert(key, CommitFilesState::Loaded(files));
            }
            Err(detail) => {
                state.files.insert(key, CommitFilesState::Failed(detail));
            }
        }
    }

    /// Note the limit a page fetch was issued with (drives `Load more` and
    /// the limit notice). Called when the poller spawns the worker.
    pub fn note_fetch_limit(&mut self, project: ProjectId, limit: usize) {
        let state = self.project_mut(project);
        state.limit = limit;
        if limit > HISTORY_PAGE_SIZE {
            state.loading_more = true;
        }
    }

    /// Whether the section has any page content or explicit state to show
    /// (loading counts: the section renders a loading row, never nothing).
    pub fn known(&self, project: ProjectId) -> bool {
        self.project(project)
            .is_some_and(|state| !state.commits.is_empty() || state.empty.is_some())
    }
}

fn row_belongs_to(row: &HistoryRow, commit: &str) -> bool {
    match row {
        HistoryRow::Commit(key) => key == commit,
        HistoryRow::File { commit: key, .. } => key == commit,
    }
}

fn self_row_visible(state: &ProjectHistory, row: &HistoryRow) -> bool {
    match row {
        HistoryRow::Commit(key) => state.commits.iter().any(|commit| commit.id.as_str() == key),
        HistoryRow::File { commit, path } => {
            state.expanded.contains(commit)
                && state
                    .files
                    .get(commit)
                    .and_then(|files| match files {
                        CommitFilesState::Loaded(loaded) => {
                            Some(loaded.files.iter().any(|file| &file.path == path))
                        }
                        _ => None,
                    })
                    .unwrap_or(false)
        }
    }
}

/// Short relative age for a commit row (`committer` time is the list's
/// date; author time is labeled in details). Pure and unit-tested.
pub fn relative_time(unix_seconds: i64, now_seconds: i64) -> String {
    let age = now_seconds.saturating_sub(unix_seconds).max(0);
    const MINUTE: i64 = 60;
    const HOUR: i64 = 60 * MINUTE;
    const DAY: i64 = 24 * HOUR;
    const WEEK: i64 = 7 * DAY;
    const MONTH: i64 = 30 * DAY;
    const YEAR: i64 = 365 * DAY;
    if age < MINUTE {
        return "just now".to_owned();
    }
    let (count, unit) = if age < HOUR {
        (age / MINUTE, "m")
    } else if age < DAY {
        (age / HOUR, "h")
    } else if age < WEEK {
        (age / DAY, "d")
    } else if age < MONTH {
        (age / WEEK, "w")
    } else if age < YEAR {
        (age / MONTH, "mo")
    } else {
        (age / YEAR, "y")
    };
    format!("{count}{unit} ago")
}

/// Decide whether the poller should spawn a history fetch: first sight of
/// a project, an explicit dirty hint (manual refresh, scope switch, commit
/// effects), or the configured interval elapsing. Reuses the status cadence
/// predicate; pure and unit-tested through `git_panel::should_refresh`.
pub use crate::git_panel::should_refresh;

/// Page-fetch options: scope roots plus the row limit. Bundled so the
/// worker spawn stays under the argument cap.
#[derive(Debug, Clone, Copy)]
pub struct HistoryFetch {
    pub scope: GitHistoryScope,
    pub limit: usize,
}

/// Spawn the background history-page worker. The worker resolves the M12
/// root off-thread and runs `git log`; the result carries the worker thread
/// id so tests pin the off-UI-thread contract without timing flakes.
pub fn spawn_history_thread(
    caller: ThreadId,
    project: ProjectId,
    generation: u64,
    pinned: Option<PathBuf>,
    active_cwd: Option<PathBuf>,
    fetch: HistoryFetch,
    tx: std::sync::mpsc::Sender<(u64, ProjectId, HistoryRefresh)>,
) {
    std::thread::spawn(move || {
        let worker = std::thread::current().id();
        debug_assert_ne!(worker, caller, "history worker must not be the caller");
        let result = history_off_thread(
            pinned.as_deref(),
            active_cwd.as_deref(),
            fetch.scope,
            fetch.limit,
        );
        let _ = tx.send((generation, project, HistoryRefresh { worker, result }));
    });
}

/// Spawn the background changed-file worker for one expanded commit. The
/// comparison uses the commit's first parent (empty tree for roots);
/// merge parent selection reuses this entry with an explicit base.
pub fn spawn_commit_files_thread(
    caller: ThreadId,
    project: ProjectId,
    generation: u64,
    pinned: Option<PathBuf>,
    active_cwd: Option<PathBuf>,
    commit: GitObjectId,
    tx: std::sync::mpsc::Sender<(u64, ProjectId, CommitFilesRefresh)>,
) {
    std::thread::spawn(move || {
        let worker = std::thread::current().id();
        debug_assert_ne!(worker, caller, "history worker must not be the caller");
        let result = commit_files_off_thread(pinned.as_deref(), active_cwd.as_deref(), &commit);
        let _ = tx.send((
            generation,
            project,
            CommitFilesRefresh {
                worker,
                commit,
                result,
            },
        ));
    });
}

fn history_off_thread(
    pinned: Option<&std::path::Path>,
    active_cwd: Option<&std::path::Path>,
    scope: GitHistoryScope,
    limit: usize,
) -> Result<GitHistoryPage, HistoryEmpty> {
    let resolved = omaterm_context::resolve_root(pinned, active_cwd);
    let Some(root) = resolved.root else {
        return Err(HistoryEmpty::NoRoot);
    };
    match omaterm_context::git_history(&root, scope, limit) {
        Ok(page) => Ok(page),
        Err(omaterm_context::GitError::NotARepo) => Err(HistoryEmpty::NotRepo),
        Err(omaterm_context::GitError::GitUnavailable(message)) => {
            Err(HistoryEmpty::Unavailable(bound_detail(message)))
        }
        Err(omaterm_context::GitError::Timeout) => {
            Err(HistoryEmpty::Failed("git log timed out".into()))
        }
        Err(omaterm_context::GitError::Cancelled) => {
            Err(HistoryEmpty::Failed("git log cancelled".into()))
        }
        Err(omaterm_context::GitError::GitFailed(message)) => {
            Err(HistoryEmpty::Failed(bound_detail(message)))
        }
        Err(omaterm_context::GitError::PathOutsideRoot) => {
            Err(HistoryEmpty::Failed("path escapes the project root".into()))
        }
        // Unreachable on the read-only history path (kept for exhaustiveness).
        Err(
            omaterm_context::GitError::DirtyWorktree
            | omaterm_context::GitError::CurrentBranch
            | omaterm_context::GitError::AuthFailed
            | omaterm_context::GitError::Offline
            | omaterm_context::GitError::Diverged
            | omaterm_context::GitError::NonFastForward
            | omaterm_context::GitError::NoUpstream,
        ) => Err(HistoryEmpty::Failed("branch state blocks git log".into())),
        Err(omaterm_context::GitError::Io(error)) => {
            Err(HistoryEmpty::Failed(bound_detail(error.to_string())))
        }
    }
}

fn commit_files_off_thread(
    pinned: Option<&std::path::Path>,
    active_cwd: Option<&std::path::Path>,
    commit: &GitObjectId,
) -> Result<GitCommitFiles, String> {
    let resolved = omaterm_context::resolve_root(pinned, active_cwd);
    let Some(root) = resolved.root else {
        return Err("no project root".to_owned());
    };
    let parents = omaterm_context::git_commit_parents(&root, commit).map_err(|error| {
        bound_detail(match error {
            omaterm_context::GitError::GitFailed(message) => message,
            omaterm_context::GitError::NotARepo => "not a git repository".to_owned(),
            omaterm_context::GitError::Timeout => "git log timed out".to_owned(),
            other => other.to_string(),
        })
    })?;
    let base = match parents.first() {
        Some(parent) => GitComparisonBase::Parent(parent.clone()),
        None => GitComparisonBase::EmptyTree,
    };
    omaterm_context::git_commit_files(&root, commit, &base).map_err(|error| {
        bound_detail(match error {
            omaterm_context::GitError::GitFailed(message) => message,
            omaterm_context::GitError::Timeout => "git diff timed out".to_owned(),
            other => other.to_string(),
        })
    })
}

fn bound_detail(detail: String) -> String {
    if detail.chars().count() <= MAX_HISTORY_ERROR_CHARS {
        detail
    } else {
        detail.chars().take(MAX_HISTORY_ERROR_CHARS).collect()
    }
}

/// Shared poller cadence helper: first sight, dirty hint, or interval.
/// Thin wrapper so main.rs reads one history-specific name in the tick.
pub fn history_should_refresh(
    known: bool,
    dirty_hint: bool,
    last: Option<Instant>,
    interval: Duration,
    now: Instant,
) -> bool {
    should_refresh(known, dirty_hint, last, interval, now)
}

#[cfg(test)]
mod tests {
    use super::*;
    use omaterm_core::{GitCommitFile, GitCommitFileKind, GitRef, GitTimestamp};

    fn summary(id: char, subject: &str, parents: &str) -> GitCommitSummary {
        let oid = |c: char| GitObjectId::parse(c.to_string().repeat(40)).expect("fixture oid");
        GitCommitSummary {
            id: oid(id),
            parents: if parents.is_empty() {
                Vec::new()
            } else {
                vec![oid(parents.chars().next().expect("parent fixture"))]
            },
            author_name: "Ada".into(),
            author_email: "ada@example.test".into(),
            author_time: GitTimestamp {
                unix_seconds: 10,
                offset_minutes: 0,
            },
            subject: subject.into(),
            refs: Vec::<GitRef>::new(),
            body: String::new(),
            shallow_boundary: false,
        }
    }

    fn page(second: GitCommitSummary, first: GitCommitSummary) -> GitHistoryPage {
        GitHistoryPage {
            commits: vec![second, first],
            has_more: false,
            truncated: false,
        }
    }

    fn loaded_files(commit: char) -> GitCommitFiles {
        let oid = |c: char| GitObjectId::parse(c.to_string().repeat(40)).expect("fixture oid");
        GitCommitFiles {
            commit: oid(commit),
            base: GitComparisonBase::EmptyTree,
            files: vec![GitCommitFile {
                path: PathBuf::from("a.txt"),
                old_path: None,
                kind: GitCommitFileKind::Added,
                old_mode: None,
                new_mode: Some(0o100644),
            }],
            truncated: false,
        }
    }

    #[test]
    fn history_page_expand_files_and_selection_walk() {
        let project = ProjectId::new();
        let mut panel = HistoryPanel::default();
        assert!(panel.rows_for(project).is_empty());
        assert!(panel.move_selection(project, 1).is_none());

        panel.apply_history_refresh(
            project,
            HistoryRefresh {
                worker: std::thread::current().id(),
                result: Ok(page(summary('b', "second", "a"), summary('a', "first", ""))),
            },
        );
        assert!(panel.empty_for(project).is_none());
        assert!(!panel.has_more(project));

        // Newest-first rows; nothing selected lands on the first commit.
        let rows = panel.rows_for(project);
        assert_eq!(rows.len(), 2);
        assert_eq!(
            panel.move_selection(project, 1),
            Some(HistoryRow::Commit("b".repeat(40)))
        );

        // Expanding without metadata needs a fetch; the landed files join
        // the walk only while the commit stays expanded.
        let commit = GitObjectId::parse("b".repeat(40)).unwrap();
        assert!(panel.toggle_expanded(project, commit.clone()));
        panel.mark_files_loading(project, &commit);
        assert!(matches!(
            panel.files_for(project, &"b".repeat(40)),
            Some(CommitFilesState::Loading)
        ));
        panel.apply_files_refresh(
            project,
            CommitFilesRefresh {
                worker: std::thread::current().id(),
                commit: commit.clone(),
                result: Ok(loaded_files('b')),
            },
        );
        let rows = panel.rows_for(project);
        assert_eq!(rows.len(), 3);
        assert_eq!(
            rows[1],
            HistoryRow::File {
                commit: "b".repeat(40),
                path: PathBuf::from("a.txt"),
            }
        );

        // Walk clamps at both ends: one step reaches the file row, two
        // more land on (and clamp to) the last commit.
        panel.move_selection(project, 1);
        assert_eq!(
            panel.selected_row(project),
            Some(&HistoryRow::File {
                commit: "b".repeat(40),
                path: PathBuf::from("a.txt"),
            })
        );
        panel.move_selection(project, 1);
        panel.move_selection(project, 1);
        assert_eq!(
            panel.selected_row(project),
            Some(&HistoryRow::Commit("a".repeat(40)))
        );
        panel.move_selection(project, -10);
        assert_eq!(
            panel.selected_row(project),
            Some(&HistoryRow::Commit("b".repeat(40)))
        );
        assert!(!panel.toggle_expanded(project, commit.clone()));
        assert_eq!(panel.rows_for(project).len(), 2);
        panel.apply_files_refresh(
            project,
            CommitFilesRefresh {
                worker: std::thread::current().id(),
                commit: commit.clone(),
                result: Err("stale".into()),
            },
        );
        assert!(matches!(
            panel.files_for(project, &"b".repeat(40)),
            Some(CommitFilesState::Loaded(_))
        ));

        // Retry after a real failure keeps the row expandable.
        panel.toggle_expanded(project, commit.clone());
        panel.apply_files_refresh(
            project,
            CommitFilesRefresh {
                worker: std::thread::current().id(),
                commit,
                result: Err("git diff timed out".into()),
            },
        );
        assert!(matches!(
            panel.files_for(project, &"b".repeat(40)),
            Some(CommitFilesState::Failed(_))
        ));
        assert_eq!(panel.rows_for(project).len(), 2);
    }

    #[test]
    fn refresh_preserves_survivors_and_keeps_last_good_on_error() {
        let project = ProjectId::new();
        let mut panel = HistoryPanel::default();
        panel.apply_history_refresh(
            project,
            HistoryRefresh {
                worker: std::thread::current().id(),
                result: Ok(page(summary('b', "second", "a"), summary('a', "first", ""))),
            },
        );
        let commit = GitObjectId::parse("b".repeat(40)).unwrap();
        panel.toggle_expanded(project, commit.clone());
        panel.apply_files_refresh(
            project,
            CommitFilesRefresh {
                worker: std::thread::current().id(),
                commit,
                result: Ok(loaded_files('b')),
            },
        );
        panel.select(
            project,
            HistoryRow::File {
                commit: "b".repeat(40),
                path: PathBuf::from("a.txt"),
            },
        );

        // A failed refresh keeps rows, expansion and selection with a notice.
        panel.apply_history_refresh(
            project,
            HistoryRefresh {
                worker: std::thread::current().id(),
                result: Err(HistoryEmpty::Failed("git log timed out".into())),
            },
        );
        assert_eq!(panel.rows_for(project).len(), 3);
        assert!(panel.last_error(project).is_some());
        assert!(panel.selected_row(project).is_some());

        // A successful refresh drops vanished commits and their metadata.
        panel.apply_history_refresh(
            project,
            HistoryRefresh {
                worker: std::thread::current().id(),
                result: Ok(GitHistoryPage {
                    commits: vec![summary('a', "first", "")],
                    has_more: true,
                    truncated: false,
                }),
            },
        );
        assert!(panel.last_error(project).is_none());
        assert!(panel.has_more(project));
        assert_eq!(panel.rows_for(project).len(), 1);
        assert!(panel.selected_row(project).is_none());
    }

    #[test]
    fn scope_switch_and_graph_collapse_are_project_local() {
        let project = ProjectId::new();
        let other = ProjectId::new();
        let mut panel = HistoryPanel::default();
        assert_eq!(panel.scope_for(project), GitHistoryScope::CurrentHead);
        assert!(!panel.is_graph_collapsed(project));

        assert!(panel.toggle_graph_collapsed(project));
        assert!(panel.is_graph_collapsed(project));
        assert!(!panel.is_graph_collapsed(other));

        assert!(panel.set_scope(project, GitHistoryScope::AllLocalBranches));
        assert!(!panel.set_scope(project, GitHistoryScope::AllLocalBranches));
        assert_eq!(panel.loaded_limit(project), 0);
        assert!(!panel.is_graph_collapsed(other));

        panel.clear_project(project);
        assert_eq!(panel.scope_for(project), GitHistoryScope::CurrentHead);
        assert!(!panel.is_graph_collapsed(project));
    }

    #[test]
    fn relative_time_buckets_future_as_now() {
        assert_eq!(relative_time(100, 100), "just now");
        assert_eq!(relative_time(200, 100), "just now");
        assert_eq!(relative_time(0, 90), "1m ago");
        assert_eq!(relative_time(0, 3 * 3600), "3h ago");
        assert_eq!(relative_time(0, 3 * 86400), "3d ago");
        assert_eq!(relative_time(0, 14 * 86400), "2w ago");
        assert_eq!(relative_time(0, 60 * 86400), "2mo ago");
        assert_eq!(relative_time(0, 800 * 86400), "2y ago");
    }

    #[test]
    fn history_should_refresh_covers_first_sight_dirty_and_interval() {
        let now = Instant::now();
        let interval = Duration::from_secs(5);
        assert!(history_should_refresh(false, false, None, interval, now));
        assert!(history_should_refresh(true, true, Some(now), interval, now));
        assert!(!history_should_refresh(
            true,
            false,
            Some(now),
            interval,
            now
        ));
    }

    /// The off-UI-thread contract, pinned without timing flakes: history
    /// and file workers never run on the caller thread, and a real linear
    /// repository reports newest-first through both.
    #[test]
    fn history_workers_run_off_the_caller_thread() {
        let caller = std::thread::current().id();
        let repo: PathBuf =
            std::env::temp_dir().join(format!("omaterm-history-panel-{}", std::process::id()));
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
        std::fs::write(repo.join("a.txt"), b"v1\n").unwrap();
        git(&["add", "-A"]);
        git(&["commit", "-qm", "first"]);
        std::fs::write(repo.join("a.txt"), b"v2\n").unwrap();
        git(&["add", "-A"]);
        git(&["commit", "-qm", "second"]);

        let project = ProjectId::new();
        let (tx, rx) = std::sync::mpsc::channel();
        spawn_history_thread(
            caller,
            project,
            3,
            Some(repo.clone()),
            None,
            HistoryFetch {
                scope: GitHistoryScope::CurrentHead,
                limit: HISTORY_PAGE_SIZE,
            },
            tx,
        );
        let (generation, landed_project, refresh) = rx
            .recv_timeout(Duration::from_secs(30))
            .expect("worker must answer");
        assert_eq!((generation, landed_project), (3, project));
        assert_ne!(refresh.worker, caller, "git must run off the caller thread");
        let page = refresh.result.expect("history page");
        assert_eq!(page.commits.len(), 2);
        assert_eq!(page.commits[0].subject, "second");
        assert!(!page.has_more);

        let head = page.commits[0].id.clone();
        let (tx, rx) = std::sync::mpsc::channel();
        spawn_commit_files_thread(
            caller,
            project,
            4,
            Some(repo.clone()),
            None,
            head.clone(),
            tx,
        );
        let (_, _, files_refresh) = rx.recv_timeout(Duration::from_secs(30)).unwrap();
        assert_ne!(files_refresh.worker, caller);
        assert_eq!(files_refresh.commit, head);
        let files = files_refresh.result.expect("commit files");
        assert_eq!(files.files.len(), 1);
        assert_eq!(files.files[0].path, PathBuf::from("a.txt"));

        // An unborn repository reports the explicit empty state.
        let unborn: PathBuf =
            std::env::temp_dir().join(format!("omaterm-history-unborn-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&unborn);
        std::fs::create_dir_all(&unborn).unwrap();
        assert!(
            std::process::Command::new("git")
                .args(["init"])
                .current_dir(&unborn)
                .env("GIT_TERMINAL_PROMPT", "0")
                .env("GIT_CONFIG_NOSYSTEM", "1")
                .stdin(std::process::Stdio::null())
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .status()
                .expect("git must spawn")
                .success()
        );
        let (tx, rx) = std::sync::mpsc::channel();
        spawn_history_thread(
            caller,
            project,
            5,
            Some(unborn.clone()),
            None,
            HistoryFetch {
                scope: GitHistoryScope::CurrentHead,
                limit: HISTORY_PAGE_SIZE,
            },
            tx,
        );
        let (_, _, refresh) = rx.recv_timeout(Duration::from_secs(30)).unwrap();
        assert_ne!(refresh.worker, caller);
        assert!(refresh.result.expect("unborn history").commits.is_empty());
        let _ = std::fs::remove_dir_all(&repo);
        let _ = std::fs::remove_dir_all(&unborn);
    }
}
