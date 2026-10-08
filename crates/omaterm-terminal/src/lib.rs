pub mod alacritty;
pub mod color;
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
pub use color::{CURSOR_COLOR, DEFAULT_BG, DEFAULT_FG};
pub use engine::{
    CellFlags, CellWidth, CursorShape, CursorState, EngineOutput, MouseMode, MouseModeKind,
    ScrollCommand, TermColor, TerminalCell, TerminalEngine, TerminalRow, TerminalViewport,
};
pub use events::TerminalEvent;
pub use input::{
    Key, KeyEvent, KeyModifiers, MOUSE_WHEEL_DOWN, MOUSE_WHEEL_UP, encode_key, encode_sgr_mouse,
    escape_shell_path, format_dropped_paths, needs_paste_confirm, prepare_paste,
    wrap_bracketed_paste,
};
pub use lifecycle::{LifecycleEvent, LifecycleKind, LifecycleParser, decode_command, decode_exit};
pub use osc7::{Osc7Parser, parse_osc7_uri};
pub use platform::{
    ClipboardProvider, LinuxProcessInspector, ListeningPort, NotificationProvider, ProcessInfo,
    ProcessInspector,
};
pub use pty::{PtyError, PtyProcess, poll_fd_readable};
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
