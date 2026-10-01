//! VS Code-workbench shell theme and layout helpers.
//!
//! GPUI-free. Rendering and key/mouse wiring live in `main.rs`; this module
//! owns the dark workbench design tokens (tuned from the supplied
//! `vscode_ui_clone.html` reference), structural dimensions, and the pure
//! state transitions the shell needs (sidebar collapse, activity switching,
//! status text). Components request semantic roles, never raw hex.
//!
//! The terminal canvas itself keeps its existing renderer colors; only the
//! surrounding chrome (title/activity/sidebar/tabs/context/status) uses
//! these tokens.

// ---- Palette (VS Code Dark+ baseline, tuned from the reference) ----

/// Window frame, activity rail, and bottom panel background.
pub const WINDOW_BG: u32 = 0x181818;
/// Contextual sidebar background (Explorer / Source Control).
pub const SIDEBAR_BG: u32 = 0x1F1F1F;
/// Tab strip and secondary header background.
pub const TABSTRIP_BG: u32 = 0x252526;
/// Main work-surface background (terminal tabs, diff preview).
pub const SURFACE_BG: u32 = 0x1E1E1E;
/// Inactive tab background.
pub const TAB_INACTIVE_BG: u32 = 0x2D2D2D;
/// Raised surfaces: search/commit inputs.
pub const INPUT_BG: u32 = 0x313131;
/// Structural seams between chrome regions.
pub const BORDER: u32 = 0x2B2B2B;
/// Selected sidebar row / pressed control.
pub const SELECTION_BG: u32 = 0x37373D;
/// Accent: active tab top edge, focus ring, status bar.
pub const ACCENT: u32 = 0x007ACC;
/// Primary label text.
pub const TEXT: u32 = 0xCCCCCC;
/// Dimmed metadata text.
pub const MUTED: u32 = 0x8B8B8B;
/// Inactive tab label text.
pub const TAB_INACTIVE_TEXT: u32 = 0x969696;
/// Active label text.
pub const TEXT_BRIGHT: u32 = 0xFFFFFF;
/// Notice strip severity colors (existing banner semantics).
pub const WARN_BG: u32 = 0x3F321D;
pub const WARN_TEXT: u32 = 0xFDE68A;
pub const ERROR_BG: u32 = 0x3F1D1D;
pub const ERROR_TEXT: u32 = 0xFCA5A5;

// ---- Structural dimensions (px) ----

/// Activity rail width (icon column).
pub const ACTIVITY_WIDTH: f32 = 48.0;
/// Default contextual-sidebar width.
pub const SIDEBAR_DEFAULT: f32 = 260.0;
/// Narrowest usable sidebar (tree indentation stays readable).
pub const SIDEBAR_MIN: f32 = 190.0;
/// Widest sidebar before the terminal surface starves.
pub const SIDEBAR_MAX: f32 = 460.0;
/// Command/title row height.
pub const TITLE_HEIGHT: f32 = 35.0;
/// Terminal/diff tab strip height.
pub const TAB_HEIGHT: f32 = 35.0;
/// Context (breadcrumb) row height under the tabs.
pub const CONTEXT_HEIGHT: f32 = 28.0;
/// Status bar height.
pub const STATUS_HEIGHT: f32 = 22.0;

// ---- Activity rail glyphs (Nerd Font codepoints already used by the
// file/git panels, same family the terminal grid resolves) ----

/// Explorer activity icon (folder mark, shared with the file tree).
pub const EXPLORER_ICON: char = '\u{f07b}';
/// Source Control activity icon (git mark, shared with `.git` rows).
pub const SOURCE_ICON: char = '\u{e65d}';

// ---- Pure layout/state helpers ----

/// Clamp a requested sidebar width into the usable range. Non-finite
/// values (drag math must never produce these, but the render path runs
/// every frame) fall back to the default instead of poisoning layout.
pub fn clamp_sidebar_width(width: f32) -> f32 {
    if !width.is_finite() {
        return SIDEBAR_DEFAULT;
    }
    width.clamp(SIDEBAR_MIN, SIDEBAR_MAX)
}

/// Sidebar-collapsed state after pressing an activity-rail icon.
/// Pressing the already-active icon toggles; pressing the other panel
/// always reveals the sidebar on that panel.
pub fn activity_press_collapsed(collapsed: bool, pressed_active: bool) -> bool {
    if pressed_active { !collapsed } else { false }
}

/// Change-count badge for the Source Control activity icon.
/// `None` when the tree is clean so no badge renders.
pub fn change_badge(staged: usize, unstaged: usize, untracked: usize) -> Option<String> {
    let total = staged + unstaged + untracked;
    if total == 0 {
        None
    } else {
        Some(total.to_string())
    }
}

/// Compact staged/working-tree summary for the status bar, e.g.
/// `"2 staged · 3 changed · 1 untracked"`. `None` when clean.
pub fn change_summary(staged: usize, unstaged: usize, untracked: usize) -> Option<String> {
    let mut parts = Vec::new();
    if staged > 0 {
        parts.push(format!("{staged} staged"));
    }
    if unstaged > 0 {
        parts.push(format!("{unstaged} changed"));
    }
    if untracked > 0 {
        parts.push(format!("{untracked} untracked"));
    }
    if parts.is_empty() {
        None
    } else {
        Some(parts.join(" · "))
    }
}

/// Branch label with a dirty marker (`main*`). `None` when the project is
/// not a repo so the status bar omits git rather than inventing metadata.
pub fn branch_label(branch: Option<&str>, dirty: bool) -> Option<String> {
    branch.map(|name| {
        if dirty {
            format!("{name}*")
        } else {
            name.to_string()
        }
    })
}

#[cfg(test)]
mod tests {
    use super::{
        SIDEBAR_DEFAULT, SIDEBAR_MAX, SIDEBAR_MIN, activity_press_collapsed, branch_label,
        change_badge, change_summary, clamp_sidebar_width,
    };

    #[test]
    fn sidebar_width_clamps_to_usable_range() {
        assert_eq!(clamp_sidebar_width(0.0), SIDEBAR_MIN);
        assert_eq!(clamp_sidebar_width(100.0), SIDEBAR_MIN);
        assert_eq!(clamp_sidebar_width(260.0), 260.0);
        assert_eq!(clamp_sidebar_width(900.0), SIDEBAR_MAX);
        assert_eq!(clamp_sidebar_width(f32::NAN), SIDEBAR_DEFAULT);
        assert_eq!(clamp_sidebar_width(f32::INFINITY), SIDEBAR_DEFAULT);
    }

    #[test]
    fn activity_press_toggles_only_on_active_icon() {
        // Visible + active icon -> collapse.
        assert!(activity_press_collapsed(false, true));
        // Visible + other icon -> stay visible (switch panel).
        assert!(!activity_press_collapsed(false, false));
        // Collapsed + either icon -> reveal.
        assert!(!activity_press_collapsed(true, true));
        assert!(!activity_press_collapsed(true, false));
    }

    #[test]
    fn change_badge_hides_when_clean() {
        assert_eq!(change_badge(0, 0, 0), None);
        assert_eq!(change_badge(1, 0, 0), Some("1".to_string()));
        assert_eq!(change_badge(1, 2, 3), Some("6".to_string()));
    }

    #[test]
    fn change_summary_lists_only_nonzero_groups() {
        assert_eq!(change_summary(0, 0, 0), None);
        assert_eq!(change_summary(2, 0, 0), Some("2 staged".to_string()));
        assert_eq!(
            change_summary(2, 3, 1),
            Some("2 staged · 3 changed · 1 untracked".to_string())
        );
        assert_eq!(change_summary(0, 0, 5), Some("5 untracked".to_string()));
    }

    #[test]
    fn branch_label_marks_dirty_and_omits_non_repo() {
        assert_eq!(branch_label(None, false), None);
        assert_eq!(branch_label(None, true), None);
        assert_eq!(branch_label(Some("main"), false), Some("main".to_string()));
        assert_eq!(branch_label(Some("main"), true), Some("main*".to_string()));
    }
}
