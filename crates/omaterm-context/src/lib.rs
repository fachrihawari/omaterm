//! Project context foundation (M12, blueprint §§30–31, 50).
//!
//! Every v0.2 developer-context feature (file tree, git, diff, search, the
//! process panel) targets one deterministic filesystem root per project.
//! This crate owns root resolution, the symlink-aware filesystem boundary,
//! and the `.gitignore`/`.ignore` policy. It introduces no user-visible UI.
//!
//! Dependency direction: `omaterm-context` → `omaterm-core` (domain types
//! only). There is no `gpui` dependency; resolution runs on the desktop
//! owner thread with a bounded git wait, never on socket threads.

pub mod boundary;
pub mod ignore;
pub mod resolve;

pub use boundary::{ContextError, canonicalize_under_root};
pub use ignore::IgnoreFilter;
pub use resolve::{
    GIT_TOPLEVEL_TIMEOUT, git_toplevel_of, git_toplevel_of_with, resolve_root, resolve_root_with,
};
