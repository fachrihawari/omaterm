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
    /// The requested process id is not part of the project's process family,
    /// or no longer exists, so `process.kill` has nothing to signal (M18).
    ProcessNotFound,
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
    /// Branch checkout refused: the worktree has staged or unstaged
    /// changes (C9.1). Callers stash or discard first, then retry.
    DirtyWorktree,
    /// Branch delete refused: the branch is currently checked out (C9.1).
    CurrentBranch,
    /// The system git binary is missing or cannot be spawned (M14,
    /// blueprint §32). Distinct from `GitFailed`: the tool is absent, not
    /// the repository.
    GitUnavailable,
    /// No open document has the requested ID (M19). Stale document handles
    /// fail with this code and no effects, never a fallback document.
    DocumentNotOpen,
    /// The file changed on disk since the document was opened or last saved
    /// (M19). Save is refused until the caller reloads or explicitly reverts.
    DocumentConflict,
    /// The file exceeds the editor byte/line caps (M19). Listing/searching
    /// never returns contents, so this only surfaces on explicit open/save.
    DocumentTooLarge,
    /// The file is not openable as text: binary content or invalid UTF-8
    /// (M19). Rejected with a reason rather than lossy silent conversion.
    NotTextFile,
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
            Self::ProcessNotFound => "process_not_found",
            Self::NoProjectRoot => "no_project_root",
            Self::NotARepo => "not_a_repo",
            Self::GitFailed => "git_failed",
            Self::DirtyWorktree => "dirty_worktree",
            Self::CurrentBranch => "current_branch",
            Self::GitUnavailable => "git_unavailable",
            Self::DocumentNotOpen => "document_not_open",
            Self::DocumentConflict => "document_conflict",
            Self::DocumentTooLarge => "document_too_large",
            Self::NotTextFile => "not_text_file",
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
    EditorOpened(EditorDocumentInfo),
    EditorSaved(EditorDocumentInfo),
    GitStatus(GitStatusInfo),
    GitHistory(GitHistoryPage),
    GitBranchList(GitBranchList),
    GitCommitFiles(GitCommitFiles),
    GitCommitted {
        oid: String,
    },
    Diff(DiffInfo),
    ProcessList(ProcessListInfo),
    /// A project-scoped `SIGTERM` was delivered. `signal` is the stable wire
    /// name (`SIGTERM`); callers never parse a numeric signal.
    ProcessKilled {
        pid: u32,
        signal: &'static str,
    },
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

/// Owned snapshot of an open editor document (M19, blueprint §34). Carries
/// identity and size metadata only — never buffer contents — so list-style
/// responses stay bounded. Contents live in the desktop `DocumentStore`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EditorDocumentInfo {
    pub document: crate::DocumentId,
    pub project: ProjectId,
    /// Root-relative path, as supplied on open.
    pub path: PathBuf,
    pub bytes: usize,
    pub lines: usize,
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

/// One local branch from `git for-each-ref` (C9.1). `upstream` is the
/// configured upstream short name, if any; `track` is the symbolic
/// tracking state (`=`/`>`/`<`/`<>`/none) — exact ahead/behind numbers
/// are read on demand for the current branch only.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitBranch {
    pub name: String,
    pub upstream: Option<String>,
    pub track: GitBranchTrack,
    pub is_head: bool,
}

/// Symbolic upstream tracking state from `%(upstream:trackshort)`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum GitBranchTrack {
    UpToDate,
    Ahead(usize),
    Behind(usize),
    Diverged { ahead: usize, behind: usize },
    NoUpstream,
}

/// Bounded local branch listing with HEAD identity. `head` is `None` on
/// detached HEAD (then `detached_oid` carries the short oid); `truncated`
/// is accurate whenever the ref cap drops branches.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitBranchList {
    pub head: Option<String>,
    pub detached_oid: Option<String>,
    pub branches: Vec<GitBranch>,
    pub truncated: bool,
}

impl GitBranchList {
    /// Explicit empty envelope for pinned non-repo directories.
    pub const fn empty() -> Self {
        Self {
            head: None,
            detached_oid: None,
            branches: Vec::new(),
            truncated: false,
        }
    }
}

/// A canonical full Git object ID. History operations deliberately accept full
/// SHA-1 (40 hex) or SHA-256 (64 hex) IDs only: abbreviated revisions and rev
/// expressions make a selected historical comparison ambiguous or mutable.
#[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct GitObjectId(String);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum GitObjectIdError {
    InvalidLength,
    InvalidCharacter,
}

impl std::fmt::Display for GitObjectIdError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidLength => {
                f.write_str("git object ID must contain 40 or 64 hexadecimal characters")
            }
            Self::InvalidCharacter => {
                f.write_str("git object ID must contain only hexadecimal characters")
            }
        }
    }
}

impl std::error::Error for GitObjectIdError {}

impl GitObjectId {
    pub fn parse(value: impl AsRef<str>) -> Result<Self, GitObjectIdError> {
        let value = value.as_ref();
        if !matches!(value.len(), 40 | 64) {
            return Err(GitObjectIdError::InvalidLength);
        }
        if !value.bytes().all(|byte| byte.is_ascii_hexdigit()) {
            return Err(GitObjectIdError::InvalidCharacter);
        }
        Ok(Self(value.to_ascii_lowercase()))
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }

    pub fn short(&self) -> &str {
        &self.0[..self.0.len().min(12)]
    }
}

impl std::fmt::Display for GitObjectId {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

impl TryFrom<String> for GitObjectId {
    type Error = GitObjectIdError;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        Self::parse(value)
    }
}

impl TryFrom<&str> for GitObjectId {
    type Error = GitObjectIdError;

    fn try_from(value: &str) -> Result<Self, Self::Error> {
        Self::parse(value)
    }
}

/// Bounded opaque continuation token for a project-scoped history snapshot.
/// The token is server-issued; callers must not synthesize a traversal from a
/// commit's parent because Git history is a DAG, not a linear list.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct GitHistoryCursor(String);

pub const MAX_GIT_HISTORY_CURSOR_BYTES: usize = 4 * 1024;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum GitHistoryCursorError {
    Empty,
    TooLong,
    ControlCharacter,
}

impl std::fmt::Display for GitHistoryCursorError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Empty => f.write_str("git history cursor must not be empty"),
            Self::TooLong => f.write_str("git history cursor exceeds the 4096-byte limit"),
            Self::ControlCharacter => {
                f.write_str("git history cursor must not contain control characters")
            }
        }
    }
}

impl std::error::Error for GitHistoryCursorError {}

impl GitHistoryCursor {
    pub fn parse(value: impl Into<String>) -> Result<Self, GitHistoryCursorError> {
        let value = value.into();
        if value.is_empty() {
            return Err(GitHistoryCursorError::Empty);
        }
        if value.len() > MAX_GIT_HISTORY_CURSOR_BYTES {
            return Err(GitHistoryCursorError::TooLong);
        }
        if value.chars().any(char::is_control) {
            return Err(GitHistoryCursorError::ControlCharacter);
        }
        Ok(Self(value))
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }
}

/// Which immutable graph roots a history page walks. `AllLocalBranches` never
/// consults remotes or fetches; remote refs are decorations only.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum GitHistoryScope {
    #[default]
    CurrentHead,
    AllLocalBranches,
}

impl GitHistoryScope {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::CurrentHead => "current_head",
            Self::AllLocalBranches => "all_local_branches",
        }
    }
}

/// An instant emitted by Git's commit metadata. `offset_minutes` is the
/// original author/committer UTC offset, not the viewer's local timezone.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct GitTimestamp {
    pub unix_seconds: i64,
    pub offset_minutes: i16,
}

/// Decoration type rendered beside a history row. Decorations never alter the
/// traversal roots: remote tracking refs and tags are display metadata only.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum GitRefKind {
    Head,
    LocalBranch,
    RemoteTracking,
    Tag,
    Other,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitRef {
    pub name: String,
    pub kind: GitRefKind,
}

/// Bounded, immutable commit metadata for one graph row. Full messages and
/// changed paths are separate on-demand queries so an initial history page
/// stays small.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitCommitSummary {
    pub id: GitObjectId,
    pub parents: Vec<GitObjectId>,
    pub author_name: String,
    pub author_email: String,
    pub author_time: GitTimestamp,
    pub subject: String,
    pub refs: Vec<GitRef>,
    /// The history walk reached a shallow boundary whose unavailable parent is
    /// not represented as a root commit.
    pub shallow_boundary: bool,
}

/// Bounded history page from an immutable graph snapshot. `has_more` means a
/// complete next record exists; `truncated` means a configured byte/item/ref
/// cap prevented a complete representation.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitHistoryPage {
    pub commits: Vec<GitCommitSummary>,
    pub has_more: bool,
    pub truncated: bool,
}

/// A complete bounded commit message. The summary repeats the graph row so a
/// details response remains self-describing after a sidebar refresh.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitCommitDetails {
    pub summary: GitCommitSummary,
    pub body: String,
    pub body_truncated: bool,
}

/// File-level change kind in a parent-to-commit comparison. Mode/type changes
/// are explicit because they can have no textual hunk body.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum GitCommitFileKind {
    Added,
    Deleted,
    Modified,
    Renamed,
    Copied,
    TypeChanged,
    ModeChanged,
    Submodule,
}

impl GitCommitFileKind {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Added => "added",
            Self::Deleted => "deleted",
            Self::Modified => "modified",
            Self::Renamed => "renamed",
            Self::Copied => "copied",
            Self::TypeChanged => "type_changed",
            Self::ModeChanged => "mode_changed",
            Self::Submodule => "submodule",
        }
    }
}

/// One raw root-relative historical path pair. These paths identify committed
/// tree entries and must not be canonicalized against today's filesystem.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitCommitFile {
    pub path: PathBuf,
    pub old_path: Option<PathBuf>,
    pub kind: GitCommitFileKind,
    pub old_mode: Option<u32>,
    pub new_mode: Option<u32>,
}

/// Parent-specific changed-file listing for an expanded history node.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GitCommitFiles {
    pub commit: GitObjectId,
    pub base: GitComparisonBase,
    pub files: Vec<GitCommitFile>,
    pub truncated: bool,
}

/// The base side of an immutable committed comparison. `EmptyTree` is valid
/// only for a genuine root commit and is resolved by the Git backend for the
/// repository's configured object format.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub enum GitComparisonBase {
    Parent(GitObjectId),
    EmptyTree,
}

/// Provenance for a diff preview. Rendering derives available controls from
/// this identity rather than inferring that every non-staged diff is mutable.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DiffSource {
    WorkingTree {
        path: Option<PathBuf>,
        untracked: bool,
    },
    Index {
        path: Option<PathBuf>,
    },
    Commit {
        commit: GitObjectId,
        base: GitComparisonBase,
        old_path: Option<PathBuf>,
        path: PathBuf,
    },
}

/// UI/action affordances approved for a source. The owner still revalidates
/// command inputs and project scope before every semantic mutation.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct DiffCapabilities {
    pub stage_file: bool,
    pub unstage_file: bool,
    pub discard_file: bool,
    pub stage_hunk: bool,
    pub open_working_file: bool,
}

impl DiffSource {
    pub const fn capabilities(&self) -> DiffCapabilities {
        match self {
            Self::WorkingTree { .. } => DiffCapabilities {
                stage_file: true,
                unstage_file: false,
                discard_file: true,
                stage_hunk: true,
                open_working_file: true,
            },
            Self::Index { .. } => DiffCapabilities {
                stage_file: false,
                unstage_file: true,
                discard_file: false,
                stage_hunk: false,
                open_working_file: true,
            },
            Self::Commit { .. } => DiffCapabilities {
                stage_file: false,
                unstage_file: false,
                discard_file: false,
                stage_hunk: false,
                open_working_file: true,
            },
        }
    }

    pub const fn is_historical(&self) -> bool {
        matches!(self, Self::Commit { .. })
    }
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
    /// Stable identity for this complete parsed hunk. It is derived from the
    /// hunk's spans and body, so callers can request a hunk without sending a
    /// patch or relying on its visible screen position.
    pub id: u64,
    /// Original unified hunk header (`@@ ... @@`), including any trailing
    /// function/context text so copying does not reconstruct a lossy header.
    pub header: String,
    pub old_start: u32,
    pub old_lines: u32,
    pub new_start: u32,
    pub new_lines: u32,
    pub lines: Vec<DiffLineInfo>,
    pub truncated: bool,
}

impl DiffHunkInfo {
    /// Deterministic FNV-1a identity over the exact parsed hunk structure.
    /// This is an optimistic freshness selector, not a cryptographic digest.
    pub fn id_for(
        old_start: u32,
        old_lines: u32,
        new_start: u32,
        new_lines: u32,
        lines: &[DiffLineInfo],
    ) -> u64 {
        let mut hash = 0xcbf2_9ce4_8422_2325_u64;
        for value in [old_start, old_lines, new_start, new_lines] {
            for byte in value.to_le_bytes() {
                hash = (hash ^ u64::from(byte)).wrapping_mul(0x0000_0100_0000_01b3);
            }
        }
        for line in lines {
            hash = (hash
                ^ match line.kind {
                    DiffLineKind::Context => 0,
                    DiffLineKind::Addition => 1,
                    DiffLineKind::Deletion => 2,
                })
            .wrapping_mul(0x0000_0100_0000_01b3);
            for byte in line.text.as_bytes() {
                hash = (hash ^ u64::from(*byte)).wrapping_mul(0x0000_0100_0000_01b3);
            }
            hash = (hash ^ u64::from(line.no_newline_at_end)).wrapping_mul(0x0000_0100_0000_01b3);
        }
        hash
    }
}

/// One hunk body line. The marker is structural (`kind`); `text` never
/// carries the leading ` `/`+`/`-`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DiffLineInfo {
    pub kind: DiffLineKind,
    pub text: String,
    /// Git reported that this source line has no trailing newline. The marker
    /// belongs to the preceding body line, not to a synthetic display row.
    pub no_newline_at_end: bool,
}

pub const MAX_PROCESS_ENTRIES: usize = 512;

#[derive(Debug, Clone, PartialEq)]
pub struct ProcessListInfo {
    pub entries: Vec<ProcessEntryInfo>,
    pub truncated: bool,
}

#[derive(Debug, Clone, PartialEq)]
pub struct ProcessEntryInfo {
    pub pid: u32,
    pub ppid: u32,
    pub name: String,
    pub pane: PaneId,
    pub session: SessionId,
    pub ports: Vec<u16>,
    pub cpu_percent: Option<f32>,
    pub memory_bytes: Option<u64>,
}

/// Hunk line kind for the `±` coloring, composed with token foregrounds.
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
/// any cap dropped files, hunks, lines, or bytes. Nested flags identify the
/// precise loss location.
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

#[cfg(test)]
mod tests {
    use super::*;

    const SHA1: &str = "0123456789abcdef0123456789abcdef01234567";
    const SHA256: &str = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";

    #[test]
    fn git_object_ids_require_full_hex_and_normalize_case() {
        let uppercase = SHA1.to_ascii_uppercase();
        let sha1 = GitObjectId::parse(&uppercase).expect("valid SHA-1");
        let sha256 = GitObjectId::parse(SHA256).expect("valid SHA-256");

        assert_eq!(sha1.as_str(), SHA1);
        assert_eq!(sha1.short(), &SHA1[..12]);
        assert_eq!(sha256.short(), &SHA256[..12]);
        assert_eq!(
            GitObjectId::parse("0123456789abcdef0123456789abcdef0123456"),
            Err(GitObjectIdError::InvalidLength)
        );
        assert_eq!(
            GitObjectId::parse(format!("{}g", &SHA1[..39])),
            Err(GitObjectIdError::InvalidCharacter)
        );
    }

    #[test]
    fn history_cursor_is_bounded_and_printable() {
        assert_eq!(
            GitHistoryCursor::parse(String::new()),
            Err(GitHistoryCursorError::Empty)
        );
        assert_eq!(
            GitHistoryCursor::parse("snapshot\n2"),
            Err(GitHistoryCursorError::ControlCharacter)
        );
        assert_eq!(
            GitHistoryCursor::parse("x".repeat(MAX_GIT_HISTORY_CURSOR_BYTES + 1)),
            Err(GitHistoryCursorError::TooLong)
        );
        assert_eq!(
            GitHistoryCursor::parse("snapshot:2").unwrap().as_str(),
            "snapshot:2"
        );
    }

    #[test]
    fn historical_diffs_never_advertise_git_mutations() {
        let source = DiffSource::Commit {
            commit: GitObjectId::parse(SHA1).unwrap(),
            base: GitComparisonBase::Parent(GitObjectId::parse(SHA256).unwrap()),
            old_path: Some(PathBuf::from("old name.rs")),
            path: PathBuf::from("new name.rs"),
        };
        let capabilities = source.capabilities();

        assert!(source.is_historical());
        assert!(!capabilities.stage_file);
        assert!(!capabilities.unstage_file);
        assert!(!capabilities.discard_file);
        assert!(!capabilities.stage_hunk);
        assert!(capabilities.open_working_file);

        let working = DiffSource::WorkingTree {
            path: Some(PathBuf::from("src/main.rs")),
            untracked: false,
        };
        assert!(!working.is_historical());
        assert!(working.capabilities().stage_file);
        assert!(working.capabilities().discard_file);

        let index = DiffSource::Index { path: None };
        assert!(index.capabilities().unstage_file);
        assert!(!index.capabilities().discard_file);
    }
}
