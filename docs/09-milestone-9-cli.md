# Milestone 9 — CLI

> The `omaterm` CLI binary — the user-facing and agent-facing command interface.

## Overview

This is the final milestone for OmaTerm v0.1. The `omaterm` CLI binary connects to the running desktop app via the Unix socket (Milestone 8) and issues semantic commands. When v0.1 is complete, every important workspace operation can be performed from the command line.

This is the foundation for future AI-agent automation.

## Goals

- [ ] `omaterm-cli` crate with `clap` for argument parsing
- [ ] Core commands: `project list`, `pane list`, `pane split`, `terminal send`, `terminal read`
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

### Commands

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

# Run a command in a pane (send + newline)
omaterm terminal run --pane <pane-id> -- cargo test

# Read terminal output
omaterm terminal read --pane <pane-id> --lines 50
omaterm terminal read --pane <pane-id> --lines 100 --columns 500
omaterm terminal read --pane <pane-id> --json

# Create a new terminal (in new pane)
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
  "panes": [
    {
      "id": "abc123",
      "kind": "terminal",
      "session_id": "sess-001",
      "focused": true
    },
    {
      "id": "def456",
      "kind": "terminal",
      "session_id": "sess-002",
      "focused": false
    }
  ]
}
```

### Connection Logic

```rust
fn connect() -> Result<IpcClient> {
    let socket = socket_path(); // $XDG_RUNTIME_DIR/omaterm.sock

    if !socket.exists() {
        return Err(anyhow!("OmaTerm is not running. Start it first."));
    }

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

The CLI and the desktop app MAY share the same binary:

```bash
omaterm              # No args → launch desktop app (or focus if running)
omaterm pane list    # Subcommand → CLI mode via IPC
```

Alternatively, separate binaries. Decide during implementation.

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
- No launch/focus detection (future)

## References

- Blueprint §16 — CLI Design
- Blueprint §17 — The CLI Must Not Automate the UI
- Blueprint §43 — Output Format for CLI
- Blueprint §60 — CLI Launch Behavior
- Blueprint §68, Slice 9 — CLI
- Blueprint §78 — First Implementation Objective
