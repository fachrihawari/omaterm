use std::path::PathBuf;

use crate::{PaneId, ProjectId, SessionId, SplitDirection, SplitId, TabId};

/// Semantic operations accepted by every in-process and future IPC caller.
#[derive(Debug, Clone, PartialEq)]
pub enum OmaCommand {
    Project(ProjectCommand),
    Tab(TabCommand),
    Pane(PaneCommand),
    Terminal(TerminalCommand),
    History(HistoryCommand),
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
    },
    ReadVisible {
        session: SessionId,
        max_lines: usize,
        max_columns: usize,
    },
    Clear {
        session: SessionId,
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

/// Authority is kept separate from command data so transport identity cannot
/// accidentally be confused with a requested target.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum CommandContext {
    #[default]
    LocalUser,
    Project(ProjectId),
}
