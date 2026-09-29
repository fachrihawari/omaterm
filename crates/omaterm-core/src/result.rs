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
