use std::path::PathBuf;

use crate::{PaneId, ProjectId, SessionId, TabId};

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
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProjectInfo {
    pub id: ProjectId,
    pub name: String,
    pub directory: Option<PathBuf>,
    pub selected: bool,
    pub tab_count: usize,
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
