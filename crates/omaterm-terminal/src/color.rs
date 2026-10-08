//! Default terminal colors shared between the engine (OSC 10/11/12 query
//! replies) and the GPUI renderer.
//!
//! Single source of truth: the colors painted for `TermColor::DefaultFg` /
//! `DefaultBg` and the bytes sent to an application that asks "what is your
//! foreground/background/cursor color?" must never disagree. A TUI such as
//! opencode derives its light/dark theme from the OSC 11 reply, so replying
//! with a color the renderer does not actually paint produces unreadable
//! output.
//!
//! Values mirror the dark UI tokens in `apps/omaterm/src/ui/theme.rs`
//! (`BG2`, `TERMINAL_CURSOR`) and the renderer's hardcoded default
//! foreground. Keeping the pair in sync is enforced by unit tests in this
//! module and by the renderer importing these constants.

/// Default foreground: near-white text on the dark pane.
pub const DEFAULT_FG: (u8, u8, u8) = (0xE4, 0xE4, 0xE7);
/// Default background: the dark pane surface (`theme::BG2`).
pub const DEFAULT_BG: (u8, u8, u8) = (0x0F, 0x13, 0x18);
/// Cursor color (`theme::TERMINAL_CURSOR`).
pub const CURSOR_COLOR: (u8, u8, u8) = (0x95, 0xD7, 0xFF);

#[cfg(test)]
mod tests {
    use super::*;

    /// The renderer reads these same constants; a mismatch with the UI theme
    /// is exactly the class of bug that made opencode pick a light theme on a
    /// dark pane. Assert the literals the renderer/theme publish.
    #[test]
    fn defaults_match_renderer_and_theme_tokens() {
        assert_eq!(DEFAULT_FG, (0xE4, 0xE4, 0xE7));
        assert_eq!(DEFAULT_BG, (0x0F, 0x13, 0x18));
        assert_eq!(CURSOR_COLOR, (0x95, 0xD7, 0xFF));
    }
}
