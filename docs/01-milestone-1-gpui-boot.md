# Milestone 1 — GPUI Boot

> Get a GPUI window rendering on Linux with Wayland and X11 support.

## Overview

This is the foundational milestone. The goal is to set up the Cargo workspace, add GPUI as a dependency, and render a basic window with background color and text on Omarchy/Wayland.

This milestone validates that the GPUI toolchain works on the target platform before any terminal complexity is introduced.

## Goals

- [x] Cargo workspace initialized
- [ ] GPUI dependency compiles on Linux
- [ ] A window opens with a solid background color
- [ ] Basic text renders in the window
- [ ] Wayland works (Omarchy/Hyprland)
- [ ] X11 build support compiles

## Prerequisites

None — this is the first milestone.

## Deliverables

### Files to Create

```
omaterm/
├── Cargo.toml              # Workspace manifest
├── apps/
│   └── omaterm/
│       ├── Cargo.toml      # Binary crate
│       └── src/
│           └── main.rs     # GPUI app entry point
```

### Workspace `Cargo.toml`

```toml
[workspace]
resolver = "2"
members = [
    "apps/omaterm",
]

[workspace.package]
edition = "2024"
rust-version = "1.85"
```

### App `Cargo.toml`

```toml
[package]
name = "omaterm"
version = "0.1.0"
edition.workspace = true

[dependencies]
gpui = { git = "https://github.com/zed-industries/zed", package = "gpui" }
```

> **Note:** The exact GPUI dependency source may need adjustment. Check if GPUI is published to crates.io or if a specific Zed commit/tag should be pinned.

### `main.rs` — Minimal GPUI App

The entry point should:
1. Initialize a GPUI `App`
2. Open a single window with title "OmaTerm"
3. Render a dark background with centered text "OmaTerm" or similar
4. Handle basic window close

## Architecture Notes

```
main.rs
  └── gpui::App::new()
        └── open_window()
              └── render background + text
```

This is intentionally minimal. No domain model, no terminal, no crates beyond the app binary.

### GPUI Platform Features

For Linux, GPUI needs platform features. Check the GPUI crate for feature flags like:

```toml
[features]
default = ["wayland", "x11"]
```

Or platform-specific features via `gpui_platform`.

## Implementation Steps

1. **Create Cargo workspace** with `apps/omaterm` member
2. **Add GPUI dependency** — resolve the correct source (git, crates.io, path)
3. **Write minimal `main.rs`** — App init → window → render
4. **Build and test on Wayland** — verify window opens on Omarchy
5. **Build with X11 support** — verify it compiles (test if X11 session available)
6. **Document any GPUI quirks** encountered during setup

## Acceptance Criteria

- [ ] `cargo build` succeeds with no errors
- [ ] `cargo run` opens a window on Wayland (Omarchy/Hyprland)
- [ ] Window displays a background color and text
- [ ] Window can be closed cleanly (no crash, no hang)
- [ ] `cargo clippy` has zero warnings
- [ ] X11 feature compiles (if available, test on X11 session)

## Non-Goals

- No terminal rendering
- No pane splitting
- No project/tab model
- No input handling beyond window close
- No configuration loading
- No IPC/CLI

## References

- Blueprint §8 — UI Framework Decision
- Blueprint §8.1 — Why GPUI Fits OmaTerm
- Blueprint §48 — Wayland
- Blueprint §49 — X11
- Blueprint §68, Slice 1 — GPUI Boot
- Blueprint §69 — Agent Instructions for Starting the Repository
- GPUI source: https://github.com/zed-industries/zed/tree/main/crates/gpui
