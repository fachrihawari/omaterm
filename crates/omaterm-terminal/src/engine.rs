use crate::events::TerminalEvent;

/// Output of feeding PTY bytes into the engine.
///
/// `reply_bytes` are device-query replies (DSR, DA, color requests, …)
/// that must be written back to the PTY through the same single writer.
/// This is an intentional deviation from the milestone doc's bare
/// `Vec<TerminalEvent>`: replies are not UI events and must not be dropped.
#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct EngineOutput {
    pub events: Vec<TerminalEvent>,
    pub reply_bytes: Vec<u8>,
}

/// OmaTerm's abstraction over terminal emulators.
///
/// The rest of the application programs against this trait, never against
/// `alacritty_terminal` directly, so a future Ghostty backend is possible.
pub trait TerminalEngine: Send {
    /// Resize the virtual grid. `cols >= 2`, `rows >= 1` (alacritty minimums).
    fn resize(&mut self, cols: u16, rows: u16);

    /// Feed PTY output bytes into the VT parser.
    fn advance_output(&mut self, bytes: &[u8]) -> EngineOutput;

    /// Snapshot the visible viewport for rendering. Immutable, cheap to clone.
    fn viewport(&self) -> TerminalViewport;

    /// Visible text with enforced bounds (automation contrato: never unbounded).
    fn read_visible_text(&self, max_lines: usize, max_columns: usize) -> String;

    /// Scroll the viewport (history offset only, never mutates grid content).
    fn scroll(&mut self, command: ScrollCommand);

    /// Current title from OSC sequences, if any.
    fn title(&self) -> Option<&str>;

    /// Whether the alternate screen is active (for tests / renderer).
    fn is_alt_screen(&self) -> bool;

    /// Current display offset (0 = bottom). Exposed for scroll tests.
    fn display_offset(&self) -> usize;

    /// One row below the viewport for smooth pixel scrolling. Available only
    /// above the live bottom; does not resize or mutate the terminal grid.
    fn viewport_following_row(&self) -> Option<TerminalRow> {
        None
    }

    /// Whether application-cursor mode is active (arrow-key encoding).
    fn app_cursor(&self) -> bool;

    /// Whether application-keypad mode is active.
    fn app_keypad(&self) -> bool;

    /// Whether bracketed paste is active.
    fn bracketed_paste(&self) -> bool;
}

/// Immutable snapshot of the visible terminal for the renderer.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TerminalViewport {
    pub rows: Vec<TerminalRow>,
    pub cursor: CursorState,
    pub cols: u16,
    pub lines: u16,
    pub display_offset: usize,
    /// Total scrollback history lines (for scroll indicators).
    pub history_size: usize,
    pub is_alt_screen: bool,
}

/// One visible row.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TerminalRow {
    pub cells: Vec<TerminalCell>,
}

/// One visible cell. `text` holds the base char plus any combining sequence.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TerminalCell {
    pub text: String,
    pub width: CellWidth,
    pub fg: TermColor,
    pub bg: TermColor,
    pub flags: CellFlags,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CellWidth {
    Single,
    Wide,
    WideContinuation,
}

/// Concrete cell color. Named/indexed colors are resolved against the
/// engine's color table at snapshot time; defaults stay symbolic so the
/// UI can theme them later.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TermColor {
    Rgb(u8, u8, u8),
    DefaultFg,
    DefaultBg,
}

/// Cell attributes. Manual bitflags to avoid an extra dependency.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct CellFlags(pub u16);

impl CellFlags {
    pub const BOLD: Self = Self(1 << 0);
    pub const ITALIC: Self = Self(1 << 1);
    pub const UNDERLINE: Self = Self(1 << 2);
    pub const INVERSE: Self = Self(1 << 3);
    pub const DIM: Self = Self(1 << 4);
    pub const HIDDEN: Self = Self(1 << 5);
    pub const STRIKETHROUGH: Self = Self(1 << 6);
    /// Row wraps onto the next line (soft wrap, no newline when copying).
    pub const WRAPPED: Self = Self(1 << 7);

    #[must_use]
    pub fn contains(self, other: Self) -> bool {
        self.0 & other.0 == other.0
    }

    #[must_use]
    pub fn is_empty(self) -> bool {
        self.0 == 0
    }
}

impl std::ops::BitOr for CellFlags {
    type Output = Self;
    fn bitor(self, rhs: Self) -> Self {
        Self(self.0 | rhs.0)
    }
}

impl std::ops::BitOrAssign for CellFlags {
    fn bitor_assign(&mut self, rhs: Self) {
        self.0 |= rhs.0;
    }
}

/// Cursor state for rendering.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct CursorState {
    pub row: u16,
    pub col: u16,
    pub shape: CursorShape,
    pub visible: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CursorShape {
    Block,
    Underline,
    Bar,
    Hidden,
}

/// Viewport scroll commands.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ScrollCommand {
    Lines(i32),
    PageUp,
    PageDown,
    Top,
    Bottom,
}
