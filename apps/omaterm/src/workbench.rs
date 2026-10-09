//! Legacy workbench helpers still in use by notices.
//!
//! The VS Code-style shell (activity rail, contextual sidebar, title and
//! context rows, tab strip) is retired: `ui::theme` and `ui::geometry` own
//! the frame. What remains here are notice-severity colors.

/// Notice strip severity colors (existing banner semantics).
pub const WARN_BG: u32 = 0x3A2E0E;
pub const WARN_TEXT: u32 = 0xFFD23F;
pub const ERROR_BG: u32 = 0x3D1222;
pub const ERROR_TEXT: u32 = 0xFF7A93;
