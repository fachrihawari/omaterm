use std::path::PathBuf;

use crate::{DocumentId, PaneId, ProjectId, SessionId, SplitDirection, SplitId, TabId};

/// Semantic operations accepted by every in-process and future IPC caller.
#[derive(Debug, Clone, PartialEq)]
pub enum OmaCommand {
    Project(ProjectCommand),
    Tab(TabCommand),
    Pane(PaneCommand),
    Terminal(TerminalCommand),
    History(HistoryCommand),
    File(FileCommand),
    Editor(EditorCommand),
    Git(GitCommand),
    Diff(DiffCommand),
    Process(ProcessCommand),
}

#[derive(Debug, Clone, PartialEq)]
pub enum ProjectCommand {
    Create {
        name: Option<String>,
        directory: Option<PathBuf>,
    },
    Delete {
        project: ProjectId,
    },
    Select {
        project: ProjectId,
    },
    List,
    Rename {
        project: ProjectId,
        name: String,
    },
    /// Change the project's base directory. Only future tabs, splits, and
    /// default terminal launches use it; live sessions keep their CWD.
    SetDirectory {
        project: ProjectId,
        directory: PathBuf,
    },
    /// Resolve the project's filesystem root (M12, blueprint §31). Pure
    /// query: pin wins when set and present, else the nearest enclosing git
    /// repository of the active tab's shell CWD, else no root. No effects.
    Root {
        project: ProjectId,
    },
    /// Discover the repositories under the project root (M20): the root
    /// itself when it is a repo, else its depth-1 repo children, plus the
    /// effective active repo. Pure query, no effects.
    ListRepos {
        project: ProjectId,
    },
    /// Select the active child repository by directory name (M20). The
    /// name must match the live depth-1 scan; traversal and unknown
    /// names are rejected. Only the stored selection changes — no Git
    /// operation runs here.
    SetActiveRepo {
        project: ProjectId,
        repo: String,
    },
}

#[derive(Debug, Clone, PartialEq)]
pub enum TabCommand {
    Create {
        project: ProjectId,
        name: Option<String>,
    },
    Close {
        tab: TabId,
    },
    Select {
        tab: TabId,
    },
    List {
        project: ProjectId,
    },
    Rename {
        tab: TabId,
        name: String,
    },
}

#[derive(Debug, Clone, PartialEq)]
pub enum PaneCommand {
    Split {
        target: PaneId,
        direction: SplitDirection,
    },
    Close {
        pane: PaneId,
    },
    Focus {
        pane: PaneId,
    },
    FocusDirection {
        direction: SplitDirection,
    },
    Resize {
        split: SplitId,
        fraction: f32,
    },
    ResizeFocused {
        amount: f32,
    },
    Equalize {
        tab: TabId,
    },
    EqualizeSelected,
    List {
        tab: Option<TabId>,
    },
}

#[derive(Debug, Clone, PartialEq)]
pub enum TerminalCommand {
    Create {
        project: ProjectId,
        directory: Option<PathBuf>,
    },
    SendBytes {
        session: SessionId,
        data: Vec<u8>,
    },
    RunCommand {
        session: SessionId,
        argv: Vec<String>,
    },
    /// Rebind a restored logical pane to a fresh PTY session after startup.
    RestorePane {
        project: ProjectId,
        tab: TabId,
        pane: PaneId,
        directory: PathBuf,
        /// Program this pane last launched. Absent snapshots keep the
        /// current default (`$SHELL` on Linux, the saved Windows shell).
        shell: Option<String>,
    },
    ReadVisible {
        session: SessionId,
        max_lines: usize,
        max_columns: usize,
    },
    List,
}

/// Opt-in encrypted history operations. Every variant routes through the
/// common dispatcher so UI, IPC, and CLI share one implementation; the
/// desktop owner applies the effects. See the M10 milestone contract.
#[derive(Debug, Clone, PartialEq)]
pub enum HistoryCommand {
    EnablePersistence,
    DisablePersistence,
    PausePane { pane: PaneId },
    ResumePane { pane: PaneId },
    ListJournal { pane: PaneId, limit: usize },
    ClearPane { pane: PaneId },
    ClearProject { project: ProjectId },
    ClearWorkspace,
    Status,
}

/// File tree + `Ctrl+P` filename search (M13, blueprint §23). All three
/// variants route through the common dispatcher so sidebar, `Ctrl+P`,
/// IPC, and CLI share one implementation. `List`/`Search` are pure
/// queries; `Open` submits `$EDITOR <path>` to the focused pane through
/// the existing `terminal.run` path (the editor pane itself is v0.3).
#[derive(Debug, Clone, PartialEq)]
pub enum FileCommand {
    List {
        project: ProjectId,
        dir: Option<PathBuf>,
        limit: Option<usize>,
    },
    Search {
        project: ProjectId,
        query: String,
        limit: Option<usize>,
    },
    Open {
        project: ProjectId,
        path: PathBuf,
    },
}

/// Native built-in editor document lifecycle (M19, blueprint §34). Only
/// document open/close and explicit save/revert mutations travel through the
/// dispatcher; keystroke-level editing is UI text-input state, not a command.
/// `Open` names a root-relative path and yields a `DocumentId`; the remaining
/// variants address that ID so a root replacement can never silently retarget
/// a buffer. Terminal-routed `FileCommand::Open` keeps its existing contract.
#[derive(Debug, Clone, PartialEq)]
pub enum EditorCommand {
    Open { project: ProjectId, path: PathBuf },
    Close { document: DocumentId },
    Save { document: DocumentId },
    Revert { document: DocumentId },
}

/// VSCode-Source-Control-style git operations over the system git binary
/// (M14, blueprint §32). All variants route through the common dispatcher
/// so the sidebar panel, IPC, and CLI share one implementation.
/// `Status` is a pure query (empty envelope without a repo root, never an
/// error); `Stage`/`Unstage`/`Discard` mutate the index/worktree through
/// explicit root-relative paths only. No commit/push/pull surface exists
/// (explicit non-goal); `Discard` of untracked paths deletes them from
/// disk, which is why the desktop arms it two-step before dispatching.
#[derive(Debug, Clone, PartialEq)]
pub enum GitCommand {
    Status {
        project: ProjectId,
    },
    /// First bounded page of immutable commits reachable from the selected
    /// graph roots. Continuation cursors are introduced with the async query
    /// service; this query intentionally has no UI-specific behavior.
    History {
        project: ProjectId,
        scope: crate::GitHistoryScope,
        limit: usize,
    },
    /// Parent-specific changed-file listing for one expanded commit. `base`
    /// must be one of the commit's real parents, or the empty tree for a root.
    CommitFiles {
        project: ProjectId,
        commit: crate::GitObjectId,
        base: crate::GitComparisonBase,
    },
    Stage {
        project: ProjectId,
        paths: Vec<PathBuf>,
    },
    /// Stage one current, complete unstaged hunk. `hunk_id` originates from
    /// `diff.show`; the backend re-reads and generates the patch itself.
    StageHunk {
        project: ProjectId,
        path: PathBuf,
        hunk_id: u64,
    },
    Unstage {
        project: ProjectId,
        paths: Vec<PathBuf>,
    },
    Discard {
        project: ProjectId,
        paths: Vec<PathBuf>,
    },
    /// Commit staged changes with an explicit message. Authorship comes
    /// from the repository's git config (no author UI); amend/push stay
    /// out of scope.
    Commit {
        project: ProjectId,
        message: String,
    },
    /// Bounded local branch listing with HEAD identity (C9.1). A pinned
    /// non-repo directory returns the empty envelope, like status.
    BranchList {
        project: ProjectId,
    },
    /// Create a local branch without checking out. `start` is an optional
    /// revision (defaults to HEAD) resolved through `rev-parse --verify`.
    BranchCreate {
        project: ProjectId,
        name: String,
        start: Option<String>,
    },
    /// Check out a local branch. Refused with `dirty_worktree` when staged
    /// or unstaged changes exist (stash or discard first).
    BranchCheckout {
        project: ProjectId,
        name: String,
    },
    /// Delete a local branch. The checked-out branch is refused with
    /// `current_branch`; unmerged branches need `force` (same as `-D`).
    BranchDelete {
        project: ProjectId,
        name: String,
        force: bool,
    },
    /// Rename a branch, including the checked-out one.
    BranchRename {
        project: ProjectId,
        old: String,
        new: String,
    },
    /// Fetch from the default remote (or `remote`), pruning stale
    /// remote-tracking refs. Never touches the worktree.
    SyncFetch {
        project: ProjectId,
        remote: Option<String>,
    },
    /// Pull `--ff-only` the upstream into the current branch. Refused on
    /// dirty worktrees, diverged histories, or missing upstreams.
    SyncPull {
        project: ProjectId,
        remote: Option<String>,
    },
    /// Push the current branch. Refused on detached HEAD, missing
    /// upstreams (unless `set_upstream`), and non-fast-forwards.
    SyncPush {
        project: ProjectId,
        set_upstream: bool,
    },
    /// Bounded stash listing, newest first (C9.3). Non-repos return the
    /// empty envelope, like status.
    StashList {
        project: ProjectId,
    },
    /// Stash tracked changes with `message`; `untracked` adds `-u`.
    /// Nothing-to-stash fails honestly through git.
    StashPush {
        project: ProjectId,
        message: String,
        untracked: bool,
    },
    /// Re-apply an entry without dropping it (conflicts fail honestly).
    StashApply {
        project: ProjectId,
        index: usize,
    },
    /// Re-apply an entry and drop it on success (conflicts keep it).
    StashPop {
        project: ProjectId,
        index: usize,
    },
    /// Drop an entry (destructive; the desktop arms two-step).
    StashDrop {
        project: ProjectId,
        index: usize,
    },
    /// Bounded per-file blame (C9.4). Non-repos return the empty envelope.
    Blame {
        project: ProjectId,
        path: PathBuf,
    },
}

/// Read-only unified-diff viewer over `git diff` (M15, blueprint §33).
/// Both variants route through the common dispatcher so the sidebar
/// panel, IPC, and CLI share one implementation. Both are pure queries:
/// a project without a repo root returns the empty envelope, never an
/// error. Per-hunk stage buttons dispatch `GitCommand::Stage` for the
/// hunk's file — no bespoke git logic in the view.
#[derive(Debug, Clone, PartialEq)]
pub enum DiffCommand {
    /// Full hunk bodies for unstaged (`staged: false`), staged
    /// (`staged: true`), or one filtered path.
    Show {
        project: ProjectId,
        path: Option<PathBuf>,
        staged: bool,
        context_lines: u8,
    },
    /// File headers + hunk counts without bodies (the fast 1000-file
    /// surface).
    ListFiles { project: ProjectId, staged: bool },
    /// One committed path pair against a chosen parent/base. Historical and
    /// strictly read-only: the desktop derives its action set from
    /// [`omaterm_core::DiffSource::Commit`], never from a staged flag.
    ShowCommit {
        project: ProjectId,
        commit: crate::GitObjectId,
        base: crate::GitComparisonBase,
        old_path: Option<PathBuf>,
        path: PathBuf,
        context_lines: u8,
    },
}

/// Bounded, project-scoped process inspection (M18 query slice).
#[derive(Debug, Clone, PartialEq)]
pub enum ProcessCommand {
    List {
        project: ProjectId,
    },
    /// Scoped `SIGTERM` to a project-owned descendant. Membership is
    /// revalidated at dispatch and again before signalling.
    Kill {
        project: ProjectId,
        pid: u32,
    },
}

/// Authority is kept separate from command data so transport identity cannot
/// accidentally be confused with a requested target.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum CommandContext {
    #[default]
    LocalUser,
    Project(ProjectId),
}
