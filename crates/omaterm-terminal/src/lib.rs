pub mod alacritty;
pub mod engine;
pub mod events;
pub mod history;
pub mod input;
pub mod lifecycle;
pub mod osc7;
pub mod platform;
pub mod pty;
pub mod registry;
pub mod search;
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
pub use input::{
    Key, KeyEvent, KeyModifiers, encode_key, escape_shell_path, format_dropped_paths,
    needs_paste_confirm, prepare_paste, wrap_bracketed_paste,
};
pub use lifecycle::{LifecycleEvent, LifecycleKind, LifecycleParser, decode_command, decode_exit};
pub use osc7::{Osc7Parser, parse_osc7_uri};
#[cfg(unix)]
pub use platform::LinuxProcessInspector;
pub use platform::{
    ClipboardProvider, HostProcessInspector, ListeningPort, NotificationProvider, ProcessInfo,
    ProcessInspector,
};
#[cfg(unix)]
pub use pty::poll_fd_readable;
pub use pty::{OutputWait, PtyError, PtyProcess};
pub use registry::{RegistryError, TerminalConfig, TerminalRegistry};
pub use search::{
    MAX_SEARCH_HITS, MAX_SEARCH_QUERY_CHARS, SearchHit, char_range_to_cells, find_hits,
    viewport_line_text,
};
pub use selection::{CellPoint, SelectionRange, extract_text};
pub use session::{
    CurrentDirectory, CwdProvenance, LifecycleRecord, RunCommandError, SessionError,
    TerminalSession,
};
pub use spawn_queue::{SessionSpawnQueue, SpawnCompletion, SpawnQueueError};
pub use workspace::{
    ClosedPane, CoordinatorError, ProjectSessionCommit, SplitSessionCommit, WorkspaceCoordinator,
};
