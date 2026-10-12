//! Process-wide terminal palette shared by OSC replies and desktop rendering.
//!
//! The mode is a live setting, not a launch constant: the desktop follows the
//! system appearance and the user can switch at runtime. Readers
//! ([`terminal_palette`], OSC replies, `COLORFGBG` at spawn) always observe
//! the current mode. Running TUIs that cached an OSC reply at startup redraw
//! on the SIGWINCH the desktop sends with every switch; panes created after
//! a switch are exact.

use std::sync::RwLock;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ThemeMode {
    Dark,
    Light,
}

impl ThemeMode {
    /// Flip for the palette toggle command.
    pub fn toggled(self) -> ThemeMode {
        match self {
            ThemeMode::Dark => ThemeMode::Light,
            ThemeMode::Light => ThemeMode::Dark,
        }
    }
}

static THEME: RwLock<Option<ThemeMode>> = RwLock::new(None);

/// First-wins startup selection. Later calls are ignored so background
/// initialization races cannot override the resolved startup mode; runtime
/// switches go through [`set_theme_mode`].
pub fn initialize_theme(mode: ThemeMode) {
    if let Ok(mut guard) = THEME.write()
        && guard.is_none()
    {
        *guard = Some(mode);
    }
}

/// Unconditional runtime switch (palette toggle, system-appearance change).
pub fn set_theme_mode(mode: ThemeMode) {
    if let Ok(mut guard) = THEME.write() {
        *guard = Some(mode);
    }
}

pub fn theme_mode() -> ThemeMode {
    THEME
        .read()
        .ok()
        .and_then(|guard| *guard)
        .unwrap_or(ThemeMode::Dark)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct TerminalPalette {
    pub foreground: (u8, u8, u8),
    pub background: (u8, u8, u8),
    pub cursor: (u8, u8, u8),
    pub ansi: [(u8, u8, u8); 16],
}

/// Default foreground: cool near-white. Matches dark `theme::text` closely enough
/// for OSC replies; the renderer paints `colors().text` for chrome and this
/// triple for default terminal cells.
pub const DEFAULT_FG: (u8, u8, u8) = (0xEE, 0xEA, 0xFF);
/// Default background: the terminal well (`theme::BG2` in dark mode).
pub const DEFAULT_BG: (u8, u8, u8) = (0x0F, 0x0E, 0x17);
/// Cursor color (`theme::terminal_cursor` in dark mode).
pub const CURSOR_COLOR: (u8, u8, u8) = (0xFF, 0x4F, 0xD8);

pub const DARK_PALETTE: TerminalPalette = TerminalPalette {
    foreground: DEFAULT_FG,
    background: DEFAULT_BG,
    cursor: CURSOR_COLOR,
    ansi: [
        (0x24, 0x20, 0x36),
        (0xFF, 0x4D, 0x6D),
        (0x2E, 0xE6, 0xA6),
        (0xFF, 0xD2, 0x3F),
        (0x5B, 0x8C, 0xFF),
        (0xD9, 0x6B, 0xFF),
        (0x3D, 0xE0, 0xF5),
        (0xD6, 0xD1, 0xEE),
        (0x6B, 0x62, 0x90),
        (0xFF, 0x7A, 0x93),
        (0x6B, 0xFF, 0xC4),
        (0xFF, 0xE6, 0x7A),
        (0x8F, 0xB2, 0xFF),
        (0xE9, 0x9C, 0xFF),
        (0x7D, 0xEE, 0xFF),
        (0xFF, 0xFF, 0xFF),
    ],
};

pub const LIGHT_PALETTE: TerminalPalette = TerminalPalette {
    foreground: (0x20, 0x29, 0x36),
    background: (0xFA, 0xFB, 0xFD),
    cursor: (0x17, 0x5F, 0xBD),
    ansi: [
        (32, 41, 54),
        (180, 35, 50),
        (33, 110, 66),
        (128, 91, 0),
        (23, 95, 189),
        (121, 66, 160),
        (9, 108, 121),
        (193, 202, 214),
        (89, 103, 121),
        (194, 38, 54),
        (37, 122, 70),
        (140, 98, 0),
        (29, 103, 198),
        (132, 69, 173),
        (12, 118, 130),
        (250, 251, 253),
    ],
};

pub fn terminal_palette() -> &'static TerminalPalette {
    match theme_mode() {
        ThemeMode::Dark => &DARK_PALETTE,
        ThemeMode::Light => &LIGHT_PALETTE,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn defaults_match_renderer_and_theme_tokens() {
        assert_eq!(DEFAULT_FG, (0xEE, 0xEA, 0xFF));
        assert_eq!(DEFAULT_BG, (0x0F, 0x0E, 0x17));
        assert_eq!(CURSOR_COLOR, (0xFF, 0x4F, 0xD8));
    }

    /// Every dark ANSI text color must stay distinguishable from the well.
    #[test]
    fn ansi_text_colors_contrast_with_background() {
        fn luma((r, g, b): (u8, u8, u8)) -> f32 {
            0.2126 * f32::from(r) + 0.7152 * f32::from(g) + 0.0722 * f32::from(b)
        }
        let bg = luma(DEFAULT_BG);
        for (index, color) in DARK_PALETTE.ansi.iter().enumerate().skip(1) {
            assert!(luma(*color) - bg > 60.0, "ANSI {index} is too dark");
        }
    }

    #[test]
    fn startup_selection_is_first_wins_and_runtime_switch_is_live() {
        let previous = theme_mode();
        // A runtime switch always applies and is visible to every reader.
        // (Compared by value: each `&CONST` reference promotes separately,
        // so pointer identity across expressions is not guaranteed.)
        set_theme_mode(ThemeMode::Light);
        assert_eq!(theme_mode(), ThemeMode::Light);
        assert_eq!(*terminal_palette(), LIGHT_PALETTE);
        // Startup selection never overrides an existing mode, so a late
        // background init cannot undo the resolved theme or a user switch.
        initialize_theme(ThemeMode::Dark);
        assert_eq!(theme_mode(), ThemeMode::Light);
        set_theme_mode(ThemeMode::Dark);
        assert_eq!(*terminal_palette(), DARK_PALETTE);
        assert_eq!(ThemeMode::Dark.toggled(), ThemeMode::Light);
        assert_eq!(ThemeMode::Light.toggled(), ThemeMode::Dark);
        set_theme_mode(previous);
    }
}
