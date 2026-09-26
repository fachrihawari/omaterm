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
| Serialization/IPC/CLI dependencies | Select when their slices require them | Selected packages | Not verified |

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

## Reference provenance

Kero and Zed terminal/terminal-view code are behavioral/architectural references.
Record inspected URLs and revisions with relevant findings at implementation time.
Do not copy GPL implementation without an explicit compatible project-license
decision. GPUI has its own licensing context: inspect its selected package rather
than inferring its license from unrelated Zed crates.

Project license: **undecided**. This document records dependencies; it does not
choose the application's license.
