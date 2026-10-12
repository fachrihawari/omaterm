//! Shared semantic palettes for chrome, editor, and terminal surfaces.
//! Dark values are the Oma Neon palette; light values preserve the paper hierarchy.

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
    pub sidebar_bg: u32,
    pub sidebar_hover_bg: u32,
    pub sidebar_edge: u32,
    pub sidebar_tint: u32,
}

pub const DARK: Palette = Palette {
    // Oma Neon. Light mode keeps the paper palette below.
    bg: 0x13111D,
    bg2: 0x0F0E17,
    panel: 0x14121F,
    panel2: 0x221D35,
    panel3: 0x2F2848,
    border: 0x262037,
    border2: 0x3A3156,
    text: 0xF2EFFF,
    text2: 0xCFC8EC,
    muted: 0x9C93C4,
    muted2: 0x6C6394,
    blue: 0x4CC9F0,
    blue2: 0x7C5CFF,
    cyan: 0x9D7CFF,
    green: 0x2EE6A6,
    yellow: 0xFFD23F,
    orange: 0xFF8A3D,
    red: 0xFF4D6D,
    purple: 0xD96BFF,
    lime: 0xB8F35A,
    header_bg: 0x0F0E17,
    active_tab_bg: 0x231D3A,
    selected_project_bg: 0x2E2456,
    cmd_hover_bg: 0x221D35,
    cmd_hover_border: 0x3A3156,
    row_hover_bg: 0x1D1930,
    tree_selected_bg: 0x2E2456,
    pill_bg: 0x221D35,
    pill_border: 0x3A3156,
    scrollbar_thumb: 0x4A4070,
    editor_bg: 0x0F0E17,
    editor_side_header_bg: 0x15131F,
    info_card_bg: 0x1B1729,
    info_icon_box_bg: 0x14121F,
    git_badge_bg: 0x2F2848,
    commit_hover_bg: 0xB39DFF,
    terminal_cursor: 0xFF4FD8,
    white: 0xFFFFFF,
    on_accent: 0xFFFFFF,
    warning_bg: 0x3A2E0E,
    warning_text: 0xFFD23F,
    error_bg: 0x3D1222,
    error_text: 0xFF7A93,
    kbd_bg: 0x221D35,
    kbd_border: 0x3A3156,
    active_line_no: 0x9C93C4,
    comment_token: 0x7A70A8,
    match_accent: 0xFF4FD8,
    line_no: 0x564E78,
    line_no_add: 0x2E9E77,
    line_no_del: 0xB04A60,
    diff_add_bg: 0x2EE6A61F,
    diff_add_mark: 0x2EE6A6CC,
    diff_del_bg: 0xFF4D6D1F,
    diff_del_mark: 0xFF4D6DD1,
    selection_hsla: (0.708, 1.0, 0.74, 0.30),
    sidebar_bg: 0x1A1729,
    sidebar_hover_bg: 0x272140,
    sidebar_edge: 0x0A0912,
    sidebar_tint: 0x120F1E,
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
    sidebar_bg: 0xE6EDF5,
    sidebar_hover_bg: 0xD7E1EE,
    sidebar_edge: 0xC3CEDC,
    sidebar_tint: 0xF7F9FC,
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

/// Pane header background (`BG2` at 90%).
pub const PANE_HEADER_BG_OPACITY: f32 = 0.90;
/// Wash over unfocused panes in a split tab.
pub const PANE_INACTIVE_DIM: f32 = 0.42;
/// Floating pane toolbar background.
pub const PANE_TOOLBAR_BG_OPACITY: f32 = 0.95;
/// Terminal text selection.
pub const TERMINAL_SELECTION_OPACITY: f32 = 0.38;
/// Accent outline around the active session tab.
pub const ACTIVE_TAB_OUTLINE_OPACITY: f32 = 0.55;
/// Translucent sidebar glass, and the plate behind its labels.
pub const SIDEBAR_TINT_OPACITY: f32 = 0.42;
pub const SIDEBAR_LABEL_SCRIM_OPACITY: f32 = 0.82;
/// Was the faint full-rectangle focus stroke.
#[allow(dead_code)]
pub const PANE_FOCUS_STROKE_ALPHA: f32 = 0.34;
/// Was the translucent top tab accent.
#[allow(dead_code)]
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
