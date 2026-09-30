pub mod command;
pub mod error;
pub mod ids;
pub mod pane;
pub mod project;
pub mod result;
pub mod tab;
pub mod validation;
pub mod workspace;

pub use command::{
    CommandContext, FileCommand, HistoryCommand, OmaCommand, PaneCommand, ProjectCommand,
    TabCommand, TerminalCommand,
};
pub use error::{CoreError, Result};
pub use ids::{PaneId, ProjectId, SessionId, SplitId, TabId, WindowId};
pub use pane::{
    NormalizedRect, Pane, PaneContent, PaneNode, PaneRect, PaneTree, Removal, SplitAxis,
    SplitDirection, SplitSummary,
};
pub use project::Project;
pub use result::{
    CommandError, CommandOutput, CommandResult, ErrorCode, FileEntry, FileKind, FileListInfo,
    HistoryStatusInfo, JournalEntryInfo, PaneInfo, ProjectInfo, ProjectRootInfo, RootSource,
    TabInfo, TerminalInfo,
};
pub use tab::Tab;
pub use workspace::{Workspace, WorkspaceWindow};
