pub mod alacritty;
pub mod engine;
pub mod events;
pub mod history;
pub mod input;
pub mod lifecycle;
pub mod osc7;
pub mod pty;
pub mod registry;
pub mod selection;
pub mod session;
mod shell;
pub mod spawn_queue;
pub mod workspace;

pub use alacritty::AlacrittyEngine;
pub use engine::{
    CellFlags, CellWidth, CursorShape, CursorState, EngineOutput, ScrollCommand, TermColor,
    TerminalCell, TerminalEngine, TerminalRow, TerminalViewport,
};
pub use events::TerminalEvent;
pub use input::{Key, KeyEvent, KeyModifiers, encode_key, prepare_paste, wrap_bracketed_paste};
pub use lifecycle::{LifecycleEvent, LifecycleKind, LifecycleParser, decode_command, decode_exit};
pub use osc7::{Osc7Parser, parse_osc7_uri};
pub use pty::{PtyError, PtyProcess, poll_fd_readable};
pub use registry::{RegistryError, TerminalConfig, TerminalRegistry};
pub use selection::{CellPoint, SelectionRange, extract_text};
pub use session::{
    CurrentDirectory, CwdProvenance, LifecycleRecord, RunCommandError, SessionError,
    TerminalSession,
};
pub use spawn_queue::{SessionSpawnQueue, SpawnCompletion, SpawnQueueError};
pub use workspace::{
    ClosedPane, CoordinatorError, ProjectSessionCommit, SplitSessionCommit, WorkspaceCoordinator,
};
