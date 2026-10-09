pub mod command;
pub mod error;
pub mod ids;
pub mod palette;
pub mod pane;
pub mod project;
pub mod repos;
pub mod result;
pub mod tab;
pub mod validation;
pub mod workspace;

pub use command::{
    CommandContext, DiffCommand, EditorCommand, FileCommand, GitCommand, HistoryCommand,
    OmaCommand, PaneCommand, ProcessCommand, ProjectCommand, TabCommand, TerminalCommand,
};
pub use error::{CoreError, Result};
pub use ids::{DocumentId, PaneId, ProjectId, SessionId, SplitId, TabId, WindowId};
pub use palette::{MAX_PALETTE_RESULTS, PaletteRank, rank_palette_indices};
pub use pane::{
    NormalizedRect, Pane, PaneContent, PaneNode, PaneRect, PaneTree, Removal, SplitAxis,
    SplitDirection, SplitSummary,
};
pub use project::Project;
pub use repos::{MAX_SECTIONS, RepoScan};
pub use result::{
    CommandError, CommandOutput, CommandResult, DiffCapabilities, DiffFileInfo, DiffFileStatus,
    DiffHunkInfo, DiffInfo, DiffLineInfo, DiffLineKind, DiffSource, EditorDocumentInfo, ErrorCode,
    FileEntry, FileKind, FileListInfo, GitBlame, GitBlameLine, GitBranch, GitBranchList,
    GitBranchTrack, GitCommitDetails, GitCommitFile, GitCommitFileKind, GitCommitFiles,
    GitCommitSummary, GitComparisonBase, GitEntry, GitHistoryCursor, GitHistoryCursorError,
    GitHistoryPage, GitHistoryScope, GitObjectId, GitObjectIdError, GitRef, GitRefKind,
    GitStashEntry, GitStashList, GitStatusInfo, GitTimestamp, HistoryStatusInfo, JournalEntryInfo,
    MAX_GIT_HISTORY_CURSOR_BYTES, MAX_PROCESS_ENTRIES, PaneInfo, ProcessEntryInfo, ProcessListInfo,
    ProjectInfo, ProjectReposInfo, ProjectRootInfo, RepoEntry, RootSource, TabInfo, TerminalInfo,
};
pub use tab::Tab;
pub use workspace::{Workspace, WorkspaceWindow};
