//! UI v5 exact-design foundation (P1).
//!
//! Test-only for now (`#[cfg(test)] mod ui` in `main.rs`): pure theme
//! tokens and shell geometry with no GPUI dependency and zero production
//! impact. Wiring into the render path happens in P2 after the reference
//! freeze, so no visual behavior changes yet.
pub mod assets;
pub mod geometry;
pub mod metrics;
pub mod primitives;
// P2 wires these tokens into the render path; until then they are an
// unwired foundation, not dead logic.
// (allow(dead_code) is scoped to this declaration.)
#[allow(dead_code)]
pub mod theme;
