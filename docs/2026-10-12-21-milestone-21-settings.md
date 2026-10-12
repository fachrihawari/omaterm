# Milestone 21 — Settings App (About / Update / Configuration / License)

> One Settings overlay reachable from the command palette (`Preferences:
> Open Settings`), the `Ctrl+,` shortcut (`settings.open`), and a gear
> button below `Open Folder…` in the Projects sidebar footer. Four
> sections: About, Update, Configuration, License. Configuration is fully
> editable inline with write-back to `config.toml`; Update checks the
> stable GitHub channel with an opt-out startup check and applies via a
> verified atomic replace with privilege elevation when needed; every
> behavior is reachable through the semantic command bus with CLI/IPC
> parity.

**Status:** S1, S2 and S3a complete (uncommitted); S3b planned below —
see [status.md](status.md) for the live verification record.

## Agreed Decisions (locked 2026-10-12)

- **Entry: palette + footer gear + `Ctrl+,`.** One `open_settings()`
  handler serves all three; the palette entry is a `ViewAction` (like the
  theme rows), the shortcut id is `settings.open`, the footer gains a
  second row below `Open Folder…` (footer becomes two-row `flex_col`).
- **Update channel: stable only.** Tag must parse as semver `vX.Y.Z`;
  prereleases and drafts are ignored. No channel toggle in v1.
- **Elevation, not fallback-only.** Atomic replace runs as the user; on
  `EACCES` (e.g. `/usr/local/bin` owned by root) a minimal `pkexec`
  replace helper performs the rename. No silent sudo, no stored
  passwords; clean `permission_denied` when no polkit agent answers.
- **Startup update check: on with opt-out.** New `[updates]` section,
  key `check-on-startup: bool`, default on; `false` disables the
  at-startup check (manual `Check for updates` always available).
- **Config: full editable.** Every key in the table below is settable
  inline, via `omaterm config set`, and via `config.set` IPC — one
  validation path, reject-before-disk, comments preserved.
- **S3b venue: disposable fixture/VM only**, never the dev host.

## Recorded deviations (maintainer approval owed; blueprint text untouched)

- Blueprint §61 specifies file-only configuration — M21 adds inline
  editing that writes back through the same validated `toml_edit` path.
- Blueprint §59 specifies Arch/tarball packaging with no updater — M21
  adds a stable-channel self-updater gated by `pacman -Qo` (managed
  installs are blocked with `sudo pacman -Syu` guidance, never touched).
- Blueprint §70 covers license inventory only — M21 adds an offline
  License tab embedding `LICENSE-MIT` + `LICENSE-APACHE` via
  `include_str!`.

## Goals

- [x] S1 shell: overlay + 4-section nav + `InputOwner::Settings` +
  palette entry + `settings.open` (`Ctrl+,`, parity in
  [shortcuts.md](shortcuts.md)) + footer gear + About + License tabs.
- [x] S2 config editable: `ConfigCommand::{Get, Set, Reset}` +
  generalized `set_config_key`/`reset_config_key` + `config.get/set/reset`
  wire methods + `omaterm config` CLI + inline controls (click-to-cycle
  theme/bools, `−/+` steppers for scalars) dispatching through the shared
  command bus; live-apply for theme/translucency, reject-before-disk with
  `toml_edit` comment preservation, new `ConfigUnavailable` error code.
- [x] S3a update check: `AppCommand::{About, UpdateCheck}` +
  `app.about` / `app.update-check` + `releases/latest` redirect
  discovery (semver-stable-only filter, bounded timeouts, state codes
  `up_to_date`/`update_available`/`offline`/`rate_limited`/
  `unavailable`) + startup check default-on honoring
  `[updates] check-on-startup` + Settings Update tab (`Check now`) +
  `omaterm about` / `omaterm update check` CLI parity.
- [x] S3b update apply: `AppCommand::UpdateApply` /
  `app.update-apply` + `omaterm update apply [--confirm-downgrade]` +
  Settings `Update now` (GPUI prompt confirm) + pipeline
  `pacman -Qo` gate (managed → `managed_by_package_manager` +
  `pacman -Syu` guidance, no bytes moved) → bounded download →
  sha256 verify (refuse before any exec-bit) → stage + `--version`
  confirm → atomic rename; `EACCES` escalates one rename through
  `pkexec` with a data-only helper (`update::maybe_run_replace_helper`
  in `main`, never the whole app as root); downgrades refused unless
  `confirm_downgrade`. Pure `apply()` sequence is unit-tested with a
  scripted `ApplyHost`; the live host sits behind the same trait.
- [x] Native-line validation gates: desktop tests, protocol/state/CLI
  suites, docs parity, clippy/fmt/diff hygiene all recorded below.
- [ ] Manual Wayland validation per slice (overlay entries, config
  edits, update check/apply on a real machine) — still owed.

## S2 key table (whitelist, includes the S3a opt-out key from day one)

| Key | Type / bounds | Live-apply |
|---|---|---|
| `appearance.theme` | `system`/`dark`/`light` | yes (existing preference path) |
| `appearance.translucent-sidebar` | bool | yes |
| `terminal.font-family` | non-empty string | yes |
| `terminal.font-size` | 6–72 | yes; invalid rejected + toast, defaults hold (§61/M11 contract) |
| `terminal.scrollback-lines` | ≤100000 | new panes (existing contract) |
| `terminal.shell` | `powershell`/`cmd`/`git-bash`, Windows-only | yes (existing picker path) |
| `history.enabled` | bool | existing two-step opt-in/out (`router.rs` `Enable`/`DisablePersistence`) |
| `files.max-results` | 1–5000 | yes |
| `files.show-hidden` | bool | yes, else restart banner |
| `git.refresh-secs` | 1–300 | yes (live poller) |
| `automation.enabled` | bool | recorded; IPC stays on in v0.1 (no false claim) |
| `updates.check-on-startup` | bool, default on | yes (next startup) |

## S3 pipeline (both phases)

1. Discovery: `GET …/releases/latest` redirect → tag `vX.Y.Z` →
   `…/releases/download/vX.Y.Z/omaterm-X.Y.Z-x86_64.tar.gz` + `.sha256`
   (mirrors `install.sh`). Fallback: Releases API `releases/latest`
   with `prerelease == false && draft == false`, cached/ETag, bounded
   timeouts.
2. Preflight: `pacman -Qo <exe>` success → managed install → Apply
   disabled + `sudo pacman -Syu` text. Target ≤ installed → explicit
   downgrade confirm.
3. Apply: bounded download → `^[0-9a-f]{64}$` + compare (abort =
   `checksum_mismatch`, no exec-bit before pass) → extract → stage +
   `chmod +x` → atomic rename (user, else minimal `pkexec` helper) →
   `<new> --version` match → snapshot save → re-exec restart.
4. Error codes (stable, never text-matched): `invalid_request`,
   `update_unavailable`, `checksum_mismatch`, `permission_denied`,
   `managed_by_package_manager`, `restart_required`.

## Non-goals (v1)

Separate settings window, in-app full-text `config.toml` editor,
beta/nightly channels, delta updates, silent auto-apply, new signature
infra (v1 trusts TLS + the published sha256 sidecar, the release status
quo), Windows/macOS updater artifacts, account/cloud/settings sync
(blueprint §46).

## Verification (per slice)

`cargo fmt --all --check`, `cargo test --workspace`,
`cargo clippy --workspace --all-targets -- -D warnings`,
`python3 scripts/check-docs.py`, `git diff --check`, plus manual
Wayland evidence per slice (or explicitly recorded as owed). Known
pre-existing failures at S1 baseline: 8 `omaterm-cli` wire-mapping
tests and `git::tests::timeout_reaps_descendants_holding_pipes`, all
proven failing on the stashed clean tree.
