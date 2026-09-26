# Toolchain and Dependency Inventory

## Current verification state

No Cargo workspace exists. No Rust version, GPUI revision, native dependency set,
or MSRV has been validated for this project. `rust-toolchain.toml` requests `stable`
with `rustfmt`/`clippy` as bootstrap configuration only.

| Component | Selection | License evidence | Verification |
|---|---|---|---|
| Rust toolchain | Pending M1; pin after successful build | Official distribution | Not verified |
| GPUI/platform crates | Pending M1, exact release or Git revision | Selected revision/package | Not verified |
| Native Linux packages | Pending M1, record versions and purpose | Package metadata | Not verified |
| Core ID/error dependencies | Pending M2 | Selected package metadata/license files | Not verified |
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

## Reference provenance

Kero and Zed terminal/terminal-view code are behavioral/architectural references.
Record inspected URLs and revisions with relevant findings at implementation time.
Do not copy GPL implementation without an explicit compatible project-license
decision. GPUI has its own licensing context: inspect its selected package rather
than inferring its license from unrelated Zed crates.

Project license: **undecided**. This document records dependencies; it does not
choose the application's license.
