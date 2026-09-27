pub mod error;
pub mod ids;
pub mod pane;
pub mod project;
pub mod tab;
pub mod workspace;

pub use error::{CoreError, Result};
pub use ids::{PaneId, ProjectId, SessionId, SplitId, TabId, WindowId};
pub use pane::{
    NormalizedRect, Pane, PaneContent, PaneNode, PaneRect, PaneTree, Removal, SplitAxis,
    SplitDirection,
};
pub use project::Project;
pub use tab::Tab;
pub use workspace::{Workspace, WorkspaceWindow};
