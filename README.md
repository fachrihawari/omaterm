# OmaTerm

A Linux-first, terminal-first developer workspace built with Rust and GPUI.
Projects, tabs, recursive split panes, and long-lived terminal sessions share a
semantic command layer designed for CLI control and future agent automation.

## Current status

The Cargo workspace and terminal workspace implementation exist. Milestones 1–14
are complete with recorded limits; M15 (diff), M16 (palette), M18 (process panel),
and M19 (basic editor) remain in progress. Read [status](docs/status.md) for the
current source-specific evidence and open acceptance gates.

## Start here

- [Agent instructions](AGENTS.md): development rules and workflow.
- [Architecture blueprint](OMATERM_AGENT_BLUEPRINT.md): authoritative decisions.
- [Implementation milestones](docs/00-overview.md): milestone index and sequencing.
- [Project status](docs/status.md): progress, evidence, blockers, and next actions.
- [Acceptance matrix](docs/acceptance-matrix.md): v0.1 release verification.
- [Dependency inventory](docs/dependencies.md): selected versions and licenses.

## Development environment

The target is Linux, with Omarchy/Wayland first and X11 build support.
`rust-toolchain.toml` and CI pin Rust 1.98.1 with `rustfmt` and `clippy`.
Dependency versions, licenses, native packages, and any explicitly recorded local
toolchain override are tracked in [dependencies](docs/dependencies.md).

```bash
cargo fmt --all --check
cargo test --workspace
cargo clippy --workspace --all-targets -- -D warnings
cargo build --release --bin omaterm --bin omaterm-desktop
```

Run `target/release/omaterm-desktop` from an Omarchy/Wayland session. The
`omaterm` CLI connects to the running desktop through its owner-validated Unix
socket; see [M9](docs/09-milestone-9-cli.md) for supported commands and launch
behavior.

`.codegraph` is an optional, ignored local index, not a build dependency. Generated
indexes may reference absolute machine-local paths.

## License

MIT OR Apache-2.0 — see `LICENSE-MIT` and `LICENSE-APACHE`. Kero and Zed
remain behavioral/architectural references only; no GPL implementation code
is incorporated. Outside contributions are accepted under the same dual terms.
