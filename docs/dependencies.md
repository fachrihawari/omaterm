# Toolchain and Dependency Inventory

## Current verification state

Milestone 1 selected the published `gpui` 0.2.2 release and validated it on
Omarchy/Hyprland. `rust-toolchain.toml` pins the working Rust 1.98.1 toolchain
with `rustfmt` and `clippy`. This is a verified working toolchain, not an MSRV.

| Component | Selection | License evidence | Verification |
|---|---|---|---|
| Rust toolchain | 1.98.1 (`48a229cea`, LLVM 22.1.8) | Official distribution | `rustc --version --verbose`; build/checks pass on 2026-09-26 |
| GPUI/platform crates | `gpui` 0.2.2 from crates.io, `wayland` + `x11` features | Apache-2.0; crate manifest and crates.io metadata | Linux build and Wayland launch pass on 2026-09-26 |
| Native Linux packages | See M1 native package record below | Arch package metadata | Present and linked during M1 build on 2026-09-26 |
| Core ID/error dependencies | `uuid` 1.26.1; `thiserror` 2.0.21 | Selected crate manifests | M2 core/workspace checks pass on 2026-09-26 |
| `alacritty_terminal` | Pending M3, exact compatible release | Selected package | Not verified |
| PTY provider | Pending M3 API/lifecycle evaluation | Selected package | Not verified |
| `serde`, `serde_json`, `libc` for protocol/IPC | `serde` 1.0.228, `serde_json` 1.0.149, existing `libc` 0.2.189 resolution | Permissive dual licenses in selected manifests; full transitive review pending | M8 protocol/transport targeted tests and Clippy pending final M8 integration |
| M10 key storage/encryption/compression | Unselected; research after M5–M9 prerequisites | Must review crate and native-service licenses | No API, license, or runtime verification yet |

## Selection record

For each introduced dependency record package/source URL, exact version or Git
revision, direct/transitive purpose, license expression and source license file,
verification date, commands/results, and platform constraints. Commit `Cargo.lock`.
Record `rustc --version --verbose` and `cargo --version`, then pin the working
toolchain. Do not claim an MSRV solely from the edition or a blueprint example.

M1 records GPUI initialization/feature findings and native compiler/linker packages;
M3 records Alacritty APIs and PTY provider behavior actually exercised. Add license
inventory tooling once Cargo metadata exists, and review the transitive inventory
before release. Unknown licenses remain unresolved, not implicitly approved.

### Milestone 1 record — 2026-09-26

- `gpui` 0.2.2: direct GPU UI dependency from crates.io, published 2025-10-22,
  checksum `979b45cfa6ec723b6f42330915a1b3769b930d02b2d505f9697f8ca602bee707`;
  Apache-2.0 per its crate manifest and crates.io metadata. Both `wayland` and
  `x11` features are explicitly enabled with default features disabled.
- `Cargo.lock`: committed application resolution. A full transitive license
  review remains required before release; no project license has been selected.
- Local Arch native packages used by GPUI: `wayland` 1.26.0-1 (MIT),
  `libxkbcommon` 1.13.2-1 (MIT), `libx11` 1.8.13-1 (MIT AND X11), `libxcb`
  1.17.0-1 (X11), `fontconfig` 2:2.18.3-2 (HPND AND Unicode-DFS-2016),
  `freetype2` 2.14.3-1 (FTL OR GPL-2.0-or-later), `mesa` 1:26.2.2-1
  (MIT AND BSD-3-Clause AND SGI-B-2.0), `vulkan-icd-loader` 1.4.357.0-1
  (Apache-2.0), and `pkgconf` 3.0.7-1 (ISC).
- CI installs Ubuntu counterparts: `libfontconfig1-dev`, `libfreetype-dev`,
  `libvulkan1`, `libwayland-dev`, `libx11-dev`, `libxcb1-dev`,
  `libxkbcommon-dev`, and `pkg-config`.
- `cargo build --workspace`, `cargo test --workspace`, and
  `cargo clippy --workspace --all-targets -- -D warnings` complete successfully.
  Cargo reports the upstream `proc-macro-error2` 2.0.1 future-incompatibility
   warning; it is transitive and does not produce a Clippy warning.

### Milestone 2 record — 2026-09-26

- `uuid` 1.26.1: direct typed-ID dependency from crates.io with only the `v4`
  feature enabled. License `Apache-2.0 OR MIT`, verified in its selected crate
  manifest and `LICENSE-APACHE`/`LICENSE-MIT` package files.
- `thiserror` 2.0.21: direct library-error derive dependency from crates.io.
  License `MIT OR Apache-2.0`, verified in its selected crate manifest and
  package license files.
- `cargo fmt --all --check`, `cargo test -p omaterm-core`, `cargo test
  --workspace`, `cargo clippy --workspace --all-targets -- -D warnings`, and
  `cargo build --workspace` pass on 2026-09-26. Interactive Wayland validation
  remains pending and is tracked in `docs/status.md`.

### Milestone 3 record — 2026-09-26 (Phase A: headless PTY/parser)

- `alacritty_terminal` =0.26.0: direct terminal-emulation dependency from
  crates.io (published 2026-04-06, checksum
  `bda177466b9524d59f1b12f0dd30b68696788e9992a7e959021c4a0ed96fcf59`).
  License `Apache-2.0` per its crate manifest (`LICENSE-APACHE` in package).
  MSRV 1.85.0, edition 2024; working toolchain 1.98.1, no conflict.
  Verified API surface against the selected package source:
  `Term::new(Config, &impl Dimensions, EventListener)`, `Term::resize`,
  `scroll_display(Scroll::Delta/PageUp/PageDown/Top/Bottom)`,
  `renderable_content()` (visible cells + cursor + display offset),
  `grid()`/`colors()`/`mode()`, `EventListener::send_event(&self, Event)`,
  `Event::{Title, ResetTitle, Bell, Wakeup, PtyWrite, ColorRequest,
  ChildExit(ExitStatus), Exit}`, `vte::ansi::Processor::advance`,
  `Config { scrolling_history: 10_000 (default), .. }`.
  Breaking change vs 0.25.x: `ChildExit` carries `ExitStatus`, not `i32`.
- PTY provider: built-in `alacritty_terminal::tty` (Unix backend via
  `rustix-openpty` 0.2.0 + `libc`, `polling`, `signal-hook`). No extra PTY
  crate added. Rationale: version-locked with the emulator, correct
  controlling-terminal/session/`TIOCSWINSZ`/`SIGHUP`-on-drop semantics,
  child-only env via `Options::env` (parent env never mutated), race-free
  `SIGCHLD` exit pipe, non-blocking master. `portable-pty` evaluated and
  deferred (heavier transitive tree, unneeded Windows/macOS scope for v0.1);
  raw `nix`/`rustix` forkpty rejected for M3 (re-implements fixed bugs).
  Transitive `vte` 0.15.0 (`Apache-2.0 OR MIT`) provides the VT parser.
- Child env (via `Options::env`, child-only): `TERM=xterm-256color`,
  `COLORTERM=truecolor`, `TERM_PROGRAM=OmaTerm`, `OMATERM=1`. Justification:
  the alacritty parser genuinely handles 256 + truecolor SGR, app-cursor,
  and bracketed paste; no further capability claims made.
- `libc` 0.2: direct dependency for `poll()`-based PTY waiting
  (`poll_fd_readable`), so the reader thread sleeps in the kernel instead of
  spin-polling. License MIT/Apache-2.0 per its crate manifest.
- `async-channel` =2.5.0 (apps/omaterm only): bounded snapshot channel
  between the PTY reader thread and the GPUI main thread. Already in the
  lock as a GPUI transitive dependency, so no new compiles. License
  `Apache-2.0 OR MIT`. Event-driven `recv().await` delivery replaced a
  16ms poll timer that cost ~3.5%/core in debug builds.
- `tracing` 0.1 (apps/omaterm): structured warnings (e.g. non-monospace
  font detection in debug builds). Same version/license as the existing
  workspace use. No subscriber is installed yet, so output currently goes
  nowhere in release; debug-assertion probe only.
- `cargo tree -p omaterm-terminal` shows only `alacritty_terminal`, `libc`,
  `omaterm-core`, `thiserror`, `tracing` — no `gpui` dependency.
- Phase A checks 2026-09-26: 16 `omaterm-terminal` unit tests
  (engine dimensions, SGR, title/bell, DSR replies, alt-screen, scroll,
  wide chars, input encoder) + 5 PTY integration tests
  (spawn/echo, resize, exit/reap, `/proc` CWD, combining) pass;
  `cargo test --workspace` (9 core + 21 terminal) green;
   `cargo clippy --workspace --all-targets -- -D warnings` clean.

### Milestone 6 record — 2026-09-27 (implementation in progress)

- `serde` =1.0.228 with derive and `serde_json` =1.0.149 are direct
  `omaterm-state` dependencies for versioned, human-readable workspace snapshots.
  Both use permissive dual-license expressions (MIT OR Apache-2.0) in the
  selected package manifests. `uuid` =1.26.1 is a direct dependency for snapshot
  ID parsing; version/license evidence is recorded in M2 above. `thiserror`
  reuses the workspace-selected version.
- Versions were locked by Cargo during implementation and checked on Rust
  1.98.1. `cargo check --workspace` and Clippy passed; final tests and desktop
  verification are still pending, so this is not yet a completed M6 selection
  verification record.

### Milestone 8 transport foundation — 2026-09-27 (partial)

- `omaterm-protocol` uses exact locked `serde` 1.0.228 and `serde_json`
  1.0.149 for v1 DTOs and JSON framing. Their selected crate manifests record
  MIT OR Apache-2.0 licensing. No new protocol serialization dependency was
  needed beyond packages already in `Cargo.lock`.
- `omaterm-ipc` uses the existing `libc` 0.2.189 resolution for `flock`,
  `SO_PEERCRED`, and UID validation. It uses Rust's standard Unix socket and
  filesystem APIs for transport and endpoint handling.
- Targeted protocol and socket tests pass on Rust 1.98.1. This is a partial
  dependency verification; desktop integration and full workspace gates remain
  pending. The repository-wide transitive license review remains outstanding.

### Milestone 7/8 desktop IPC integration — 2026-09-27

- `apps/omaterm` adds direct `base64` =0.22.1 (wire `terminal.send` decoding;
  already in `Cargo.lock` as a transitive dependency, now also a direct one;
  Apache-2.0 OR MIT), `serde_json` =1.0.149 (IPC response DTO construction;
  MIT OR Apache-2.0), and `uuid` =1.26.1 (UUID selector parsing; Apache-2.0 OR
  MIT). `uuid` was already a direct workspace dependency via `omaterm-core`;
  no new version was introduced.
- Session/local-user credential secrets are three concatenated UUID v4
  `simple()` strings generated via the existing `uuid` dependency (no new RNG
  crate). No keyring, encryption, or compression dependencies were added.
- Verified with `cargo tree -p omaterm`, `cargo clippy --workspace
  --all-targets -- -D warnings`, the targeted suites in `docs/status.md`, and
  release Wayland E2E on Rust 1.98.1. Transitive license review remains
  outstanding; no project license selected.

## Reference provenance

Kero and Zed terminal/terminal-view code are behavioral/architectural references.
Record inspected URLs and revisions with relevant findings at implementation time.
Do not copy GPL implementation without an explicit compatible project-license
decision. GPUI has its own licensing context: inspect its selected package rather
than inferring its license from unrelated Zed crates.

Project license: **undecided**. This document records dependencies; it does not
choose the application's license.
