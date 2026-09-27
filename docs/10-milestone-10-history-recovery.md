# Milestone 10 — Encrypted History Recovery

> Optional, encrypted restoration of terminal scrollback and an OmaTerm command journal, while always launching fresh shells.

## Product Contract

M10 is a post-v0.1 milestone. It may start only after M5–M9 are complete and
verified. It does not change M6's default behavior or acceptance criteria.

The user requested both scrollback and command history, explicitly opt-in
persistence, an OmaTerm-owned command journal, and encrypted-at-rest archives.
This is an approved post-v0.1 extension to blueprint §29 and M6's explicit
scrollback non-goal. The deviation is limited to M10; M6 remains layout/CWD
persistence with fresh shells.

On restart, restore the saved visual terminal history, then launch a fresh shell
in the saved directory. Never restore a PTY, process, shell parser state, current
command, foreground job, or alternate-screen TUI state. The command journal is
OmaTerm-owned and does not read, write, or replay shell history files.

## Goals

- [ ] Persistence is disabled by default and requires an explicit user opt-in.
- [ ] Persist bounded scrollback in separately encrypted per-pane archives.
- [ ] Restore scrollback before fresh shell output is attached to the pane.
- [ ] Maintain an encrypted OmaTerm command journal from supported shell
  lifecycle events, separate from shell history files.
- [ ] Provide disable, pause, list, and clear controls through semantic commands.
- [ ] Use Linux OS-backed key storage; never fall back to plaintext if unavailable.
- [ ] Keep archive corruption, key loss, or locked keyring isolated from workspace
  snapshot recovery and shell startup.
- [ ] Bound memory, archive, frame, and total-workspace history sizes.

## Prerequisites

- M5 through M9 complete, including M7 shell lifecycle/readiness work.
- M6 workspace snapshot/recovery complete and unchanged in its guarantees.
- M7 semantic command router and M8 authorization/IPC provide a single command
  path for settings, journal queries, and clear operations.
- M9 CLI available for history administration.
- A dependency spike verifies Secret Service behavior, cryptographic APIs,
  licenses, packaging/runtime availability, and user-session failure behavior on
  the supported Omarchy environment before dependency selection.

## Deliverables

### Configuration and Key Provider

- Add explicit persisted history configuration, disabled by default, with a
  global opt-in plus per-pane pause/exclusion.
- Add a key-provider abstraction in the persistence layer. Production Linux
  implementation stores only a randomly generated encryption master key in the
  user's OS Secret Service/keyring. Tests use an injected in-memory provider.
- Derive versioned archive keys from the master key with a standard KDF and
  per-archive random salt. Rotate the master key when clearing all history.
- If the keyring is absent, locked, or denies access: preserve in-memory terminal
  use, do not read/write plaintext, do not overwrite prior encrypted archives,
  and show a recoverable warning.
- Record exact dependency versions, source/license evidence, security API
  verification, and platform requirements in `docs/dependencies.md`.

### Scrollback Event Archive

- Capture the ordered PTY output byte chunks and terminal resize events required
  to deterministically rebuild the main-screen Alacritty history. Record only
  after opt-in is enabled; enabling begins a new archive and does not backfill
  earlier output.
- Preserve event order and grid dimensions. Use a versioned framed archive with
  strict per-frame length limits, authenticated metadata, and an authenticated
  encryption mode from a reviewed crate.
- Compress the bounded stream before encryption. Never include keys or plaintext
  in logs, errors, crash messages, or workspace JSON.
- Restore by replaying verified frames into a fresh terminal engine before
  starting/attaching the new PTY shell. A failed archive is quarantined/ignored;
  the pane and shell still start normally with empty history.
- Do not restore alternate-screen content or active VT modes. Preserve the last
  known main-screen scrollback or stop capture while an alternate screen is
  active; prove the chosen behavior with fixtures before finalizing it.
- Use a background, revision-ordered history writer. Terminal output delivery
  must not wait for compression, encryption, keyring calls, or disk I/O.

Initial ceilings, subject to tests and resource measurements:

| Limit | Initial value |
|---|---:|
| Retained scrollback | 10,000 logical lines per pane |
| Encrypted archive | 8 MiB per pane |
| Combined persisted history | 64 MiB per workspace |
| Journal entries | 10,000 per pane; workspace limit applies |
| Decompressed frame | 1 MiB |

Limits must be enforced before allocation/decompression, not after decoding.
Retention drops oldest complete logical records/lines; it must never leave a
partially authenticated or undecodable archive.

### OmaTerm Command Journal

- Record only structured lifecycle events emitted by a supported shell
  integration. Never infer command boundaries or readiness by scraping terminal
  text or guessing from process identity.
- Journal command text, timestamp, shell dialect, working directory, project/tab/
  pane IDs, and lifecycle result when authoritatively reported. Do not promise
  completion or exit status when the shell integration cannot provide it.
- Do not capture arbitrary keypresses, password prompts, terminal TUI input,
  unsupported-shell input, or commands from before opt-in.
- Shell integration must use an owner-only session channel and authenticated
  session association. Do not expose credentials in terminal logs, workspace
  snapshots, or command output.
- Start with one shell dialect only after a dedicated hook reliability spike.
  Add Bash/zsh/fish independently after testing interactive shells, nested shells,
  multiline commands, prompts, Ctrl+C, TUI foreground jobs, and shell startup
  files. Unsupported shells continue normally without journal capture.
- Keep the journal as a separate logical record set from terminal scrollback.
  `history list` returns bounded entries and metadata, not an unbounded dump.

## Storage Layout And Lifecycle

Use a dedicated directory under `$XDG_STATE_HOME/omaterm/history/` (with the
standard `$HOME/.local/state/omaterm/history/` fallback):

```text
history/
  key metadata is held by Secret Service, not in this directory
  <opaque-pane-id>.<revision>.omhist
```

Archive names must not disclose command text, paths, or project names. Store a
version, opaque pane identity, salt/nonce, bounded ciphertext, and authenticated
metadata. Do not place history in `workspace-v1.json` or its recovery files.

- Use `0700` directories and `0600` files; reject symlinks and unexpected
  ownership/type. Atomic replacement uses same-directory temporary files.
- A close/disable/clear operation deletes relevant archives and journal data.
  Do not claim physical secure erasure on SSDs or copy-on-write filesystems.
- Clear-all rotates/removes the key as well as archives so undeleted filesystem
  blocks cannot be decrypted with the prior key.
- A corrupt/tampered archive is retained for diagnosis unless the user clears it;
  it is never overwritten as if it had been valid.
- Key loss must not block workspace restore, terminal spawn, or final shutdown.
- Per-pane archive cleanup follows pane/tab/project deletion, including hidden
  projects and tabs.

## User Controls And Commands

Add semantic `HistoryCommand` operations through the common dispatcher:

- `EnablePersistence` / `DisablePersistence`
- `PausePane` / `ResumePane`
- `ListJournal { pane, limit }`
- `ClearPane`, `ClearProject`, `ClearWorkspace`
- `Status` for enabled state, key availability, archive count/size, and warnings

The M10 CLI should include:

```text
omaterm history enable
omaterm history disable
omaterm history status
omaterm history list --pane <id> [--limit N]
omaterm history pause --pane <id>
omaterm history resume --pane <id>
omaterm history clear --pane <id>
omaterm history clear --project <id>
omaterm history clear --all
```

Desktop controls must state what is stored and that terminal output can contain
secrets. Enabling is an explicit opt-in; destructive clear operations receive a
clear result and remove both journal and scrollback for the selected scope.
Configuration changes themselves are persisted separately from workspace
snapshots and use the existing config path contract.

## Implementation Phases

1. Approve the M6/blueprint deviation and finish all M5–M9 prerequisites.
2. Research Secret Service and cryptographic dependencies; test on Omarchy with
   unlocked, locked, absent, and denied keyring cases. Record exact licenses and
   APIs before adding dependencies.
3. Prototype terminal event recording/replay against the pinned
   `alacritty_terminal` version, including resize and alternate-screen behavior.
   Do not proceed to durable storage until visual history survives round-trip.
4. Implement config, key provider, encrypted archive framing, limits, atomic
   writes, recovery isolation, and memory-only behavior on key-provider failure.
5. Implement renderer-independent scrollback replay and hook it into startup
   before fresh shell output; maintain fresh process/session IDs.
6. Implement supported-shell lifecycle reporting and the independent command
   journal; expose it only for shells with authoritative integration.
7. Add router/IPC/CLI commands, desktop opt-in/status/clear controls, and bounded
   user-visible warnings.
8. Complete automated, security-boundary, resource, restart, and Wayland checks;
   record evidence in status and acceptance documents.

## Test Plan

### Archive And Key Tests

- Encrypt/decrypt round trip; wrong key; modified ciphertext, metadata, nonce,
  truncation, duplicate/reordered frames, and unsupported archive version.
- Keyring available/unlocked, locked, unavailable, permission denied, key lost,
  and key rotation; all failure cases avoid plaintext and do not block shells.
- File/directory permissions, symlink rejection, owner/type checks, atomic failure
  injection, stale writer revisions, archive quarantine, clear, and cleanup.
- Exact size boundaries, decompression bombs, aggregate workspace quota, and
  safe handling of multibyte command text.

### Terminal Replay Tests

- ANSI attributes/colors, wide and combining Unicode, soft wraps, line feeds,
  alternate-screen transitions, and resize events across archive replay.
- Flood beyond per-pane and workspace limits; retained archive remains valid and
  oldest complete history is dropped.
- Restore saved scrollback, then prove a new PID/shell is launched and accepts
  input. The previous process, foreground job, parser modes, and alternate screen
  are not restored.
- Simulate archive corruption/key failure; normal layout and fresh shell still
  restore with a visible history warning.

### Journal And UI Tests

- Supported shell command start/end hooks, multiline commands, nested shells,
  Ctrl+C, TUI foreground jobs, shell exit, and fresh prompts.
- Verify password prompts, raw key events, unsupported shells, pre-opt-in output,
  and unstructured terminal traffic do not enter the journal.
- Verify opt-in defaults off, pane pause/resume, bounded list output, each clear
  scope, deletion cleanup, restart behavior, and no journal recovery through shell
  dotfiles.
- Wayland: opt-in warning/control, history restore appearance, fresh shell input,
  clear/status/pause controls, and keyring unavailable behavior.

## Acceptance Criteria

- [ ] M5–M9 prerequisites are complete and M10 deviation is recorded.
- [ ] History persistence is off by default and enabled only by explicit opt-in.
- [ ] No plaintext history fallback exists on any keyring failure path.
- [ ] Scrollback restoration is bounded, encrypted, integrity-checked, and visually
  verified after restart.
- [ ] Fresh shells start in saved CWDs with new PIDs; no process/TUI state resumes.
- [ ] Command journal contains only authoritative supported-shell lifecycle events
  and does not modify shell history files.
- [ ] Disable/pause/list/clear/status work through semantic dispatch and CLI.
- [ ] Tampered, missing, locked-key, and corrupt archives do not prevent workspace
  recovery, fresh shell startup, or orderly shutdown.
- [ ] Per-pane/workspace storage, decompression, and response limits are enforced.
- [ ] Cargo quality gates, dependency/license review, and actual Wayland checks pass.
- [ ] Acceptance evidence and limitations are recorded in `docs/status.md`.

## Non-Goals

- Restoring processes, PTYs, foreground jobs, current commands, shell parser state,
  or alternate-screen applications.
- Reading, injecting, syncing, or modifying shell-native history files.
- Cloud synchronization, cross-device history, or sharing between OS accounts.
- Capturing arbitrary terminal input or inferring command state from rendered text.
- Claiming secure physical deletion on storage media.

## References

- Blueprint §6.6 — Persist Layout, Not Running Processes
- Blueprint §29 — Terminal Scrollback
- Blueprint §30 — Persistence
- M6 — Workspace Persistence (remains unchanged)
- M7 — Command Router
- M8 — IPC
- M9 — CLI
