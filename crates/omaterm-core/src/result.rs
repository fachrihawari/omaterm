use std::path::PathBuf;

use crate::{PaneId, ProjectId, SessionId, SplitSummary, TabId};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ErrorCode {
    ProjectNotFound,
    TabNotFound,
    PaneNotFound,
    SplitNotFound,
    SessionExited,
    TerminalRequired,
    PermissionDenied,
    CrossProjectDenied,
    InvalidRequest,
    UnsupportedOperation,
    ShellBusy,
    Timeout,
    NoFocusedPane,
    RuntimeFailure,
    HistoryDisabled,
    HistoryUnavailable,
    /// A filesystem path escaped the resolved project root (M12 boundary,
    /// blueprint §50). Introduced with the context foundation so M13 file
    /// and M14 git mutations share one stable code.
    PathOutsideRoot,
    /// A file path under the project root does not exist or is not
    /// accessible (M13). Distinct from `PathOutsideRoot`: the path is
    /// inside the root but missing.
    FileNotFound,
    /// `$EDITOR` is unset or empty so `file.open` has nothing to submit
    /// (M13). The editor pane itself is v0.3 scope.
    EditorNotConfigured,
    /// The project has no filesystem root (M12 `none` empty state) and the
    /// requested operation needs one (`file.open`). Listing/searching
    /// without a root returns an empty envelope instead of this error.
    NoProjectRoot,
    /// The project root is not inside a git repository, so a git mutation
    /// has no repository to target (M14). `git.status` on a non-repo root
    /// returns an empty envelope instead of this error.
    NotARepo,
    /// A git subprocess failed: the message carries bounded git stderr so
    /// agents never parse it for control flow (M14, blueprint §§32, 44).
    GitFailed,
    /// The system git binary is missing or cannot be spawned (M14,
    /// blueprint §32). Distinct from `GitFailed`: the tool is absent, not
    /// the repository.
    GitUnavailable,
}

impl ErrorCode {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::ProjectNotFound => "project_not_found",
            Self::TabNotFound => "tab_not_found",
            Self::PaneNotFound => "pane_not_found",
            Self::SplitNotFound => "split_not_found",
            Self::SessionExited => "session_exited",
            Self::TerminalRequired => "terminal_required",
            Self::PermissionDenied => "permission_denied",
            Self::CrossProjectDenied => "cross_project_denied",
            Self::InvalidRequest => "invalid_request",
            Self::UnsupportedOperation => "unsupported_operation",
            Self::ShellBusy => "shell_busy",
            Self::Timeout => "timeout",
            Self::NoFocusedPane => "no_focused_pane",
            Self::RuntimeFailure => "runtime_failure",
            Self::HistoryDisabled => "history_disabled",
            Self::HistoryUnavailable => "history_unavailable",
            Self::PathOutsideRoot => "path_outside_root",
            Self::FileNotFound => "file_not_found",
            Self::EditorNotConfigured => "editor_not_configured",
            Self::NoProjectRoot => "no_project_root",
            Self::NotARepo => "not_a_repo",
            Self::GitFailed => "git_failed",
            Self::GitUnavailable => "git_unavailable",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CommandError {
    pub code: ErrorCode,
    pub message: String,
    pub details: Option<String>,
}

impl CommandError {
    pub fn new(code: ErrorCode, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            details: None,
        }
    }
}

impl std::fmt::Display for CommandError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.message)
    }
}
impl std::error::Error for CommandError {}

#[derive(Debug, Clone, PartialEq)]
pub enum CommandResult {
    Ok(CommandOutput),
    Err(CommandError),
}

impl CommandResult {
    pub fn output(self) -> Result<CommandOutput, CommandError> {
        match self {
            Self::Ok(output) => Ok(output),
            Self::Err(error) => Err(error),
        }
    }
}

#[derive(Debug, Clone, PartialEq)]
pub enum CommandOutput {
    Unit,
    /// In-process receipt; a creation only succeeds when its completion commits.
    Pending {
        operation_id: u64,
    },
    ProjectList(Vec<ProjectInfo>),
    TabList(Vec<TabInfo>),
    PaneList(Vec<PaneInfo>),
    TerminalList(Vec<TerminalInfo>),
    TerminalOutput {
        text: String,
        truncated: bool,
        lines: usize,
        columns: usize,
    },
    ProjectCreated {
        project: ProjectId,
        tab: TabId,
        pane: PaneId,
        session: SessionId,
    },
    TabCreated {
        tab: TabId,
        pane: PaneId,
        session: SessionId,
    },
    PaneSplit {
        pane: PaneId,
        session: SessionId,
    },
    TerminalCreated {
        tab: TabId,
        pane: PaneId,
        session: SessionId,
    },
    RunSubmitted,
    HistoryStatus(HistoryStatusInfo),
    JournalEntries(Vec<JournalEntryInfo>),
    HistoryCleared {
        removed_files: usize,
    },
    ProjectRoot(ProjectRootInfo),
    FileList(FileListInfo),
    GitStatus(GitStatusInfo),
    GitCommitted {
        oid: String,
    },
    Diff(DiffInfo),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProjectInfo {
    pub id: ProjectId,
    pub name: String,
    pub directory: Option<PathBuf>,
    pub selected: bool,
    pub tab_count: usize,
}

/// Where a resolved project root came from (M12, blueprint §31). The wire
/// form is the lowercase string (`pinned` | `git` | `none`).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RootSource {
    Pinned,
    Git,
    Absent,
}

impl RootSource {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Pinned => "pinned",
            Self::Git => "git",
            Self::Absent => "none",
        }
    }
}

/// Owned result of `ProjectCommand::Root`: the resolved filesystem root
/// plus its source. `root` is `None` exactly when `source` is `Absent`
/// (non-repo project without a usable pin — an explicit empty state,
/// never an error).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProjectRootInfo {
    pub root: Option<PathBuf>,
    pub source: RootSource,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TabInfo {
    pub id: TabId,
    pub project: ProjectId,
    pub name: String,
    pub selected: bool,
    pub focused_pane: Option<PaneId>,
    pub pane_count: usize,
}

#[derive(Debug, Clone, PartialEq)]
pub struct PaneInfo {
    pub id: PaneId,
    pub project: ProjectId,
    pub tab: TabId,
    pub session: Option<SessionId>,
    pub focused: bool,
    pub x: f32,
    pub y: f32,
    pub width: f32,
    pub height: f32,
    /// Root-to-leaf split path of this pane. The last entry is the innermost
    /// split and the one `pane.resize` should target for this pane. Empty
    /// for an unsplit root pane.
    pub splits: Vec<SplitSummary>,
}

/// Safe history metadata for `history status`: counts and state only, never
/// keys, plaintext, or keyring details.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HistoryStatusInfo {
    pub enabled: bool,
    pub key_available: bool,
    pub warning: Option<String>,
    pub archive_files: usize,
    pub archive_bytes: u64,
    pub paused_panes: usize,
}

/// One bounded journal row for `history list`. Command text comes only from
/// authenticated shell lifecycle events.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct JournalEntryInfo {
    pub pane: String,
    pub project: Option<String>,
    pub tab: Option<String>,
    pub command: String,
    pub shell_dialect: String,
    pub working_directory: String,
    pub started_unix_secs: u64,
    pub finished_unix_secs: Option<u64>,
    pub exit_status: Option<i32>,
}

/// One file-tree row (M13). `path` is relative to the project root so the
/// wire form stays stable when the root moves (pinned edits, git re-root).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FileEntry {
    pub path: PathBuf,
    pub kind: FileKind,
}

/// Entry kind for file listings and search results.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FileKind {
    File,
    Directory,
    Symlink,
    Other,
}

impl FileKind {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::File => "file",
            Self::Directory => "directory",
            Self::Symlink => "symlink",
            Self::Other => "other",
        }
    }
}

/// Bounded file listing envelope: entries plus an accurate truncation flag.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FileListInfo {
    pub entries: Vec<FileEntry>,
    pub truncated: bool,
}

/// One changed path from `git status --porcelain=v2 -z` (M14, blueprint
/// §32). `path` is relative to the project root (git runs with
/// `status.relativePaths=true` and the root as its working directory), so
/// the wire form stays stable when the root moves. `x`/`y` are the raw
/// index/worktree status codes (`.` = unmodified); `renamed_from` carries
/// the pre-rename path for `R`/`C` entries (`git status -z` order).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitEntry {
    pub path: PathBuf,
    pub renamed_from: Option<PathBuf>,
    pub x: char,
    pub y: char,
}

impl GitEntry {
    /// True when the index side differs from HEAD (staged change).
    pub const fn is_staged(&self) -> bool {
        self.x != '.'
    }

    /// True when the worktree side differs from the index (unstaged change).
    pub const fn is_unstaged(&self) -> bool {
        self.y != '.'
    }
}

/// Bounded git status envelope (M14). Groups mirror the VSCode Source
/// Control sections; `truncated` is accurate whenever the entry cap drops
/// paths. `branch` is `None` for detached HEAD; `upstream` is `None`
/// without a configured upstream.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitStatusInfo {
    pub branch: Option<String>,
    pub upstream: Option<String>,
    pub ahead: usize,
    pub behind: usize,
    pub staged: Vec<GitEntry>,
    pub unstaged: Vec<GitEntry>,
    pub untracked: Vec<GitEntry>,
    pub truncated: bool,
}

impl GitStatusInfo {
    /// Explicit empty state for non-repo roots: never an error (M14).
    pub const fn empty() -> Self {
        Self {
            branch: None,
            upstream: None,
            ahead: 0,
            behind: 0,
            staged: Vec::new(),
            unstaged: Vec::new(),
            untracked: Vec::new(),
            truncated: false,
        }
    }

    /// Total entries across all three groups.
    pub fn len(&self) -> usize {
        self.staged.len() + self.unstaged.len() + self.untracked.len()
    }

    pub const fn is_empty(&self) -> bool {
        self.staged.is_empty() && self.unstaged.is_empty() && self.untracked.is_empty()
    }
}

/// One unified-diff file (M15, blueprint §33). `path` is relative to the
/// project root (git runs with the root as its working directory), so the
/// wire form stays stable when the root moves. `old_path` is set for
/// renames only. Binary files carry no hunks ("binary, not shown").
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiffFileInfo {
    pub path: PathBuf,
    pub old_path: Option<PathBuf>,
    pub status: DiffFileStatus,
    pub binary: bool,
    pub hunks: Vec<DiffHunkInfo>,
    /// Total hunks in the file, including dropped ones. Equals
    /// `hunks.len()` when fully loaded; larger when caps truncated the
    /// file or `list-files` skipped bodies.
    pub hunk_count: usize,
    pub truncated: bool,
}

/// File change kind from the `diff --git` headers.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DiffFileStatus {
    Added,
    Deleted,
    Modified,
    Renamed,
    Copied,
}

impl DiffFileStatus {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Added => "added",
            Self::Deleted => "deleted",
            Self::Modified => "modified",
            Self::Renamed => "renamed",
            Self::Copied => "copied",
        }
    }
}

/// One `@@` hunk with its bounded body lines.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiffHunkInfo {
    pub old_start: u32,
    pub old_lines: u32,
    pub new_start: u32,
    pub new_lines: u32,
    pub lines: Vec<DiffLineInfo>,
    pub truncated: bool,
}

/// One hunk body line. The marker is structural (`kind`); `text` never
/// carries the leading ` `/`+`/`-`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiffLineInfo {
    pub kind: DiffLineKind,
    pub text: String,
}

/// Hunk line kind for the `±` coloring (no highlighting in v0.2).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DiffLineKind {
    Context,
    Addition,
    Deletion,
}

impl DiffLineKind {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Context => "context",
            Self::Addition => "addition",
            Self::Deletion => "deletion",
        }
    }
}

/// Bounded diff envelope (M15). `staged` records which side was read
/// (`git diff` vs `git diff --cached`); `truncated` is accurate whenever
/// any cap dropped files, hunks, lines, or bytes.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiffInfo {
    pub files: Vec<DiffFileInfo>,
    pub truncated: bool,
    pub staged: bool,
}

impl DiffInfo {
    /// Explicit empty state for non-repo roots: never an error (M14
    /// product contract, reused for diff).
    pub const fn empty(staged: bool) -> Self {
        Self {
            files: Vec::new(),
            truncated: false,
            staged,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TerminalInfo {
    pub id: SessionId,
    pub project: ProjectId,
    pub tab: TabId,
    pub pane: PaneId,
    pub cwd: PathBuf,
    pub title: Option<String>,
    pub exited: bool,
    pub columns: usize,
    pub lines: usize,
}
