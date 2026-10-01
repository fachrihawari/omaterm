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
    CommandContext, DiffCommand, FileCommand, GitCommand, HistoryCommand, OmaCommand, PaneCommand,
    ProjectCommand, TabCommand, TerminalCommand,
};
pub use error::{CoreError, Result};
pub use ids::{PaneId, ProjectId, SessionId, SplitId, TabId, WindowId};
pub use pane::{
    NormalizedRect, Pane, PaneContent, PaneNode, PaneRect, PaneTree, Removal, SplitAxis,
    SplitDirection, SplitSummary,
};
pub use project::Project;
pub use result::{
    CommandError, CommandOutput, CommandResult, DiffFileInfo, DiffFileStatus, DiffHunkInfo,
    DiffInfo, DiffLineInfo, DiffLineKind, ErrorCode, FileEntry, FileKind, FileListInfo, GitEntry,
    GitStatusInfo, HistoryStatusInfo, JournalEntryInfo, PaneInfo, ProjectInfo, ProjectRootInfo,
    RootSource, TabInfo, TerminalInfo,
};
pub use tab::Tab;
pub use workspace::{Workspace, WorkspaceWindow};
