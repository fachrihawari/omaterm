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

### M19 acceptance hardening — 2026-10-03

- Desktop promotes already-locked `unicode-segmentation` =1.13.3 and
  `unicode-width` =0.2.2 to direct dependencies for grapheme-safe editor
  motion/deletion and combining/wide-character presentation. Sources:
  <https://crates.io/crates/unicode-segmentation/1.13.3> and
  <https://crates.io/crates/unicode-width/0.2.2>. Both selected package
  `Cargo.toml` manifests declare `MIT OR Apache-2.0`, with packaged
  `LICENSE-MIT` / `LICENSE-APACHE` evidence. No grammar runtime is added.
- Context adds direct `libc` 0.2 (existing locked 0.2.189, MIT OR Apache-2.0)
  for Unix `O_NOFOLLOW` / `O_NONBLOCK` when opening editor files; no unsafe
  calls are needed. Existing workspace/native-platform requirements apply.
- `Cargo.lock` direct-dependency edges change; no new package/version is
  resolved. Verification uses the existing 1.99.0 environment override, not
  a changed repository pin or a new MSRV claim. Exact commands/results are
  recorded in `docs/status.md`.

M15/M16 checkpoint verification on 2026-10-03 used the environment-selected
`rustc 1.99.0 (b940084d7 2026-09-28)` and
`cargo 1.99.0 (5f94df478 2026-08-27)`. `rustup show active-toolchain` reports
`RUSTUP_TOOLCHAIN` overriding the repository's unchanged 1.98.1 pin. Formatting,
serial workspace tests and Clippy passed; the parallel workspace test timed out
in terminal tests (see `docs/status.md`). No dependency versions, licenses or
`Cargo.lock` resolution changed in this checkpoint; no new MSRV is established.

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
  review remains required before release; project license MIT OR Apache-2.0
  (decided 2026-09-29).
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

### Milestone 9 record — 2026-09-27

- `clap` 4.6.7 (with `clap_builder`/`clap_derive` 4.6.7) from crates.io: direct
  `omaterm-cli` argument-parsing dependency with the `derive` feature.
  License `MIT OR Apache-2.0` per the selected package manifests in the
  Cargo registry. Checksum and registry source are pinned in `Cargo.lock`.
- `libc` 0.2: direct `omaterm-cli` dependency for credential-file UID
  validation; reuses the existing workspace `libc` 0.2 resolution
  (MIT/Apache-2.0). `base64` =0.22.1, `serde_json` =1.0.149, and
  `uuid` =1.26.1 (`v4`) are reused at their existing locked versions; no new
  request-ID, encoding, or serialization dependency was introduced.
- Verified with `cargo build -p omaterm-cli` (`target/debug/omaterm`),
  `cargo build --release --bin omaterm --bin omaterm-desktop`,
  `cargo test -p omaterm-cli` (24 tests), Clippy with `-D warnings`, and the
  release Wayland CLI/desktop proof on Rust 1.98.1 (Omarchy/Hyprland).
  Transitive license review remains outstanding; no project license selected.

### Milestone 10 foundation record — 2026-09-27 (history crypto/keyring spike + archive/recorder implementation)

Dependency selection for opt-in encrypted history. Verified on Rust 1.98.1
(Omarchy/Hyprland) with a live Secret Service daemon (`secret-tool`
store/lookup/clear round-trip PASS; `OsKeyProvider` create/reget/rotate/
remove/recreate/cleanup PASS against the real daemon with isolated
`omaterm-m10-spike` names, no entries left behind).

- `chacha20poly1305` =0.11.0: direct `omaterm-state` dependency for
  authenticated archive encryption (ChaCha20Poly1305 AEAD, compress-then-
  encrypt, per-archive HKDF-derived keys, AAD-bound pane UUID + revision).
  License `Apache-2.0 OR MIT` per its selected crate manifest. Pure Rust, no
  AES hardware assumptions. API note: 0.11 exposes sealing through
  `aead::AeadInOut::{encrypt_in_place, decrypt_in_place}` (nonce by
  reference); `Nonce::from_slice` is deprecated in favor of `TryFrom`.
- `hkdf` =0.12.4 + `sha2` =0.10.9: direct `omaterm-state` dependencies for
  `HKDF-SHA256(master, per-archive salt, "omaterm-history-v1/…")` key
  derivation (separate info strings for scrollback vs journal). Both
  `MIT OR Apache-2.0` per selected manifests. (The `keyring` subtree also
  resolves newer `hkdf` 0.13.0/`sha2` 0.11.0; same permissive licensing.)
- `getrandom` =0.4.3: direct `omaterm-state` dependency for master-key, salt,
  and nonce generation (`getrandom::fill`). `MIT OR Apache-2.0`. Chosen over
  `rand` 0.10 because `rand` 0.10 renamed `OsRng` to `SysRng` and moved core
  traits; `getrandom` has the smaller, stable API surface needed here.
- `flate2` =1.1.10 (`rust_backend` only): direct `omaterm-state` dependency
  for compress-before-encrypt (deflate). `MIT OR Apache-2.0`. Already in
  `Cargo.lock` transitively; the `rust_backend` feature keeps it pure-Rust
  (miniz_oxide) with no C zlib. Chosen over `zstd` (BSD-3-Clause + C
  library) to keep the license inventory uniform and CI native-free.
- `keyring` =4.2.0 (default `v1` feature): direct `omaterm-state` dependency
  for the production Linux master-key provider. `MIT OR Apache-2.0`,
  rust-version 1.88.0 (working toolchain 1.98.1, no conflict). On Linux the
  `v1` API uses `zbus-secret-service-keyring-store` (resolves
  `secret-service` 5.2.0 + `zbus` 5.19.0, already partially present via
  GPUI's `oo7` subtree), with a blocking `Entry::new/get_password/
  set_password/delete_credential` API so `omaterm-state` stays synchronous.
  Only the random 32-byte master key (hex-encoded) is ever stored; keyring
  errors map to `Locked`/`Denied`/`Unavailable` with no plaintext fallback.
- `zeroize` =1.9.0: direct `omaterm-state` dependency for in-memory key
  hygiene (cached keys zeroed on rotate/remove/drop). `Apache-2.0 OR MIT`.
  Also a direct `omaterm-desktop` dependency for zeroing per-job key copies
  in the background history writer.
- `libc` 0.2 (existing 0.2.189 resolution): reused for history-file owner
  validation (`geteuid` vs file uid) alongside symlink/type/permission
  checks; no new version introduced.
- `base64` =0.22.1 (Phase 1): now also a direct `omaterm-terminal`
  dependency for strict decoding of shell-reported command payloads; same
  locked version/license (`Apache-2.0 OR MIT`) already used by
  `apps/omaterm`, no new version introduced. Verified with the lifecycle
  parser/session/PTY matrix below.
- Replay spike (no new dependency): ordered PTY byte chunks + resize events
  replayed into a fresh `AlacrittyEngine` reproduce the main-screen visible
  text exactly (ANSI colors, wide/combining Unicode, soft wraps, resize);
  alt-screen bytes are excluded by policy (drop any chunk where alt is
  active before or after the advance; pre-alt scrollback preserved). Proven
  with a throwaway binary before durable-storage work, then encoded as
  `omaterm-terminal::history` unit tests.
- `toml_edit` =0.25.15 (added in the Phase 0 audit): direct `omaterm-state`
  dependency for reading/writing the `[history]` section of the canonical
  `~/.config/omaterm/config.toml` while preserving all other sections,
  comments, and formatting. License `MIT OR Apache-2.0` per its selected
  crate manifest; already present in `Cargo.lock` transitively, so no new
  native or async dependencies.
- Verified with `cargo test -p omaterm-state` (29 tests),
  `cargo test -p omaterm-terminal --lib` (89 tests),
  `cargo test --workspace -- --test-threads=1` (215 tests, 0 failures),
  `cargo clippy --workspace --all-targets -- -D warnings` (only the known
  transitive `proc-macro-error2` future-incompat notice),
  `cargo tree -p omaterm-terminal` / `-p omaterm-state` (zero `gpui`),
  and the live keyring spike above. Phase 0 re-verified with 37 state tests
  (8 new audit tests), 223 workspace tests green, and the same Clippy/fmt
  gates. `Cargo.lock` is committed with these resolutions. Transitive
  license review remains outstanding; no project license selected.

### Milestone 11 record — 2026-09-28 (packaging + full transitive license inventory)

New crates: `omaterm-logging` 0.1.0 (shared std-only `tracing` subscriber;
direct dep `tracing` 0.1.44 MIT, already in `Cargo.lock` — no new external
dependency, verified offline). `packaging/arch/PKGBUILD` builds both binaries
with `cargo build --release --locked`; `packaging/README.md` documents the
Arch build and the standalone tarball. `license=('MIT' 'Apache-2.0')` in the
PKGBUILD matches the decided project license (blueprint §70); the tag-tarball
`sha256sums` is `SKIP` until the first version tag.

Full transitive inventory generated offline from
`cargo metadata --format-version 1 --filter-platform x86_64-unknown-linux-gnu`
against the committed `Cargo.lock` (Rust 1.98.1, 2026-09-28):
583 third-party packages, **zero** with a missing license expression.
Histogram (top): 248 `MIT OR Apache-2.0`, 128 `MIT`, 74 `Apache-2.0 OR MIT`,
24 `MIT/Apache-2.0`, 20 `Apache-2.0`, 18 `Unicode-3.0` (unicode tables),
8 `BSD-3-Clause`, plus Zlib/Unlicense/CC0/ISC/BSD-2-Clause tails.
Two copyleft-adjacent entries need attention at license-choice time:
`option-ext` 0.2.0 `MPL-2.0` (file-level copyleft, no binary redistribution
trigger unmodified) and `self_cell` 1.3.0 `Apache-2.0 OR GPL-2.0-only`
(Apache-2.0 disjunct available). No GPL-only hard dependency exists.

Direct selections (locked, permissive): `async-channel` =2.5.0, `base64`
=0.22.1, `clap` 4.6.7, `gpui` =0.2.2 (Apache-2.0), `serde` =1.0.228,
`serde_json` =1.0.149, `tracing` 0.1.44, `uuid` =1.26.1, `zeroize` =1.9.0,
`thiserror` 2.0.21, `libc` 0.2.189, `alacritty_terminal` =0.26.0
(Apache-2.0), `chacha20poly1305` =0.11.0, `flate2` =1.1.10, `getrandom`
=0.4.3, `hkdf` =0.12.4, `sha2` =0.10.9, `keyring` =4.2.0, `toml_edit`
=0.25.15. `omaterm-logging` adds `tracing` 0.1.44 to the CLI graph (already
locked; no new external package) and to `apps/omaterm` alongside the
existing use. Dual lock entries (`thiserror` 1.0.69, `getrandom` 0.2/0.3,
`hkdf` 0.13.0, `sha2` 0.11.0, `toml_edit` 0.22.27, `async-channel` 1.9.0)
are transitive (GPUI/keyring subtrees) with the same permissive licensing.

### Milestone 12 record — 2026-09-29 (context foundation + v0.2 spike)

New workspace member `omaterm-context` 0.1.0 (direct deps only:
`ignore` =0.4.33, `omaterm-core` path, `thiserror` =2.0.21 — the existing
locked resolutions, no new async/runtime dependency). `cargo tree
-p omaterm-context` shows zero `gpui`; `cargo tree -p omaterm-core`
confirms core gained no dependency (pure `RootSource`/DTO additions).

- `ignore` =0.4.33 (crates.io, `Unlicense OR MIT` per its manifest,
  rust-version 1.88.0; working toolchain 1.98.1, no conflict): direct
  `omaterm-context` dependency for `.gitignore` + `.ignore` + parent/global
  walking policy consumed by the M13 file tree. Pure Rust (transitives
  `globset`/`walkdir`/`same-file`/`memchr`, already largely in `Cargo.lock`
  via the GPUI subtree); no native packages, no C library. Checksum and
  registry source pinned in `Cargo.lock`. Verified with 13 context unit
  tests plus the real-repo integration test below.
- System git 2.55.0 (`git --version`, Omarchy): `git rev-parse
  --show-toplevel` backs the no-pin fallback (argv vector only, never shell
  interpolation; `GIT_TERMINAL_PROMPT=0`, `--no-optional-locks`; 2s bounded
  wait with kill+reap). No new dependency — the system binary is used so
  user config/hooks keep working (blueprint §32 precedent). Proven by the
  `tests/git_roots.rs` integration (repo, nested repo, non-repo) and the
  wedged-git timeout unit test (fake `sleep`-ing executable, bounded return).
- Spike, no dependency added (M13 consumes after its own API-fit check):
  `notify` 8.2.0 stable (`CC0-1.0`, rust 1.77; 9.x is RC — stay on 8) for
  M13 watching — Linux backend is the `inotify` crate (pure Rust + `libc`,
  no new native packages). Fuzzy ranking: `fuzzy-matcher` 0.3.7 (`MIT`,
  skim algorithm) selected over `nucleo` 0.5.0 (`MPL-2.0`) to avoid adding a
  second copyleft-adjacent license to the tree (`option-ext` MPL-2.0 and
  `self_cell` Apache/GPL remain the only such entries, both compatible per
  the M11 inventory). M13 re-verifies API fit before adding either crate.

Verified with `cargo test --workspace -- --test-threads=1` (328 tests, 0
failures — 313 prior + 13 context + 1 desktop router + 1 CLI render),
`cargo clippy --workspace --all-targets -- -D warnings` (known transitive
`proc-macro-error2` notice only), `cargo fmt --all --check`,
`cargo build --release --bin omaterm --bin omaterm-desktop`,
`python3 scripts/check-docs.py`, and `git diff --check` on Rust 1.98.1
(Omarchy/Hyprland). `Cargo.lock` is committed with the `ignore` 0.4.33
resolution.

### Milestone 13 record — 2026-09-29 (file watcher + fuzzy ranking)

API-fit re-verified before feature code (M12 spike versions confirmed):

- `notify` =8.2.0 (crates.io, `CC0-1.0` per `cargo info`, rust-version
  1.77; working toolchain 1.98.1, no conflict): direct
  `omaterm-context` dependency for the M13 recursive project watcher.
  Linux backend is `inotify` 0.11.5 (pure Rust + the existing `libc`
  0.2.189 resolution — no new native packages). Stayed on stable 8.2.0
  while 9.x is RC, per the M12 spike decision. Only
  `RecommendedWatcher` + `RecursiveMode::Recursive` are consumed; events
  are debounced and revision-ordered by the desktop, and dropping the
  handle cancels watching on project switch.
- `fuzzy-matcher` =0.3.7 (crates.io, `MIT` per `cargo info`): direct
  `omaterm-context` dependency for `Ctrl+P` skim-algorithm ranking.
  Transitives are `thread_local` 1.1.10 only (already in `Cargo.lock`
  via the GPUI subtree). Selected over `nucleo` 0.5.0 (`MPL-2.0`) to
  avoid a second copyleft-adjacent license in the tree, per the M12 spike
  decision.
- `cargo tree -p omaterm-context` shows zero `gpui`; `cargo tree
  -p omaterm` confirms the desktop consumes both through
  `omaterm-context` + `omaterm-core` only.

Verified with `cargo test --workspace -- --test-threads=1` (345 tests, 0
failures), `cargo clippy --workspace --all-targets -- -D warnings`
(known transitive `proc-macro-error2` notice only), `cargo fmt
--all --check`, `cargo build --release --bin omaterm --bin
omaterm-desktop`, `python3 scripts/check-docs.py`, `git diff --check`,
and the release Wayland proofs below on Rust 1.98.1 (Omarchy/Hyprland).
`Cargo.lock` is committed with the `notify` 8.2.0 + `fuzzy-matcher`
0.3.7 resolutions.

### Milestone 13 follow-up record — 2026-09-29 (lazy loading + icons)

No new runtime dependencies. `ttf-parser` =0.25.1 is a dev-only
dependency of the desktop binary for the `icon_glyphs_exist_in_nerd_font`
coverage test (already in `Cargo.lock` transitively via GPUI; never
linked into `omaterm-desktop`). Icon codepoints were taken from the
official Nerd Fonts 3.5.1 `glyphnames.json` reference (fetched 2026-09-29)
and every tabled glyph plus both chevrons was verified present in the
locally installed `JetBrainsMonoNerdFont-Regular.ttf` (the same family
the app already resolves for its terminal grid, so no font dependency is
added). The coverage test skips gracefully where the font is absent (CI
runners).

### Milestone 14 record — 2026-10-01 (git status over the system binary)

No new dependencies of any kind: `omaterm-context/src/git.rs` spawns
the system git binary (`PATH` lookup, argv vectors only) and
`omaterm-core`/`omaterm-protocol`/`omaterm-cli`/desktop consume it
through existing `thiserror`/`serde_json`/GPUI edges. `cargo tree
-p omaterm-context` confirms zero `gpui` and zero git library —
blueprint §32 (no libgit2) holds by construction, so user
config/hooks/SSH/GPG keep working.

- System git 2.55.0 (`git --version`, Omarchy) re-verified 2026-10-01:
  the same resolution recorded in M12. Porcelain behavior the parser
  depends on was verified empirically against this version: branch
  headers require `--branch`, `-z` rename records are `<new> NUL
  <old>`, non-`-z` output C-octal-quotes non-ASCII paths (hence `-z`
  is mandatory). `git restore --source=HEAD --staged --worktree`
  (discard path) requires git ≥ 2.23; failure on older binaries
  surfaces as bounded `git_failed`, never silent.
- Verified with `cargo test --workspace -- --test-threads=1` (385
  tests green, incl. 7 real-repo git integration tests),
  `cargo clippy --workspace --all-targets -- -D warnings`,
  `cargo build --release --bin omaterm --bin omaterm-desktop`, and
  the release Wayland CLI/desktop proof on Rust 1.98.1.
  `Cargo.lock` is committed; no new resolution was introduced.

### Standing note — `proc-macro-error2` future-incompat warning (2026-10-01)

Every workspace build prints: ``proc-macro-error2 v2.0.1 ... will be
rejected by a future version of Rust'' (`E0365`: `pub use proc_macro`
re-export in its `src/lib.rs:494`). Investigated 2026-10-01, conclusion:
**no clean fix exists today, and none is needed:**

- Provenance: `proc-macro-error2 2.0.1` ← `stacksafe-macro =0.1.4` ←
  `stacksafe 0.1.4` ← `gpui 0.2.2` (our pinned GUI release). It is
  transitively required; no OmaTerm code touches it.
- Dead ends verified: `gpui` 0.2.2 is the latest release (nothing to
  bump to); `proc-macro-error2` 2.0.1 is its latest release; its
  upstream repository was **archived read-only (Jun 7, 2026)**, so no
  upstream fix will ever ship; `stacksafe` 1.x exists but `gpui`
  0.2.2 pins `stacksafe ^0.1`, so cargo cannot migrate.
- Impact: zero. It is a *future*-version diagnostic only — under the
  pinned toolchain (Rust 1.98.1, `rust-toolchain.toml`) everything
  builds, and no quality gate fails because of it. A `[patch]` fork
  would trade permanent maintenance burden for silencing build noise;
  deliberately not done.
- Revisit when: a future `gpui` release migrates to `stacksafe` 1.x
  (which drops the `proc-macro-error2` edge), or the pinned toolchain
  moves to a rustc where E0365 is a hard error.

## Reference provenance
Kero and Zed terminal/terminal-view code are behavioral/architectural references.
Record inspected URLs and revisions with relevant findings at implementation time.
Do not copy GPL implementation without an explicit compatible project-license
decision. GPUI has its own licensing context: inspect its selected package rather
than inferring its license from unrelated Zed crates.

Project license: **MIT OR Apache-2.0** (decided 2026-09-29; `LICENSE-MIT`,
`LICENSE-APACHE`, SPDX `license` fields in every crate manifest,
`license=('MIT' 'Apache-2.0')` in `packaging/arch/PKGBUILD`). No GPL-only
hard dependency exists, so the permissive choice covers the whole tree;
`option-ext` (MPL-2.0, file-level) and `self_cell` (Apache-2.0 disjunct)
remain compatible. This document records dependencies; Kero/Zed stay
behavioral references only.

### UI v5 record — 2026-10-02 (Lucide icon vendoring)

- Lucide `lucide-static` =1.49.0 (pinned; unpkg redirect and npm registry
  agree on the version): 31 SVGs vendored at
  `apps/omaterm/assets/icons/*.svg`, embedded at compile time via
  `include_bytes!` and served through a GPUI `AssetSource`
  (`apps/omaterm/src/ui/assets.rs`, `OmaAssets`). No new Cargo dependency.
- License ISC (per-file `@license lucide-static v1.49.0 - ISC` banner,
  e.g. `x.svg` SHA-256
  `f0c8edc0adc2dc42a45e286fb2342c164c761d52f3c366f20278f4e661a91745`).
  Compatible with the project license; no GPL code involved.
- All icons are 24×24 `fill=none stroke=currentColor stroke-width=2`
  round-cap geometry; native tint flows through the element text color and
  no vendored path was edited. `open-in-new` is not a Lucide name (404 at
  pin time) and maps to `external-link` (documented in the plan/assets).
- GPUI 0.2.2 SVG spike outcome (locked dependency source): `svg().path()`
  + `Styled` sizing + `text_color` tint over a usvg renderer with sprite
  atlas; asset paths resolve through `Application::with_assets`.
- Inter is NOT installed on this machine (UI chrome falls back to system
  sans — separate baseline); JetBrainsMono Nerd Font is installed
  (terminal/code). No font files bundled yet.
