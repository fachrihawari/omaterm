//! Shared UI component layer.
//!
//! Before this module the app rendered chrome ad hoc: single-line text
//! editing existed in five bespoke implementations (Git fields, palette
//! query, cheatsheet filter, branch picker, terminal find) and every
//! "button" was a bare `div().on_mouse_down(..)`. That duplication is the
//! root cause of the input/behaviour drift this layer removes.
//!
//! Components here are GPUI-side only (`apps/omaterm`); `omaterm-core`
//! stays GPUI-free. Each component owns its model, its key routing, and
//! its render so a surface cannot accidentally implement a divergent
//! variant.

pub mod text_field;
pub mod tooltip;
