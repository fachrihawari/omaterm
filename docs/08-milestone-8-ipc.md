# Milestone 8 — IPC

> Unix domain socket server in the desktop process. JSON wire protocol v1. The bridge between OmaTerm's in-process commands and external callers.

## Overview

This milestone adds a Unix domain socket server to OmaTerm's desktop process. External programs (the CLI, future AI agents) connect to this socket and send JSON-encoded command requests. The server deserializes requests, dispatches them through the `CommandRouter`, and returns JSON responses.

## Goals

- [ ] `omaterm-protocol` crate — wire types (request, response, error)
- [ ] `omaterm-ipc` crate — Unix socket server and client
- [ ] Server starts automatically with the desktop app
- [ ] JSON protocol v1 with request/response semantics
- [ ] Project-scoped session capabilities and explicit local-user credentials
- [ ] Clean shutdown (stop accepting, close socket)

## Prerequisites

- Milestone 7 complete (command router)

## Deliverables

### New Crate: `omaterm-protocol`

```
crates/omaterm-protocol/
├── Cargo.toml
└── src/
    ├── lib.rs
    ├── request.rs      # IpcRequest
    ├── response.rs     # IpcResponse
    ├── capability.rs   # Token types
    └── version.rs      # Protocol versioning
```

### New Crate: `omaterm-ipc`

```
crates/omaterm-ipc/
├── Cargo.toml
└── src/
    ├── lib.rs
    ├── server.rs       # Unix socket server
    └── client.rs       # Client (used by CLI)
```

### Wire Protocol

**Request:**

```json
{
  "version": 1,
  "request_id": "01924a3e-...",
  "method": "pane.split",
  "params": {
    "pane_id": "abc123",
    "direction": "right"
  },
  "token": "..."
}
```

**Success Response:**

```json
{
  "version": 1,
  "request_id": "01924a3e-...",
  "ok": true,
  "result": {
    "new_pane_id": "def456",
    "new_session_id": "sess789"
  }
}
```

**Error Response:**

```json
{
  "version": 1,
  "request_id": "01924a3e-...",
  "ok": false,
  "error": {
    "code": "pane_not_found",
    "message": "No pane with that id exists in this project."
  }
}
```

### Method Mapping

| Wire Method | OmaCommand |
|---|---|
| `project.list` | `ProjectCommand::List` |
| `project.create` | `ProjectCommand::Create` |
| `project.select` | `ProjectCommand::Select` |
| `project.set-directory` | `ProjectCommand::SetDirectory` |
| `project.root` | `ProjectCommand::Root` |
| `tab.list` | `TabCommand::List` |
| `tab.create` | `TabCommand::Create` |
| `tab.close` | `TabCommand::Close` |
| `pane.list` | `PaneCommand::List` |
| `pane.split` | `PaneCommand::Split` |
| `pane.close` | `PaneCommand::Close` |
| `pane.focus` | `PaneCommand::Focus` |
| `pane.resize` | `PaneCommand::Resize` |
| `pane.equalize` | `PaneCommand::Equalize` |
| `terminal.list` | `TerminalCommand::List` |
| `terminal.create` | `TerminalCommand::Create` |
| `terminal.send` | `TerminalCommand::SendBytes` |
| `terminal.run` | `TerminalCommand::RunCommand` |
| `terminal.read` | `TerminalCommand::ReadVisible` |
| `history.enable` | `HistoryCommand::EnablePersistence` |
| `history.disable` | `HistoryCommand::DisablePersistence` |
| `history.status` | `HistoryCommand::Status` |
| `history.list` | `HistoryCommand::ListJournal` |
| `history.pause` | `HistoryCommand::PausePane` |
| `history.resume` | `HistoryCommand::ResumePane` |
| `history.clear-pane` | `HistoryCommand::ClearPane` |
| `history.clear-project` | `HistoryCommand::ClearProject` |
| `history.clear-all` | `HistoryCommand::ClearWorkspace` |
| `file.list` | `FileCommand::List` |
| `file.search` | `FileCommand::Search` |
| `file.open` | `FileCommand::Open` |
| `git.status` | `GitCommand::Status` |
| `git.history` | `GitCommand::History` |
| `git.commit-files` | `GitCommand::CommitFiles` |
| `git.branch-list` | `GitCommand::BranchList` |
| `git.branch-create` | `GitCommand::BranchCreate` |
| `git.branch-checkout` | `GitCommand::BranchCheckout` |
| `git.branch-delete` | `GitCommand::BranchDelete` |
| `git.branch-rename` | `GitCommand::BranchRename` |
| `git.fetch` | `GitCommand::SyncFetch` |
| `git.pull` | `GitCommand::SyncPull` |
| `git.push` | `GitCommand::SyncPush` |
| `git.stash-list` | `GitCommand::StashList` |
| `git.stash-push` | `GitCommand::StashPush` |
| `git.stash-apply` | `GitCommand::StashApply` |
| `git.stash-pop` | `GitCommand::StashPop` |
| `git.stash-drop` | `GitCommand::StashDrop` |
| `git.blame` | `GitCommand::Blame` |
| `git.stage` | `GitCommand::Stage` |
| `git.stage-hunk` | `GitCommand::StageHunk` |
| `git.unstage` | `GitCommand::Unstage` |
| `git.discard` | `GitCommand::Discard` |
| `git.commit` | `GitCommand::Commit` |
| `diff.show` | `DiffCommand::Show` |
| `diff.show-commit` | `DiffCommand::ShowCommit` |
| `diff.list-files` | `DiffCommand::ListFiles` |
| `process.list` | `ProcessCommand::List` |
| `process.kill` | `ProcessCommand::Kill` |

### Socket Path

```
$XDG_RUNTIME_DIR/omaterm.sock
```

Validate ownership and permissions of `$XDG_RUNTIME_DIR`. If unavailable, use an
owner-validated private directory such as `/tmp/omaterm-$UID/` (mode `0700`) with
`omaterm.sock` inside it. Reject symlinks, wrong owners, and pre-existing unsafe
directories. Never bind directly to a predictable shared `/tmp` socket path.

Probe an existing socket before binding. An active instance keeps ownership;
another instance must not unlink it. Remove stale endpoints only after verifying
ownership/type and serializing startup with a lock. Shutdown removes only the
endpoint owned by this server instance. Test stale, active, and raced startup.

## Architecture Notes

### Connection Flow

```
Client connects to Unix socket
  ↓
Send newline-delimited JSON request
  ↓
Server reads line, deserializes IpcRequest
  ↓
Validate protocol version
  ↓
Resolve credential and authorize method and target project
  ↓
Map method string → OmaCommand
  ↓
Dispatch through CommandRouter
  ↓
Serialize CommandResult → IpcResponse
  ↓
Send newline-delimited JSON response
```

### Bounded Inputs

All fields have limits enforced server-side:

```
max request frame:       64 KiB (UTF-8 JSON, including newline)
max decoded send bytes:  8 KiB (also subject to encoded frame limit)
max read lines:          1000
max read columns:        1000
max response frame:      1 MiB (including JSON escaping and newline)
max argv count:          256
max argument bytes:      4096 (aggregate still subject to request frame limit)
max connections:         32
requests per connection: 1 in flight; sequential request/response
incomplete-frame timeout: 5 seconds
request deadline:       10 seconds
```

Reject requests exceeding limits with `invalid_request`. Enforce framing limits
incrementally before deserializing; do not use an unbounded line reader. Bound
response construction as well as transmission: cap text on UTF-8 boundaries and
return `truncated: true`, accounting for JSON escaping. Apply equivalent bounds
to list responses (explicit limit/truncation), names, and method/ID fields. Reject
control characters in structured fields; raw terminal bytes are intentionally
different. Test exact boundaries, multibyte text, escaping, and slow clients.

Use base64 for raw `terminal.send` data; JSON carries argv arrays for `terminal.run`.
Typed protocol DTOs and method mappings are independently round-trip tested.
Unknown methods/fields and invalid selectors produce stable errors. Request IDs
correlate responses; they do not promise replay deduplication. Clients must not
automatically retry mutations after an ambiguous disconnect/timeout.

### Protocol Versioning

```rust
const PROTOCOL_VERSION: u32 = 1;

// Reject unsupported versions
if request.version != PROTOCOL_VERSION {
    return IpcResponse::error("unsupported_version", ...);
}
```

Protocol version is independent of application semver.

### Shutdown Sequence

```
1. Stop accepting new connections
2. Cancel queued requests; settle/cancel in-flight effects within bounded deadlines
3. Capture/persist final workspace; report save failure without skipping cleanup
4. Terminate/reap sessions, cancel I/O, close PTYs, revoke capabilities
5. Close clients and remove this instance's socket/credential files
6. Release UI/GPU resources
```

### Security

- Socket permissions: owner-only (`0600`), within a validated private directory.
- Validate peer UID where supported; this supplements token scope, not replaces it.
- Issue unpredictable credentials (at least 256 random bits) per terminal session,
  bound to its project and originating pane/session. Revoke on session close and
  regenerate on restore. A project credential cannot create/select another project,
  query its IDs/output, or target its tabs/panes/splits/sessions.
- List queries filter to authorized scope; reject explicit cross-project targets
  with `cross_project_denied`. Resolve selectors and check ownership server-side
  immediately before effects. Global creation requires local-user authority.
- Inject `OMATERM_SOCKET`, `OMATERM_TOKEN`, `OMATERM_PROJECT_ID`, `OMATERM_TAB_ID`,
  `OMATERM_PANE_ID`, and `OMATERM_SESSION_ID` into child environments. Initialize
  capability/socket coordination before spawning/restoring shells at app startup.
- An outside CLI may use an explicit local-user credential stored owner-only in
  the private runtime directory. This credential has application-wide authority;
  it is never injected into terminal children. A project-scoped CLI must not
  silently fall back to it when its own token is missing, expired, or rejected.
- Same-UID processes can generally access each other's files/environment; these
  capabilities enforce API scope, not OS sandbox isolation against a hostile user.
- Never log credentials, input payloads, clipboard data, or terminal text by default.

### Cancellation and UI Handoff

Socket tasks submit bounded work to the M7 application owner and await responses;
they do not mutate UI state directly. Disconnect/deadline/shutdown cancels queued
work and releases resources. Once a mutation is committed, disconnect cannot undo
it; a timeout may have an unknown outcome to the client. Recheck session existence
and token validity before effects. No waiter task survives its caller or session.

## Implementation Steps

1. **Create `omaterm-protocol` crate** — define `IpcRequest`, `IpcResponse` with serde
2. **Create `omaterm-ipc` crate** — server and client modules
3. **Implement server** — listen on Unix socket, accept connections, handle requests
4. **Implement method routing** — map method strings to `OmaCommand` variants
5. **Implement client** — connect to socket, send request, receive response
6. **Start server on app launch** — background task in the desktop process
7. **Implement shutdown** — clean socket removal on app exit
8. **Add protocol version checking**
9. **Add input bounds checking**
10. **Write integration tests** — client → server → command → response round-trip

## Acceptance Criteria

- [ ] Unix socket is created at `$XDG_RUNTIME_DIR/omaterm.sock` on app start
- [ ] External process can connect and send JSON request
- [ ] Server responds with correct JSON response
- [ ] `pane.list` via IPC returns same data as UI shows
- [ ] `pane.split` via IPC causes visible split in the desktop app
- [ ] Protocol version mismatch returns `unsupported_version` error
- [ ] Invalid method returns `invalid_request` error
- [ ] Socket is removed on clean shutdown
- [ ] No data corruption from concurrent requests
- [ ] `cargo test` covers request/response serialization round-trips
- [ ] Two-project tests reject cross-scope targets and filter lists
- [ ] Token expiry/revocation, wrong/missing credentials, and peer mismatch tested
- [ ] Frame/response caps, slow clients, disconnects, deadlines, and queue limits tested
- [ ] Stale socket, unsafe fallback directory, and active-instance races tested

## Non-Goals

- No delegation hierarchy or agent orchestration (minimal project scope is required)
- No event streaming / subscriptions
- No multiplexed connections
- No TLS
- No separate daemon process

## References

- Blueprint §18 — IPC Architecture
- Blueprint §19 — IPC Wire Protocol
- Blueprint §20 — Capability and Security Model
- Blueprint §64 — Bounded External Inputs
- Blueprint §65 — Protocol Versioning
- Blueprint §66 — Cancellation
- Blueprint §67 — Shutdown
- Blueprint §68, Slice 8 — IPC
