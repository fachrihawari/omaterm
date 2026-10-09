//! Pure v5 shell geometry: panel widths, clamps, and rectangles.
//!
//! GPUI-free. One CSS pixel maps to one logical pixel at the baseline;
//! adjacent pane fractions must be snapped jointly at paint time, never
//! rounded independently here.

pub const PROJECTS_DEFAULT: f32 = 210.0;
pub const PROJECTS_MIN: f32 = 164.0;
pub const PROJECTS_MAX: f32 = 340.0;
pub const INSPECTOR_DEFAULT: f32 = 330.0;
pub const INSPECTOR_MIN: f32 = 280.0;
pub const INSPECTOR_MAX: f32 = 470.0;
pub const RESIZER: f32 = 4.0;
pub const HEADER_H: f32 = 42.0;
pub const STATUS_H: f32 = 24.0;

/// Clamp a Projects width; non-finite input falls back to default.
pub fn clamp_projects_width(width: f32) -> f32 {
    if !width.is_finite() {
        return PROJECTS_DEFAULT;
    }
    width.clamp(PROJECTS_MIN, PROJECTS_MAX)
}

/// Clamp an Inspector width; non-finite input falls back to default.
pub fn clamp_inspector_width(width: f32) -> f32 {
    if !width.is_finite() {
        return INSPECTOR_DEFAULT;
    }
    width.clamp(INSPECTOR_MIN, INSPECTOR_MAX)
}

/// Shell rectangles `(x, y, w, h)` for a viewport, in logical pixels.
/// Hidden panels (and their resizers) occupy zero space.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct ShellRects {
    pub projects: (f32, f32, f32, f32),
    pub projects_resizer: (f32, f32, f32, f32),
    pub header: (f32, f32, f32, f32),
    pub main_view: (f32, f32, f32, f32),
    pub inspector_resizer: (f32, f32, f32, f32),
    pub inspector: (f32, f32, f32, f32),
    pub status: (f32, f32, f32, f32),
}

pub fn shell_rects(
    viewport_w: f32,
    viewport_h: f32,
    projects_visible: bool,
    projects_width: f32,
    inspector_visible: bool,
    inspector_width: f32,
) -> ShellRects {
    let mut left = if projects_visible {
        clamp_projects_width(projects_width)
    } else {
        0.0
    };
    let mut right = if inspector_visible {
        clamp_inspector_width(inspector_width)
    } else {
        0.0
    };
    let left_resizer = if projects_visible { RESIZER } else { 0.0 };
    let right_resizer = if inspector_visible { RESIZER } else { 0.0 };
    // Preserve requested widths on wide windows, but give the terminal a
    // usable share before allocating the panels' optional extra width.
    let reserve = 240.0_f32.min((viewport_w - 452.0).max(180.0));
    let overflow = (left + right + left_resizer + right_resizer + reserve - viewport_w).max(0.0);
    let left_extra = (left - PROJECTS_MIN).max(0.0);
    let right_extra = (right - INSPECTOR_MIN).max(0.0);
    let extra = left_extra + right_extra;
    if extra > 0.0 {
        let reduction = overflow.min(extra);
        left -= reduction * left_extra / extra;
        right -= reduction * right_extra / extra;
    }
    let status_y = viewport_h - STATUS_H;
    let main_x = left + left_resizer;
    let main_w = (viewport_w - main_x - right_resizer - right).max(0.0);
    let main_h = (viewport_h - HEADER_H - STATUS_H).max(0.0);
    ShellRects {
        projects: (0.0, 0.0, left, status_y.max(0.0)),
        projects_resizer: (left, 0.0, left_resizer, status_y.max(0.0)),
        header: (main_x, 0.0, main_w + right_resizer + right, HEADER_H),
        main_view: (main_x, HEADER_H, main_w, main_h),
        inspector_resizer: (
            viewport_w - right - right_resizer,
            HEADER_H,
            right_resizer,
            main_h,
        ),
        inspector: (viewport_w - right, HEADER_H, right, main_h),
        status: (0.0, status_y, viewport_w, STATUS_H),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_rects_match_baseline_at_1440x900() {
        let r = shell_rects(1440.0, 900.0, true, 210.0, true, 330.0);
        assert_eq!(r.projects, (0.0, 0.0, 210.0, 876.0));
        assert_eq!(r.projects_resizer, (210.0, 0.0, 4.0, 876.0));
        assert_eq!(r.header, (214.0, 0.0, 1226.0, 42.0));
        assert_eq!(r.main_view, (214.0, 42.0, 892.0, 834.0));
        assert_eq!(r.inspector_resizer, (1106.0, 42.0, 4.0, 834.0));
        assert_eq!(r.inspector, (1110.0, 42.0, 330.0, 834.0));
        assert_eq!(r.status, (0.0, 876.0, 1440.0, 24.0));
    }

    #[test]
    fn hidden_panels_release_space_and_resizers() {
        let r = shell_rects(1440.0, 900.0, false, 210.0, false, 330.0);
        assert_eq!(r.projects.2, 0.0);
        assert_eq!(r.projects_resizer.2, 0.0);
        assert_eq!(r.inspector.2, 0.0);
        assert_eq!(r.inspector_resizer.2, 0.0);
        assert_eq!(r.main_view, (0.0, 42.0, 1440.0, 834.0));
        assert_eq!(r.header, (0.0, 0.0, 1440.0, 42.0));
    }

    #[test]
    fn widths_clamp_to_usable_ranges() {
        assert_eq!(clamp_projects_width(0.0), PROJECTS_MIN);
        assert_eq!(clamp_projects_width(900.0), PROJECTS_MAX);
        assert_eq!(clamp_projects_width(f32::NAN), PROJECTS_DEFAULT);
        assert_eq!(clamp_inspector_width(0.0), INSPECTOR_MIN);
        assert_eq!(clamp_inspector_width(900.0), INSPECTOR_MAX);
        assert_eq!(clamp_inspector_width(f32::INFINITY), INSPECTOR_DEFAULT);
        let r = shell_rects(1440.0, 900.0, true, 0.0, true, 9999.0);
        assert_eq!(r.projects.2, PROJECTS_MIN);
        assert_eq!(r.inspector.2, INSPECTOR_MAX);
    }

    #[test]
    fn narrow_window_preserves_terminal_and_panel_minima() {
        let r = shell_rects(640.0, 480.0, true, 340.0, true, 470.0);
        assert!(r.main_view.2 >= 180.0);
        assert!(r.projects.2 >= PROJECTS_MIN);
        assert!(r.inspector.2 >= INSPECTOR_MIN);
        assert!(
            (r.main_view.2 + r.projects.2 + r.inspector.2 + 2.0 * RESIZER - 640.0).abs() < 0.01
        );
        let wide = shell_rects(1440.0, 900.0, true, 340.0, true, 470.0);
        assert_eq!(wide.projects.2, 340.0);
        assert_eq!(wide.inspector.2, 470.0);
    }
}
