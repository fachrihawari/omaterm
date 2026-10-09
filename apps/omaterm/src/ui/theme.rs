//! Shared semantic palettes for chrome, editor, and terminal surfaces.
//! Dark values preserve the frozen v5 reference; light values preserve its hierarchy.

use omaterm_terminal::ThemeMode;

pub struct Palette {
    pub bg: u32,
    pub bg2: u32,
    pub panel: u32,
    pub panel2: u32,
    pub panel3: u32,
    pub border: u32,
    pub border2: u32,
    pub text: u32,
    pub text2: u32,
    pub muted: u32,
    pub muted2: u32,
    pub blue: u32,
    pub blue2: u32,
    pub cyan: u32,
    pub green: u32,
    pub yellow: u32,
    pub orange: u32,
    pub red: u32,
    pub purple: u32,
    pub lime: u32,
    pub header_bg: u32,
    pub active_tab_bg: u32,
    pub selected_project_bg: u32,
    pub cmd_hover_bg: u32,
    pub cmd_hover_border: u32,
    pub row_hover_bg: u32,
    pub tree_selected_bg: u32,
    pub pill_bg: u32,
    pub pill_border: u32,
    pub scrollbar_thumb: u32,
    pub editor_bg: u32,
    pub editor_side_header_bg: u32,
    pub info_card_bg: u32,
    pub info_icon_box_bg: u32,
    pub git_badge_bg: u32,
    pub commit_hover_bg: u32,
    pub terminal_cursor: u32,
    pub white: u32,
    pub on_accent: u32,
    pub warning_bg: u32,
    pub warning_text: u32,
    pub error_bg: u32,
    pub error_text: u32,
    pub kbd_bg: u32,
    pub kbd_border: u32,
    pub active_line_no: u32,
    pub comment_token: u32,
    pub match_accent: u32,
    pub line_no: u32,
    pub line_no_add: u32,
    pub line_no_del: u32,
    pub diff_add_bg: u32,
    pub diff_add_mark: u32,
    pub diff_del_bg: u32,
    pub diff_del_mark: u32,
    pub selection_hsla: (f32, f32, f32, f32),
}

pub const DARK: Palette = Palette {
    bg: 0x0B0E12,
    bg2: 0x0F1318,
    panel: 0x11161C,
    panel2: 0x161C24,
    panel3: 0x1B222C,
    border: 0x252D38,
    border2: 0x303A48,
    text: 0xD7DDE5,
    text2: 0xBCC5D0,
    muted: 0x7F8A99,
    muted2: 0x596474,
    blue: 0x5AA9FF,
    blue2: 0x2F81F7,
    cyan: 0x61D8DF,
    green: 0x63D58D,
    yellow: 0xE9C66D,
    orange: 0xF3A85F,
    red: 0xFF6F6F,
    purple: 0xC792EA,
    lime: 0xB9F263,
    header_bg: 0x0D1014,
    active_tab_bg: 0x141A21,
    selected_project_bg: 0x18202A,
    cmd_hover_bg: 0x182029,
    cmd_hover_border: 0x27313D,
    row_hover_bg: 0x171D25,
    tree_selected_bg: 0x1B2430,
    pill_bg: 0x151A21,
    pill_border: 0x2C3643,
    scrollbar_thumb: 0x343E4B,
    editor_bg: 0x101318,
    editor_side_header_bg: 0x0F1216,
    info_card_bg: 0x141920,
    info_icon_box_bg: 0x10151A,
    git_badge_bg: 0x222B36,
    commit_hover_bg: 0x3D8BF8,
    terminal_cursor: 0x95D7FF,
    white: 0xFFFFFF,
    on_accent: 0xFFFFFF,
    warning_bg: 0x3F321D,
    warning_text: 0xFDE68A,
    error_bg: 0x3F1D1D,
    error_text: 0xFCA5A5,
    kbd_bg: 0x161B22,
    kbd_border: 0x313B48,
    active_line_no: 0x768193,
    comment_token: 0x6A9955,
    match_accent: 0x4C9AFF,
    line_no: 0x515D6D,
    line_no_add: 0x5D8A67,
    line_no_del: 0x8B5E5E,
    diff_add_bg: 0x2EA0431F,
    diff_add_mark: 0x4BBE68CC,
    diff_del_bg: 0xF851491F,
    diff_del_mark: 0xF85149D1,
    selection_hsla: (0.591, 0.92, 0.578, 0.35),
};

pub const LIGHT: Palette = Palette {
    bg: 0xEEF1F5,
    bg2: 0xFAFBFD,
    panel: 0xFFFFFF,
    panel2: 0xF3F5F8,
    panel3: 0xE8EDF3,
    border: 0xD7DEE7,
    border2: 0xB8C4D2,
    text: 0x202936,
    text2: 0x3C495A,
    muted: 0x596779,
    muted2: 0x68778B,
    blue: 0x175FBD,
    blue2: 0x175FBD,
    cyan: 0x096C79,
    green: 0x216E42,
    yellow: 0x805B00,
    orange: 0x995000,
    red: 0xB42332,
    purple: 0x7942A0,
    lime: 0x4C690F,
    header_bg: 0xF3F5F8,
    active_tab_bg: 0xFFFFFF,
    selected_project_bg: 0xE5EDF8,
    cmd_hover_bg: 0xE4EAF2,
    cmd_hover_border: 0xAFBED0,
    row_hover_bg: 0xEDF1F6,
    tree_selected_bg: 0xE1EAF7,
    pill_bg: 0xEDF1F6,
    pill_border: 0xC7D1DD,
    scrollbar_thumb: 0x8E9FB4,
    editor_bg: 0xFAFBFD,
    editor_side_header_bg: 0xF3F5F8,
    info_card_bg: 0xF3F5F8,
    info_icon_box_bg: 0xE7ECF3,
    git_badge_bg: 0xE4EAF2,
    commit_hover_bg: 0x124F9F,
    terminal_cursor: 0x175FBD,
    white: 0x202936,
    on_accent: 0xFFFFFF,
    warning_bg: 0xFFF4CF,
    warning_text: 0x704B00,
    error_bg: 0xFFF0F0,
    error_text: 0xAD2332,
    kbd_bg: 0xECF0F5,
    kbd_border: 0xBAC6D5,
    active_line_no: 0x3C495A,
    comment_token: 0x476D39,
    match_accent: 0x175FBD,
    line_no: 0x637187,
    line_no_add: 0x28653B,
    line_no_del: 0x984047,
    diff_add_bg: 0x2EA0431F,
    diff_add_mark: 0x216E42CC,
    diff_del_bg: 0xF851491F,
    diff_del_mark: 0xB42332D1,
    selection_hsla: (0.591, 0.92, 0.578, 0.22),
};

pub fn colors() -> &'static Palette {
    match omaterm_terminal::theme_mode() {
        ThemeMode::Dark => &DARK,
        ThemeMode::Light => &LIGHT,
    }
}

pub fn resolve_mode(setting: Option<&str>, system: ThemeMode) -> ThemeMode {
    match setting {
        Some("light") => ThemeMode::Light,
        Some("dark") => ThemeMode::Dark,
        _ => system,
    }
}

/// View-level theme preference: an explicit palette or live system follow.
/// `System` tracks the desktop appearance (Omarchy theme → portal
/// `color-scheme` → GPUI `window.appearance()`); explicit modes ignore it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ThemePreference {
    System,
    Dark,
    Light,
}

impl ThemePreference {
    /// Parse the `appearance.theme` config value. Unknown values follow the
    /// system (the loader already rejects typos with a startup warning).
    pub fn from_setting(setting: Option<&str>) -> ThemePreference {
        match setting {
            Some("dark") => ThemePreference::Dark,
            Some("light") => ThemePreference::Light,
            _ => ThemePreference::System,
        }
    }

    /// Canonical config string for persistence.
    pub fn as_setting(self) -> &'static str {
        match self {
            ThemePreference::System => "system",
            ThemePreference::Dark => "dark",
            ThemePreference::Light => "light",
        }
    }

    /// Resolve against the live system mode.
    pub fn resolve(self, system: ThemeMode) -> ThemeMode {
        match self {
            ThemePreference::Dark => ThemeMode::Dark,
            ThemePreference::Light => ThemeMode::Light,
            ThemePreference::System => system,
        }
    }
}

/// Map a GPUI window appearance to a palette mode.
pub fn appearance_mode(appearance: gpui::WindowAppearance) -> ThemeMode {
    match appearance {
        gpui::WindowAppearance::Light | gpui::WindowAppearance::VibrantLight => ThemeMode::Light,
        gpui::WindowAppearance::Dark | gpui::WindowAppearance::VibrantDark => ThemeMode::Dark,
    }
}

/// Preserve file-type hue while bringing bright dark-theme icon inks onto paper.
pub fn light_icon_ink(color: u32) -> u32 {
    let channel = |shift: u32| ((color >> shift) & 255u32) * 3u32 / 5u32;
    (channel(16) << 16) | (channel(8) << 8) | channel(0)
}

/// Opacity of the terminal header background (`#0f1318` at 90%).
pub const PANE_HEADER_BG_OPACITY: f32 = 0.90;
/// Opacity of the floating pane toolbar background.
pub const PANE_TOOLBAR_BG_OPACITY: f32 = 0.95;
/// Inset focus-stroke alpha on the active pane.
pub const PANE_FOCUS_STROKE_ALPHA: f32 = 0.34;
/// Inset top-accent alpha on the active top tab.
pub const TAB_ACTIVE_TOP_ACCENT_ALPHA: f32 = 0.9;

/// Pack an `0xRRGGBB` literal with float alpha into `0xRRGGBBAA` for
/// `gpui::rgba`. `gpui::rgb()` drops the low byte and forces opaque, so
/// translucent surfaces must go through here.
pub fn with_alpha(rgb: u32, alpha: f32) -> u32 {
    (rgb << 8) | (alpha.clamp(0.0, 1.0) * 255.0).round() as u32
}

#[cfg(test)]
#[path = "theme_tests.rs"]
mod tests;
