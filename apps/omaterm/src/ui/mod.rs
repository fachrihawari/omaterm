//! UI v5 exact-design foundation (P1).
//!
//! Test-only for now (`#[cfg(test)] mod ui` in `main.rs`): pure theme
//! tokens and shell geometry with no GPUI dependency and zero production
//! impact. Wiring into the render path happens in P2 after the reference
//! freeze, so no visual behavior changes yet.
pub mod assets;
pub mod components;
pub mod geometry;
pub mod metrics;
pub mod primitives;
pub mod prompt;
pub mod tokens;
// The theme palette is fully wired into the render path, but a few fields are
// reserved for surfaces that do not exist yet (`lime`, `info_card_bg`,
// `info_icon_box_bg`, `git_badge_bg`, `terminal_cursor`, and the
// `PANE_HEADER_BG_OPACITY` constant for a pane header the v5 shell removed).
// They are kept because the palette is a documented contract with the light
// theme, and deleting half a palette pair would make the next surface's color
// choice ad hoc. Verified by removing this allow: exactly those items are
// unused, nothing else.
#[allow(dead_code)]
pub mod theme;
