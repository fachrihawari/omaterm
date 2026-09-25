# Milestone 8 — IPC

> Unix domain socket server in the desktop process. JSON wire protocol v1. The bridge between OmaTerm's in-process commands and external callers.

## Overview

This milestone adds a Unix domain socket server to OmaTerm's desktop process. External programs (the CLI, future AI agents) connect to this socket and send JSON-encoded command requests. The server deserializes requests, dispatches them through the `CommandRouter`, and returns JSON responses.

## Goals

- [ ] `omaterm-protocol` crate — wire types (request, response, error)
- [ ] `omaterm-ipc` crate — Unix socket server and client
- [ ] Server starts automatically with the desktop app
- [ ] JSON protocol v1 with request/response semantics
- [ ] Capability token validation (basic)
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
| `tab.list` | `TabCommand::List` |
| `tab.create` | `TabCommand::Create` |
| `tab.close` | `TabCommand::Close` |
| `pane.list` | `PaneCommand::List` |
| `pane.split` | `PaneCommand::Split` |
| `pane.close` | `PaneCommand::Close` |
| `pane.focus` | `PaneCommand::Focus` |
| `terminal.list` | `TerminalCommand::List` |
| `terminal.send` | `TerminalCommand::SendBytes` |
| `terminal.run` | `TerminalCommand::RunCommand` |
| `terminal.read` | `TerminalCommand::ReadVisible` |

### Socket Path

```
$XDG_RUNTIME_DIR/omaterm.sock
```

Fallback: `/tmp/omaterm-$UID.sock`

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
Validate token (basic for now)
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
max request size:        64 KB
max terminal read lines: 10000
max terminal read cols:  10000
max send bytes:          1 MB
max wait timeout:        300s
```

Reject requests exceeding limits with `invalid_request` error.

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
2. Finish in-flight requests (with timeout)
3. Close all client connections
4. Remove socket file
```

### Security

- Socket permissions: owner-only (`0600`)
- Token validation: each connected client provides a token
- For v0.1: simple shared-secret token written to a file at startup
- Future: project-scoped capability tokens

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

## Non-Goals

- No full capability token system (basic auth only)
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
