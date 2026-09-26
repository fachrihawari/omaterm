pub mod alacritty;
pub mod engine;
pub mod events;
pub mod input;
pub mod osc7;
pub mod pty;
pub mod selection;
pub mod session;

pub use alacritty::AlacrittyEngine;
pub use engine::{
    CellFlags, CellWidth, CursorShape, CursorState, EngineOutput, ScrollCommand, TermColor,
    TerminalCell, TerminalEngine, TerminalRow, TerminalViewport,
};
pub use events::TerminalEvent;
pub use input::{Key, KeyEvent, KeyModifiers, encode_key, prepare_paste, wrap_bracketed_paste};
pub use osc7::{Osc7Parser, parse_osc7_uri};
pub use pty::{PtyError, PtyProcess, poll_fd_readable};
pub use selection::{CellPoint, SelectionRange, extract_text};
pub use session::{CurrentDirectory, CwdProvenance, SessionError, TerminalSession};
