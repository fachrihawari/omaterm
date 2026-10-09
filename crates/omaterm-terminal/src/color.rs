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

pub const DEFAULT_FG: (u8, u8, u8) = (0xE4, 0xE4, 0xE7);
pub const DEFAULT_BG: (u8, u8, u8) = (0x0F, 0x13, 0x18);
pub const CURSOR_COLOR: (u8, u8, u8) = (0x95, 0xD7, 0xFF);

pub const DARK_PALETTE: TerminalPalette = TerminalPalette {
    foreground: DEFAULT_FG,
    background: DEFAULT_BG,
    cursor: CURSOR_COLOR,
    ansi: [
        (0, 0, 0),
        (205, 0, 0),
        (0, 205, 0),
        (205, 205, 0),
        (0, 0, 238),
        (205, 0, 205),
        (0, 205, 205),
        (229, 229, 229),
        (127, 127, 127),
        (255, 0, 0),
        (0, 255, 0),
        (255, 255, 0),
        (92, 92, 255),
        (255, 0, 255),
        (0, 255, 255),
        (255, 255, 255),
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
