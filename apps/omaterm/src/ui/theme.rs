//! UI color tokens — the "Oma Neon" palette.
//!
//! Deep ink-violet surfaces with electric accents: violet for focus and
//! selection, magenta for the cursor and search hits, sky blue for links and
//! live state, mint/amber/coral for git and status.
//!
//! Edit colors here. The terminal's own colors (default foreground and
//! background, cursor, 16 ANSI colors) live in
//! `crates/omaterm-terminal/src/color.rs`; `BG2` and `TERMINAL_CURSOR` must
//! stay equal to `DEFAULT_BG` and `CURSOR_COLOR` there (a test enforces it).
//!
//! Values are `0xRRGGBB`. Translucent surfaces use separate opacity
//! constants and go through `with_alpha`.

// Surfaces, darkest to lightest.
/// Terminal well and the session bar above it.
pub const BG2: u32 = 0x0F0E17;
/// Window chrome behind the panels.
pub const BG: u32 = 0x13111D;
/// Inspector and other content panels.
pub const PANEL: u32 = 0x14121F;
/// Projects sidebar when it is opaque.
pub const SIDEBAR_BG: u32 = 0x1A1729;
pub const SIDEBAR_HOVER_BG: u32 = 0x272140;
/// Sidebar edge: darker than the sidebar so it reads as a fold.
pub const SIDEBAR_EDGE: u32 = 0x0A0912;
/// Translucent sidebar: a light tint over the blurred desktop, so the
/// glass reads as glass. Text does not sit on this; it sits on
/// `SIDEBAR_LABEL_SCRIM_OPACITY`, which stays dark over a light or dark
/// wallpaper.
pub const SIDEBAR_TINT: u32 = 0x120F1E;
pub const SIDEBAR_TINT_OPACITY: f32 = 0.42;
/// Plate behind sidebar labels. Combined with the tint above, this stays
/// near-black whether the desktop behind the window is light or dark.
pub const SIDEBAR_LABEL_SCRIM_OPACITY: f32 = 0.82;
/// Raised controls: selected segments, active tab, fields.
pub const PANEL2: u32 = 0x221D35;
pub const PANEL3: u32 = 0x2F2848;
/// Hairline separator.
pub const BORDER: u32 = 0x262037;
/// Control outline.
pub const BORDER2: u32 = 0x3A3156;

// Text.
pub const TEXT: u32 = 0xF2EFFF;
pub const TEXT2: u32 = 0xCFC8EC;
pub const MUTED: u32 = 0x9C93C4;
pub const MUTED2: u32 = 0x6C6394;
pub const WHITE: u32 = 0xFFFFFF;

// Accents.
/// Primary accent: focus, selection, active state, primary actions.
pub const ACCENT: u32 = 0x9D7CFF;
/// Second accent: cursor, search hits, things that must be found fast.
pub const ACCENT2: u32 = 0xFF4FD8;
/// Links and live state (running shell, open folder).
pub const BLUE: u32 = 0x4CC9F0;
/// Pressed/dragging controls.
pub const BLUE2: u32 = 0x7C5CFF;
pub const GREEN: u32 = 0x2EE6A6;
pub const YELLOW: u32 = 0xFFD23F;
pub const ORANGE: u32 = 0xFF8A3D;
pub const RED: u32 = 0xFF4D6D;
pub const PURPLE: u32 = 0xD96BFF;
pub const LIME: u32 = 0xB8F35A;

// State fills.
pub const HEADER_BG: u32 = BG2;
pub const ACTIVE_TAB_BG: u32 = 0x231D3A;
/// Opacity of the accent outline around the active tab.
pub const ACTIVE_TAB_OUTLINE_OPACITY: f32 = 0.55;
pub const SELECTED_PROJECT_BG: u32 = 0x2E2456;
pub const CMD_HOVER_BG: u32 = 0x221D35;
pub const CMD_HOVER_BORDER: u32 = 0x3A3156;
pub const ROW_HOVER_BG: u32 = 0x1D1930;
pub const TREE_SELECTED_BG: u32 = 0x2E2456;
pub const PILL_BG: u32 = 0x221D35;
pub const PILL_BORDER: u32 = 0x3A3156;
pub const SCROLLBAR_THUMB: u32 = 0x4A4070;
pub const GIT_BADGE_BG: u32 = 0x2F2848;
pub const COMMIT_HOVER_BG: u32 = 0xB39DFF;
/// Must equal `omaterm_terminal::CURSOR_COLOR`.
pub const TERMINAL_CURSOR: u32 = 0xFF4FD8;
/// Keyboard-hint badge fill and border (see `primitives::kbd`).
pub const KBD_BG: u32 = 0x221D35;
pub const KBD_BORDER: u32 = 0x3A3156;
/// Reserved. The inspector no longer paints a project card around this fill.
#[allow(dead_code)]
pub const INFO_CARD_BG: u32 = 0x1B1729;
/// Reserved with `INFO_CARD_BG`.
#[allow(dead_code)]
pub const INFO_ICON_BOX_BG: u32 = 0x14121F;

// Editor and diff.
pub const EDITOR_BG: u32 = BG2;
pub const EDITOR_SIDE_HEADER_BG: u32 = 0x15131F;
pub const LINE_NO: u32 = 0x564E78;
pub const ACTIVE_LINE_NO: u32 = 0x9C93C4;
pub const COMMENT_TOKEN: u32 = 0x7A70A8;
/// Finder match highlight.
pub const MATCH_ACCENT: u32 = ACCENT2;
/// Editor selection wash as HSLA components (the `ACCENT` hue).
pub const SELECTION_HSLA: (f32, f32, f32, f32) = (0.708, 1.0, 0.74, 0.30);
pub const LINE_NO_ADD: u32 = 0x2E9E77;
pub const LINE_NO_DEL: u32 = 0xB04A60;
/// `GREEN` at 12% as 0xRRGGBBAA.
pub const DIFF_ADD_BG: u32 = 0x2EE6A61F;
/// `GREEN` at 80%.
pub const DIFF_ADD_MARK: u32 = 0x2EE6A6CC;
/// `RED` at 12%.
pub const DIFF_DEL_BG: u32 = 0xFF4D6D1F;
/// `RED` at 82%.
pub const DIFF_DEL_MARK: u32 = 0xFF4D6DD1;

// Opacities.
/// Pane header background (`BG2` at 90%).
pub const PANE_HEADER_BG_OPACITY: f32 = 0.90;
/// Wash over unfocused panes in a split tab.
pub const PANE_INACTIVE_DIM: f32 = 0.42;
/// Floating pane toolbar background.
pub const PANE_TOOLBAR_BG_OPACITY: f32 = 0.95;
/// Terminal text selection (`ACCENT`).
pub const TERMINAL_SELECTION_OPACITY: f32 = 0.38;
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
mod tests {
    use super::{BG2, DIFF_ADD_BG, GREEN, PANE_HEADER_BG_OPACITY, TERMINAL_CURSOR, with_alpha};

    fn pack((r, g, b): (u8, u8, u8)) -> u32 {
        (u32::from(r) << 16) | (u32::from(g) << 8) | u32::from(b)
    }

    #[test]
    fn with_alpha_packs_exact_rgba_bytes() {
        assert_eq!(with_alpha(BG2, PANE_HEADER_BG_OPACITY), 0x0F0E17E6);
        assert_eq!(with_alpha(0x141A21, 0.95), 0x141A21F2);
        assert_eq!(with_alpha(GREEN, 0.12), DIFF_ADD_BG);
        assert_eq!(with_alpha(0x123456, 2.0), 0x123456FF);
        assert_eq!(with_alpha(0x123456, -1.0), 0x12345600);
    }

    #[test]
    fn terminal_well_and_cursor_match_engine_defaults() {
        assert_eq!(BG2, pack(omaterm_terminal::DEFAULT_BG));
        assert_eq!(TERMINAL_CURSOR, pack(omaterm_terminal::CURSOR_COLOR));
    }
}
