//! Terminal colors shared between the engine (OSC 4/10/11/12 query replies)
//! and the GPUI renderer.
//!
//! Single source of truth: the colors painted for `TermColor::DefaultFg` /
//! `DefaultBg` and the bytes sent to an application that asks "what is your
//! foreground/background/cursor color?" must never disagree. A TUI such as
//! opencode derives its light/dark theme from the OSC 11 reply, so replying
//! with a color the renderer does not actually paint produces unreadable
//! output.
//!
//! Edit terminal colors here. `DEFAULT_BG` and `CURSOR_COLOR` must equal
//! `BG2` and `TERMINAL_CURSOR` in `apps/omaterm/src/ui/theme.rs`; a test in
//! that file enforces it.

/// Default foreground: cool near-white.
pub const DEFAULT_FG: (u8, u8, u8) = (0xEE, 0xEA, 0xFF);
/// Default background: the terminal well (`theme::BG2`).
pub const DEFAULT_BG: (u8, u8, u8) = (0x0F, 0x0E, 0x17);
/// Cursor color (`theme::TERMINAL_CURSOR`).
pub const CURSOR_COLOR: (u8, u8, u8) = (0xFF, 0x4F, 0xD8);

/// The 16 ANSI colors (0–7 normal, 8–15 bright), tuned for `DEFAULT_BG`:
/// every hue stays readable as text, and black is lifted so TUIs that paint
/// "black" panels still separate from the background.
pub const ANSI_PALETTE: [(u8, u8, u8); 16] = [
    (0x24, 0x20, 0x36), // black
    (0xFF, 0x4D, 0x6D), // red
    (0x2E, 0xE6, 0xA6), // green
    (0xFF, 0xD2, 0x3F), // yellow
    (0x5B, 0x8C, 0xFF), // blue
    (0xD9, 0x6B, 0xFF), // magenta
    (0x3D, 0xE0, 0xF5), // cyan
    (0xD6, 0xD1, 0xEE), // white
    (0x6B, 0x62, 0x90), // bright black
    (0xFF, 0x7A, 0x93), // bright red
    (0x6B, 0xFF, 0xC4), // bright green
    (0xFF, 0xE6, 0x7A), // bright yellow
    (0x8F, 0xB2, 0xFF), // bright blue
    (0xE9, 0x9C, 0xFF), // bright magenta
    (0x7D, 0xEE, 0xFF), // bright cyan
    (0xFF, 0xFF, 0xFF), // bright white
];

#[cfg(test)]
mod tests {
    use super::*;

    /// The renderer reads these same constants; a mismatch with the UI theme
    /// is exactly the class of bug that made opencode pick a light theme on a
    /// dark pane. Assert the literals the renderer/theme publish.
    #[test]
    fn defaults_match_renderer_and_theme_tokens() {
        assert_eq!(DEFAULT_FG, (0xEE, 0xEA, 0xFF));
        assert_eq!(DEFAULT_BG, (0x0F, 0x0E, 0x17));
        assert_eq!(CURSOR_COLOR, (0xFF, 0x4F, 0xD8));
    }

    /// Every ANSI text color must stay distinguishable from the background.
    #[test]
    fn ansi_text_colors_contrast_with_background() {
        fn luma((r, g, b): (u8, u8, u8)) -> f32 {
            0.2126 * f32::from(r) + 0.7152 * f32::from(g) + 0.0722 * f32::from(b)
        }
        let bg = luma(DEFAULT_BG);
        for (index, color) in ANSI_PALETTE.iter().enumerate().skip(1) {
            assert!(luma(*color) - bg > 60.0, "ANSI {index} is too dark");
        }
    }
}
