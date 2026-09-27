use crate::{PaneId, ProjectId, SplitId, TabId};

pub type Result<T> = std::result::Result<T, CoreError>;

#[derive(Debug, thiserror::Error, PartialEq)]
pub enum CoreError {
    #[error("pane {0:?} was not found")]
    PaneNotFound(PaneId),
    #[error("split {0:?} was not found")]
    SplitNotFound(SplitId),
    #[error("pane {0:?} already exists")]
    DuplicatePaneId(PaneId),
    #[error("split {0:?} already exists")]
    DuplicateSplitId(SplitId),
    #[error("split fraction must be finite")]
    NonFiniteFraction,
    #[error("split fraction must be within [0.1, 0.9]")]
    FractionOutOfBounds,
    #[error("pane tree is empty")]
    EmptyTree,
    #[error("project {0:?} was not found")]
    ProjectNotFound(ProjectId),
    #[error("tab {0:?} was not found")]
    TabNotFound(TabId),
    #[error("project {0:?} already exists")]
    DuplicateProjectId(ProjectId),
    #[error("tab {0:?} already exists")]
    DuplicateTabId(TabId),
    #[error("workspace has no selected project")]
    NoSelectedProject,
    #[error("project has no selected tab")]
    NoSelectedTab,
    #[error("tab focus does not reference a pane in the tab")]
    InvalidFocusedPane,
}
