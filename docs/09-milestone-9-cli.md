# Milestone 9 — CLI

> The `omaterm` CLI binary — the user-facing and agent-facing command interface.

## Overview

This is the final milestone for OmaTerm v0.1. The `omaterm` CLI binary connects to the running desktop app via the Unix socket (Milestone 8) and issues semantic commands. When v0.1 is complete, every important workspace operation can be performed from the command line.

This is the foundation for future AI-agent automation.

## Goals

- [ ] `omaterm-cli` crate with `clap` for argument parsing
- [ ] Commands in the coverage table, including `terminal run` and launch behavior
- [ ] Human-readable default output
- [ ] `--json` flag for machine-readable output
- [ ] Connects to running OmaTerm via Unix socket
- [ ] Clear error messages when OmaTerm is not running

## Prerequisites

- Milestone 8 complete (IPC server running)

## Deliverables

### New Crate: `omaterm-cli`

```
crates/omaterm-cli/
├── Cargo.toml
└── src/
    ├── main.rs         # Entry point, clap setup
    ├── commands/
    │   ├── mod.rs
    │   ├── project.rs  # project subcommands
    │   ├── tab.rs      # tab subcommands
    │   ├── pane.rs     # pane subcommands
    │   └── terminal.rs # terminal subcommands
    └── output.rs       # Human vs JSON output formatting
```

### CLI Structure

```
omaterm <subcommand> [options]

Subcommands:
  project     Manage projects
  tab         Manage tabs
  pane        Manage panes
  terminal    Manage terminal sessions

Global Options:
  --json              Output in JSON format
  --socket <path>     Override socket path
  --help              Show help
  --version           Show version
```

### Required Command Coverage

Each row requires a parser test, IPC mapping test, and end-to-end behavior/error
test. This table is the minimum supported M9 surface; examples below use it.

| CLI | Wire method | Domain operation | Result / acceptance assertion |
|---|---|---|---|
| `project list` | `project.list` | Project List | Authorized projects only |
| `project open PATH` | `project.create` | Project Create | New project ID and root; local-user authority required |
| `project select ID` | `project.select` | Project Select | Selection updated within scope |
| `project set-directory ID PATH` | `project.set-directory` | Project SetDirectory | Base directory updated; future tabs use it |
| `project root` | `project.root` | Project Root | Resolved root + source (`pinned`/`git`/`none`); empty state for non-repos |
| `tab list` | `tab.list` | Tab List | Tabs in resolved project |
| `tab new` | `tab.create` | Tab Create | Tab, pane, session IDs |
| `tab close ID` | `tab.close` | Tab Close | Correct cleanup and focus fallback |
| `pane list` | `pane.list` | Pane List | IDs plus project/tab/session and focus |
| `pane split --DIRECTION` | `pane.split` | Pane Split | New pane/session IDs; four directions tested |
| `pane focus ID` | `pane.focus` | Pane Focus | Correct semantic focus |
| `pane close ID` | `pane.close` | Pane Close | Correct cleanup/collapse |
| `pane resize --split ID --fraction F` | `pane.resize` | Pane Resize | Bounded fraction applied |
| `pane equalize --tab ID` | `pane.equalize` | Pane Equalize | Each split reset to 0.5 |
| `terminal list` | `terminal.list` | Terminal List | Authorized session metadata |
| `terminal new` | `terminal.create` | Terminal Create | New tab with one shell in resolved project |
| `terminal send --pane ID TEXT` | `terminal.send` | Terminal SendBytes | Exact UTF-8 bytes, no implicit newline |
| `terminal run --pane ID -- ARGV...` | `terminal.run` | Terminal RunCommand | Shell submission acknowledgement, not exit success |
| `terminal read --pane ID` | `terminal.read` | Terminal ReadVisible | Bounded text plus truncation indicator |
| `history enable` | `history.enable` | History EnablePersistence | Opt in; new archives from here, no backfill |
| `history disable` | `history.disable` | History DisablePersistence | Opt out; deletes all history data, no key rotation |
| `history status` | `history.status` | History Status | Safe metadata only: state, key, archives, warning |
| `history list --pane ID` | `history.list` | History ListJournal | Bounded entries, newest last; never an unbounded dump |
| `history pause --pane ID` | `history.pause` | History PausePane | Capture paused; record kept |
| `history resume --pane ID` | `history.resume` | History ResumePane | Capture resumed |
| `history clear --pane ID` | `history.clear-pane` | History ClearPane | Deletes the pane's archives and journal |
| `history clear --project ID` | `history.clear-project` | History ClearProject | Deletes the project's archives and journals |
| `history clear --all` | `history.clear-all` | History ClearWorkspace | Deletes everything and rotates the encryption key |
| `file list` | `file.list` | File List | Bounded `{entries[{path, kind}], truncated}`; empty envelope without a root |
| `file search QUERY` | `file.search` | File Search | Skim-ranked filename matches (`Ctrl+P` backend), bounded + truncated |
| `file open PATH` | `file.open` | File Open | Submits `$EDITOR <path>` to the focused terminal (`terminal.run` semantics) |
| `git status` | `git.status` | Git Status | Branch + ahead/behind + staged/unstaged/untracked groups, bounded + truncated; empty envelope for non-repos |
| `git log [--limit N] [--all-local]` | `git.history` | Git History | First bounded immutable graph page (1–100); current HEAD by default, local branch tips with `--all-local`; UI graph/pagination follow the history plan |
| `git commit-files COMMIT [--parent OID|empty_tree]` | `git.commit-files` | Git CommitFiles | Parent-specific changed files with kind badges and rename pairs; read-only expansion surface |
| `git branch-list` | `git.branch-list` | Git BranchList | Local branches with HEAD identity and upstream tracking; empty envelope for non-repos |
| `git branch-create NAME [--start REV]` | `git.branch-create` | Git BranchCreate | Create a local branch without checkout (start defaults to HEAD, verified via rev-parse) |
| `git branch-checkout NAME` | `git.branch-checkout` | Git BranchCheckout | Check out a local branch; refused with `dirty_worktree` on staged/unstaged changes |
| `git branch-delete NAME [--force]` | `git.branch-delete` | Git BranchDelete | Delete a local branch; head refused (`current_branch`), unmerged needs `--force` |
| `git branch-rename OLD NEW` | `git.branch-rename` | Git BranchRename | Rename a branch, including the checked-out one |
| `diff show-commit COMMIT --path PATH [--old-path PATH] [--parent OID|empty_tree]` | `diff.show-commit` | Diff ShowCommit | One committed path pair against a chosen base; strictly read-only historical preview |
| `git stage PATHS...` | `git.stage` | Git Stage | Stages 1–100 explicit root-relative paths (`git add --`) |
| `git unstage PATHS...` | `git.unstage` | Git Unstage | Restores the index for 1–100 paths, worktree kept |
| `git discard PATHS...` | `git.discard` | Git Discard | Restores tracked paths from HEAD, deletes untracked (desktop arms two-step; CLI dispatches directly) |
| `git commit -m MESSAGE` | `git.commit` | Git Commit | Commits staged changes with a message (author from repo config); nothing staged fails with `git_failed` |
| `diff show` | `diff.show` | Diff Show | Bounded unified diff for unstaged/staged changes or one selected path |
| `git stage-hunk` | `git.stage-hunk` | Git StageHunk | Stage one current unstaged hunk selected by path and diff.show hunk ID |
| `diff list-files` | `diff.list-files` | Diff ListFiles | Bounded changed-file headers and hunk counts without bodies |
| `process list` | `process.list` | Process List | Bounded live descendants belonging to terminal sessions in the selected project |
| `process kill PID` | `process.kill` | Process Kill | Scoped `SIGTERM` to a project-owned descendant; foreign PIDs denied (`cross_project_denied`), gone PIDs fail with `process_not_found` |

`terminal new` deliberately creates a new tab, avoiding an unspecified split target.
`project open` creates a project even if another project uses the same directory.
Rename/delete additions are optional until included in this table and M8 mapping.
`project set-directory` is now included: it changes the project's base
directory for future tabs while live sessions keep their CWD.

#### Project Commands

```bash
# List all projects
omaterm project list
omaterm project list --json

# Open/create a project
omaterm project open ~/Code/myapp
omaterm project open ~/Code/myapp --name "My App"

# Select a project
omaterm project select <project-id>
```

#### Tab Commands

```bash
# List tabs in current/specified project
omaterm tab list
omaterm tab list --project <project-id>

# Create a new tab
omaterm tab new
omaterm tab new --name "Tests"

# Close a tab
omaterm tab close <tab-id>
```

#### Pane Commands

```bash
# List panes in current tab
omaterm pane list
omaterm pane list --json

# Split the focused pane
omaterm pane split --right
omaterm pane split --down
omaterm pane split --left
omaterm pane split --up

# Split a specific pane
omaterm pane split --right --target <pane-id>

# Focus a pane
omaterm pane focus <pane-id>

# Close a pane
omaterm pane close <pane-id>
```

#### Terminal Commands

```bash
# List terminal sessions
omaterm terminal list
omaterm terminal list --json

# Send text/command to a terminal
omaterm terminal send --pane <pane-id> "cargo test"

# Submit argv to a ready shell (see execution contract below)
omaterm terminal run --pane <pane-id> -- cargo test

# Read terminal output
omaterm terminal read --pane <pane-id> --lines 50
omaterm terminal read --pane <pane-id> --lines 100 --columns 500
omaterm terminal read --pane <pane-id> --json

# Create a new terminal (in a new tab)
omaterm terminal new
```

### Output Formats

**Human-readable (default):**

```
$ omaterm pane list
  ID        TYPE       SESSION    FOCUSED
  abc123    terminal   sess-001   *
  def456    terminal   sess-002
```

**JSON (`--json`):**

```json
{
  "ok": true,
  "result": {
    "panes": [
      {
        "id": "abc123",
        "project_id": "project-001",
        "tab_id": "tab-001",
        "kind": "terminal",
        "session_id": "sess-001",
        "focused": true
      },
      {
        "id": "def456",
        "project_id": "project-001",
        "tab_id": "tab-001",
        "kind": "terminal",
        "session_id": "sess-002",
        "focused": false
      }
    ],
    "truncated": false
  }
}
```

### Connection Logic

Resolve socket: explicit `--socket`, then `OMATERM_SOCKET`, then the validated M8
default. Use `OMATERM_TOKEN` for in-app callers; outside callers read the matching
owner-only local-user credential. Never send a default instance credential to an
unrelated override socket. Missing/stale in-app credentials fail without privilege
fallback. Connect directly and classify OS errors; a path-existence check is not
proof that an instance is running.

```rust
fn connect() -> Result<IpcClient> {
    let socket = socket_path(); // $XDG_RUNTIME_DIR/omaterm.sock

    IpcClient::connect(&socket)
}
```

## Architecture Notes

### CLI Is a Thin Client

The CLI does NO workspace logic. It:
1. Parses arguments (clap)
2. Builds an `IpcRequest`
3. Sends it to the socket
4. Receives `IpcResponse`
5. Formats output (human or JSON)

```
omaterm pane split --right
  ↓ clap parse
IpcRequest { method: "pane.split", params: { direction: "right" } }
  ↓ Unix socket
CommandRouter.dispatch(PaneCommand::Split { ... })
  ↓
IpcResponse { ok: true, result: { new_pane_id: "..." } }
  ↓
Printed to stdout
```

### Error Handling

`--json` produces a stable success/error envelope for local parsing errors as well
as server errors. Keep diagnostics on stderr and machine output on stdout. Avoid
colors in JSON; include stable error codes. Exit status 2 is reserved for connection
failure; normalize clap usage errors to 64, command/authorization errors to 1, and
success to 0. Tests cover errors and global `--json` in either argument position.

```bash
$ omaterm pane split --right
# When OmaTerm is not running:
Error: OmaTerm is not running. Start it first.

# When pane not found:
Error: Pane 'xyz' not found.

# Exit codes:
# 0 = success
# 1 = command error (pane not found, etc.)
# 2 = connection error (OmaTerm not running)
```

### CLI Binary Name

Use two binaries: `omaterm` is the thin CLI/launcher; `omaterm-desktop` owns GPUI.
M1 starts with the desktop binary; M9 adds the public CLI without a name collision.

```bash
omaterm              # No args → launch desktop app, or acknowledge existing instance
omaterm pane list    # Subcommand → CLI mode via IPC
omaterm .            # Open current directory as a project (implies launch)
omaterm ~/Code/foo   # Open a project rooted there (implies launch)
omaterm ~/Code/foo -- cargo test  # Open, then submit argv to the new shell
```

Find the desktop binary alongside the installed CLI. No-argument launch starts it
when absent, waits for bounded readiness, and returns a useful launch error on
failure. An existing instance is left running; compositor focus is deferred.
Mutating/query subcommands require an existing instance and do not auto-launch.
Test both binaries together and document the development/install invocation.

Path launches (M11) share the same readiness wait, then issue `project.create`
for an existing directory and, when `-- <argv>` is present, one `terminal.run`
against the returned pane. A path never combines with a subcommand (exit 64).
Mid-sequence failures report honestly with no automatic replay or rollback.

### Selection and Execution Contracts

- Resolve explicit IDs first, then originating `OMATERM_*` context, then authorized
  selected project/tab/focused pane. Server-side scope checks remain authoritative;
  environment hints never grant access. An invalid explicit ID never falls back.
- `send` sends literal UTF-8 bytes, without newline. Its protocol encoding is base64.
- `run` sends structured argv, not an interpolated shell command string. The server
  uses a tested encoder for the detected shell dialect; reject unsupported shells
  rather than concatenate with spaces. Test spaces, quotes, empty args, Unicode,
  and shell metacharacters across bash/zsh/fish.
- `run` is shell input submission, not a process execution/completion API. Never
  submit to a known foreground TUI/job. Implement readiness detection (for example
  explicit prompt/command lifecycle shell integration) before enabling it; unknown
  readiness returns `shell_busy` or `unsupported_operation`. Process foreground
  identity alone does not prove a clean prompt. A fresh supported-shell prompt must
  allow the proof flow; record any integration requirement in user documentation.
- M7 owns readiness/encoding behavior; M8 transports argv and returns submission
  status. No text scraping of “Done”, exit-code promises, or automatic retries.
- `read` defaults to 50 visible lines and 500 columns, subject to M8 caps. It reads
  the current viewport, not a command transcript; return dimensions/truncation.

## Implementation Steps

1. **Create `omaterm-cli` crate** with clap dependency
2. **Define CLI structure** with clap derive macros
3. **Implement `connect()`** — find socket, connect via `omaterm-ipc` client
4. **Implement `project list`** command
5. **Implement `pane list`** command
6. **Implement `pane split`** command
7. **Implement `terminal send`** command
8. **Implement `terminal read`** command
9. **Implement `--json` output formatting**
10. **Implement error handling** with proper exit codes
11. **Test end-to-end**: launch OmaTerm desktop → run CLI commands → verify effects
12. **Test error cases**: OmaTerm not running, invalid pane ID, etc.
13. **Implement all remaining coverage-table rows**, especially `terminal run`
14. **Implement the two-binary launcher contract** and test absent/running desktop
15. **Run the final proof from an authorized shell**, checking new IDs and bounded output

## Acceptance Criteria

- [ ] `omaterm project list` returns projects via IPC
- [ ] `omaterm pane list` returns panes via IPC
- [ ] `omaterm pane split --right` creates a real split in the desktop app
- [ ] `omaterm terminal send --pane <id> "ls"` sends text to the terminal
- [ ] `omaterm terminal read --pane <id> --lines 50` returns terminal output
- [ ] `--json` flag produces valid JSON output
- [ ] Clear error when OmaTerm is not running (exit code 2)
- [ ] Clear error when pane/project not found (exit code 1)
- [ ] Exit code 0 on success
- [ ] `cargo build -p omaterm-cli` produces the `omaterm` binary
- [ ] Every coverage-table row has mapping and end-to-end success/failure tests
- [ ] `terminal run` passes quoting/readiness tests and the final proof flow
- [ ] No-argument `omaterm` launches the desktop or acknowledges an existing instance
- [ ] Scoped context, denied targets, JSON errors, truncation, and exit codes tested

## The v0.1 Proof

With this milestone complete, the following flow should work:

```bash
# 1. Start OmaTerm
omaterm

# 2. List panes (from another terminal)
omaterm pane list

# 3. Split pane via semantic command
omaterm pane split --right

# 4. Run command in new pane
omaterm terminal run --pane <new-pane> -- cargo test

# 5. Read output
omaterm terminal read --pane <new-pane> --lines 50
```

This proves the architecture for future AI-agent control. 🎉

## Non-Goals

- No `omaterm agent` commands (v0.3+)
- No `terminal wait` command (future)
- No shell completions (future)
- No man pages (future)
- No compositor focus/activation request (basic launch/instance detection is required)

## References

- Blueprint §16 — CLI Design
- Blueprint §17 — The CLI Must Not Automate the UI
- Blueprint §43 — Output Format for CLI
- Blueprint §60 — CLI Launch Behavior
- Blueprint §68, Slice 9 — CLI
- Blueprint §78 — First Implementation Objective
