# Milestone 1 — GPUI Boot

> Get a GPUI window rendering on Linux with Wayland and X11 support.

## Overview

This is the foundational milestone. The goal is to set up the Cargo workspace, add GPUI as a dependency, and render a basic window with background color and text on Omarchy/Wayland.

This milestone validates that the GPUI toolchain works on the target platform before any terminal complexity is introduced.

## Goals

- [ ] Cargo workspace initialized
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
# Set rust-version after verifying the selected GPUI/toolchain combination.
```

### App `Cargo.toml`

```toml
[package]
name = "omaterm"
version = "0.1.0"
edition.workspace = true

[[bin]]
name = "omaterm-desktop"
path = "src/main.rs"

[dependencies]
# Add the verified GPUI release or Git dependency with an explicit rev.
```

> **Dependency gate:** these snippets are illustrative. Inspect current GPUI
> examples and platform initialization. Select an exact release or Git revision,
> verify licensing, commit `Cargo.lock`, and record the tested Rust version and
> native packages in [dependencies.md](dependencies.md). Once verified, pin that
> toolchain in `rust-toolchain.toml`; `stable` is the bootstrap choice.

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

Record commands, toolchain, compositor/session, and results in [status.md](status.md).
Run the workspace quality gate from `AGENTS.md`. Enable CI for those same checks
once the workspace builds, with native packages matching the dependency inventory.
Headless CI does not replace the Wayland smoke test.

- [ ] `cargo build` succeeds with no errors
- [ ] `cargo run` opens a window on Wayland (Omarchy/Hyprland)
- [ ] Window displays a background color and text
- [ ] Window can be closed cleanly (no crash, no hang)
- [ ] `cargo clippy --workspace --all-targets -- -D warnings` passes
- [ ] X11 feature compiles (if available, test on X11 session)
- [ ] GPUI revision, toolchain, native dependencies, and licenses recorded
- [ ] Application lockfile tracked and workspace checks automated in CI

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
