pub mod error;
pub mod ids;
pub mod pane;

pub use error::{CoreError, Result};
pub use ids::{PaneId, SessionId, SplitId};
pub use pane::{
    NormalizedRect, Pane, PaneContent, PaneNode, PaneRect, PaneTree, Removal, SplitAxis,
    SplitDirection,
};
