//! No-argument launcher: acknowledge a running instance or start the
//! sibling desktop binary with bounded readiness.
//!
//! Development invocation (`cargo run --bin omaterm`) and installed
//! invocation (`omaterm` on `PATH`) both resolve `omaterm-desktop` as a
//! sibling of the current executable. Subcommands never auto-launch; only
//! this no-argument path starts the desktop.

use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use omaterm_ipc::IpcClient;

use crate::connection;
use crate::output;

/// How long to wait for a freshly spawned desktop to own its socket.
const LAUNCH_READY_TIMEOUT: Duration = Duration::from_secs(10);

/// Resolve the desktop binary next to the running CLI executable.
pub fn desktop_binary() -> Option<PathBuf> {
    let current = std::env::current_exe().ok()?;
    let dir = current.parent()?;
    let candidate = dir.join("omaterm-desktop");
    if candidate.is_file() {
        return Some(candidate);
    }
    // When tests or wrappers relocate the binary, fall back to PATH lookup.
    std::env::var_os("PATH").and_then(|paths| {
        std::env::split_paths(&paths)
            .map(|dir| dir.join("omaterm-desktop"))
            .find(|path| path.is_file())
    })
}

/// True when any listener accepts on the socket, regardless of credentials.
fn socket_accepts(path: &Path) -> bool {
    IpcClient::connect(path).is_ok()
}

/// Run the no-argument launcher. Returns the process exit code.
pub fn run(as_json: bool, socket_override: Option<PathBuf>) -> i32 {
    let socket = match connection::resolve_socket(socket_override.as_deref()) {
        Ok(socket) => socket,
        Err(failure) => {
            return output::local_error(failure.code, &failure.message, as_json, failure.exit_code);
        }
    };
    if socket_accepts(&socket) {
        if as_json {
            println!(
                "{{\"ok\":true,\"result\":{{\"running\":true,\"socket\":{}}}}}",
                serde_json::to_string(&socket.to_string_lossy()).unwrap_or_else(|_| "\"\"".into())
            );
        } else {
            println!("OmaTerm is already running.");
        }
        return 0;
    }
    let Some(desktop) = desktop_binary() else {
        return output::local_error(
            "launch_error",
            "OmaTerm desktop binary (omaterm-desktop) was not found next to the CLI",
            as_json,
            1,
        );
    };
    if let Err(error) = spawn_detached(&desktop) {
        return output::local_error(
            "launch_error",
            &format!("failed to launch OmaTerm desktop: {error}"),
            as_json,
            1,
        );
    }
    let deadline = Instant::now() + LAUNCH_READY_TIMEOUT;
    while Instant::now() < deadline {
        if socket_accepts(&socket) {
            if as_json {
                println!(
                    "{{\"ok\":true,\"result\":{{\"launched\":true,\"socket\":{}}}}}",
                    serde_json::to_string(&socket.to_string_lossy())
                        .unwrap_or_else(|_| "\"\"".into())
                );
            } else {
                println!("Launched OmaTerm.");
            }
            return 0;
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    output::local_error(
        "launch_error",
        "launched OmaTerm desktop but its socket did not become ready in time",
        as_json,
        1,
    )
}

fn spawn_detached(desktop: &Path) -> std::io::Result<()> {
    use std::process::{Command, Stdio};
    Command::new(desktop)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use omaterm_ipc::IpcServer;
    use omaterm_protocol::IpcResponse;
    use std::os::unix::fs::PermissionsExt;
    use std::sync::Arc;

    #[test]
    fn existing_instance_is_acknowledged_without_launch() {
        let dir = std::env::temp_dir().join(format!("omaterm-cli-launch-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700)).unwrap();
        let socket = dir.join("omaterm.sock");
        let handler: omaterm_ipc::RequestHandler = Arc::new(|request, _| {
            IpcResponse::success(request.request_id, serde_json::json!({"projects": []}))
        });
        let server = IpcServer::bind(&socket, handler).unwrap();
        assert!(socket_accepts(&socket));
        // The launcher must report running without spawning anything when the
        // socket already accepts, even with an explicit override socket.
        let code = run(false, Some(socket.clone()));
        assert_eq!(code, 0);
        let mut server = server;
        server.shutdown().unwrap();
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn desktop_binary_resolution_is_documented() {
        // This test documents the sibling-binary contract without asserting
        // on the developer's install layout: it passes whether or not the
        // binary is present, but exercises the lookup path.
        let _ = desktop_binary();
    }
}
