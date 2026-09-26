use crate::{PaneId, SplitId};

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
}
