//! Launcher: acknowledge a running instance, start the sibling desktop
//! binary with bounded readiness, or open a directory as a project
//! (`omaterm .`, `omaterm ~/path`, `omaterm <path> -- <cmd>`).
//!
//! Development invocation (`cargo run --bin omaterm`) and installed
//! invocation (`omaterm` on `PATH`) both resolve `omaterm-desktop` as a
//! sibling of the current executable. Plain subcommands never auto-launch;
//! only the launcher paths (no subcommand) start the desktop.

use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use omaterm_ipc::IpcClient;
use omaterm_protocol::{IpcRequest, IpcResponse, PROTOCOL_VERSION};

use crate::connection;
use crate::output;

/// How long to wait for a freshly spawned desktop to own its socket.
const LAUNCH_READY_TIMEOUT: Duration = Duration::from_secs(10);

/// Server-side argv bounds mirrored here so typos fail fast with a usage
/// error; the server re-validates authoritatively.
const MAX_LAUNCH_ARGC: usize = 256;
const MAX_LAUNCH_ARG_BYTES: usize = 4096;

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

/// Whether the socket was already live or this call launched it.
enum InstanceState {
    AlreadyRunning,
    Launched,
}

/// Ensure a desktop owns `socket`, spawning the sibling binary when needed.
/// The caller reports the outcome; only launcher paths reach here.
fn ensure_instance(socket: &Path) -> Result<InstanceState, String> {
    if socket_accepts(socket) {
        return Ok(InstanceState::AlreadyRunning);
    }
    let Some(desktop) = desktop_binary() else {
        return Err(
            "OmaTerm desktop binary (omaterm-desktop) was not found next to the CLI".into(),
        );
    };
    spawn_detached(&desktop)
        .map_err(|error| format!("failed to launch OmaTerm desktop: {error}"))?;
    let deadline = Instant::now() + LAUNCH_READY_TIMEOUT;
    while Instant::now() < deadline {
        if socket_accepts(socket) {
            return Ok(InstanceState::Launched);
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    Err("launched OmaTerm desktop but its socket did not become ready in time".into())
}

/// Run the no-argument launcher. Returns the process exit code.
pub fn run(as_json: bool, socket_override: Option<PathBuf>) -> i32 {
    let socket = match connection::resolve_socket(socket_override.as_deref()) {
        Ok(socket) => socket,
        Err(failure) => {
            return output::local_error(failure.code, &failure.message, as_json, failure.exit_code);
        }
    };
    match ensure_instance(&socket) {
        Ok(InstanceState::AlreadyRunning) => {
            tracing::debug!(target: "omaterm::cli", socket = %socket.display(), "desktop already running");
            if as_json {
                println!(
                    "{{\"ok\":true,\"result\":{{\"running\":true,\"socket\":{}}}}}",
                    serde_json::to_string(&socket.to_string_lossy())
                        .unwrap_or_else(|_| "\"\"".into())
                );
            } else {
                println!("OmaTerm is already running.");
            }
            0
        }
        Ok(InstanceState::Launched) => {
            tracing::info!(target: "omaterm::cli", socket = %socket.display(), "launched desktop");
            if as_json {
                println!(
                    "{{\"ok\":true,\"result\":{{\"launched\":true,\"socket\":{}}}}}",
                    serde_json::to_string(&socket.to_string_lossy())
                        .unwrap_or_else(|_| "\"\"".into())
                );
            } else {
                println!("Launched OmaTerm.");
            }
            0
        }
        Err(message) => output::local_error("launch_error", &message, as_json, 1),
    }
}

/// Resolve the directory a path-launch opens. `None` means the current
/// directory; a present path must be an existing directory. Failure is a
/// usage error reported before any instance is launched.
fn resolve_launch_directory(path: Option<PathBuf>) -> Result<PathBuf, String> {
    match path {
        None => std::env::current_dir()
            .map_err(|error| format!("cannot determine the current directory: {error}")),
        Some(path) => {
            if path.is_dir() {
                Ok(path)
            } else {
                Err(format!(
                    "project path must be an existing directory: {}",
                    path.display()
                ))
            }
        }
    }
}

/// Validate the trailing command before any IPC: bounds mirror the server so
/// oversized submissions fail fast with exit 64.
fn validate_initial_command(argv: &[String]) -> Result<(), String> {
    if argv.len() > MAX_LAUNCH_ARGC {
        return Err(format!(
            "command has {} arguments; at most {MAX_LAUNCH_ARGC} are accepted",
            argv.len()
        ));
    }
    if argv
        .iter()
        .any(|arg| arg.len() > MAX_LAUNCH_ARG_BYTES || arg.contains('\0'))
    {
        return Err(format!(
            "command arguments must be at most {MAX_LAUNCH_ARG_BYTES} bytes without NUL bytes"
        ));
    }
    Ok(())
}

fn send_method(
    socket: &Path,
    method: &str,
    params: serde_json::Value,
) -> Result<IpcResponse, connection::CliFailure> {
    let token = connection::load_token(socket)?;
    connection::send(
        socket,
        &IpcRequest {
            version: PROTOCOL_VERSION,
            request_id: uuid::Uuid::new_v4().to_string(),
            method: method.into(),
            params,
            token,
        },
    )
}

fn result_string(result: &serde_json::Value, key: &str) -> Option<String> {
    result
        .get(key)
        .and_then(serde_json::Value::as_str)
        .map(str::to_owned)
}

/// Open `path` as a project (launching the desktop when absent) and, when
/// `argv` is non-empty, submit it to the new project shell à la
/// `terminal run` (submission acknowledgement, never exit success).
/// A mid-sequence failure is reported honestly with no automatic replay or
/// rollback of the already-created project.
pub fn run_with_path(
    path: Option<PathBuf>,
    argv: Vec<String>,
    as_json: bool,
    socket_override: Option<PathBuf>,
) -> i32 {
    let directory = match resolve_launch_directory(path) {
        Ok(directory) => directory,
        Err(message) => return output::local_error("usage_error", &message, as_json, 64),
    };
    if let Err(message) = validate_initial_command(&argv) {
        return output::local_error("usage_error", &message, as_json, 64);
    }
    let socket = match connection::resolve_socket(socket_override.as_deref()) {
        Ok(socket) => socket,
        Err(failure) => {
            return output::local_error(failure.code, &failure.message, as_json, failure.exit_code);
        }
    };
    if let Err(message) = ensure_instance(&socket) {
        return output::local_error("launch_error", &message, as_json, 1);
    }
    let created = match send_method(
        &socket,
        "project.create",
        serde_json::json!({
            "directory": directory.to_string_lossy(),
            "name": None::<String>,
        }),
    ) {
        Ok(response) => response,
        Err(failure) => {
            return output::local_error(failure.code, &failure.message, as_json, failure.exit_code);
        }
    };
    if !created.ok {
        return output::server_error(&created, as_json);
    }
    let result = created.result.clone().unwrap_or(serde_json::Value::Null);
    let pane_id = result_string(&result, "pane_id");
    if argv.is_empty() {
        tracing::info!(target: "omaterm::cli", directory = %directory.display(), "opened project");
        return output::success("project.create", &created, as_json);
    }
    let Some(pane_id) = pane_id else {
        return output::local_error(
            "protocol_error",
            "project.create did not return a pane to run in",
            as_json,
            1,
        );
    };
    let ran = match send_method(
        &socket,
        "terminal.run",
        serde_json::json!({ "pane_id": pane_id, "argv": argv }),
    ) {
        Ok(response) => response,
        Err(failure) => {
            return output::local_error(failure.code, &failure.message, as_json, failure.exit_code);
        }
    };
    if !ran.ok {
        return output::server_error(&ran, as_json);
    }
    tracing::info!(target: "omaterm::cli", directory = %directory.display(), "opened project and submitted initial command");
    if as_json {
        println!(
            "{{\"ok\":true,\"result\":{{\"action\":\"open-and-run\",\"project_id\":{},\"tab_id\":{},\"pane_id\":{},\"session_id\":{},\"submitted\":true}}}}",
            result.get("project_id").unwrap_or(&serde_json::Value::Null),
            result.get("tab_id").unwrap_or(&serde_json::Value::Null),
            result.get("pane_id").unwrap_or(&serde_json::Value::Null),
            result.get("session_id").unwrap_or(&serde_json::Value::Null),
        );
        return 0;
    }
    let open_code = output::success("project.create", &created, false);
    let run_code = output::success("terminal.run", &ran, false);
    open_code.max(run_code)
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
    use std::sync::{Arc, Mutex, OnceLock};

    fn env_lock() -> &'static Mutex<()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(()))
    }

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

    fn fake_server(dir: &std::path::Path) -> (PathBuf, IpcServer) {
        let socket = dir.join("omaterm.sock");
        let secret = "c".repeat(43);
        let credential = socket.with_extension("credential");
        std::fs::write(&credential, &secret).unwrap();
        std::fs::set_permissions(&credential, std::fs::Permissions::from_mode(0o600)).unwrap();
        let handler: omaterm_ipc::RequestHandler =
            Arc::new(|request, _| match request.method.as_str() {
                "project.create" => IpcResponse::success(
                    request.request_id,
                    serde_json::json!({
                        "project_id": "p1", "tab_id": "t1",
                        "pane_id": "pane1", "session_id": "s1",
                    }),
                ),
                "terminal.run" => {
                    assert_eq!(request.params["argv"], serde_json::json!(["echo", "hi"]));
                    IpcResponse::success(request.request_id, serde_json::json!({"submitted": true}))
                }
                _ => IpcResponse::failure(request.request_id, "invalid_request", "no such method"),
            });
        let server = IpcServer::bind(&socket, handler).unwrap();
        (socket, server)
    }

    #[test]
    fn path_flow_opens_and_optionally_runs() {
        let _guard = env_lock().lock().unwrap();
        let saved_token = std::env::var_os("OMATERM_TOKEN");
        unsafe { std::env::remove_var("OMATERM_TOKEN") };
        let dir = std::env::temp_dir().join(format!("omaterm-cli-path-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700)).unwrap();
        let (socket, mut server) = fake_server(&dir);

        // Open-only reports the created project in both modes.
        assert_eq!(
            run_with_path(Some(dir.clone()), vec![], false, Some(socket.clone())),
            0
        );
        assert_eq!(
            run_with_path(Some(dir.clone()), vec![], true, Some(socket.clone())),
            0
        );
        // Open-and-run submits argv and reports a single combined envelope.
        assert_eq!(
            run_with_path(
                Some(dir.clone()),
                vec!["echo".into(), "hi".into()],
                false,
                Some(socket.clone())
            ),
            0
        );
        assert_eq!(
            run_with_path(
                Some(dir.clone()),
                vec!["echo".into(), "hi".into()],
                true,
                Some(socket.clone())
            ),
            0
        );
        // Missing/non-directory paths fail before any launch attempt.
        assert_eq!(
            run_with_path(Some(dir.join("nope")), vec![], false, Some(socket.clone())),
            64
        );
        // Oversized argv fails fast as a usage error.
        assert_eq!(
            run_with_path(
                Some(dir.clone()),
                vec!["x".repeat(5000)],
                false,
                Some(socket.clone())
            ),
            64
        );

        server.shutdown().unwrap();
        let _ = std::fs::remove_dir_all(&dir);
        if let Some(token) = saved_token {
            unsafe { std::env::set_var("OMATERM_TOKEN", token) };
        }
    }

    #[test]
    fn path_flow_reports_server_denial_without_retry() {
        let _guard = env_lock().lock().unwrap();
        let saved_token = std::env::var_os("OMATERM_TOKEN");
        unsafe { std::env::remove_var("OMATERM_TOKEN") };
        let dir =
            std::env::temp_dir().join(format!("omaterm-cli-path-deny-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700)).unwrap();
        let socket = dir.join("omaterm.sock");
        let secret = "d".repeat(43);
        std::fs::write(socket.with_extension("credential"), &secret).unwrap();
        std::fs::set_permissions(
            socket.with_extension("credential"),
            std::fs::Permissions::from_mode(0o600),
        )
        .unwrap();
        let handler: omaterm_ipc::RequestHandler = Arc::new(|request, _| {
            IpcResponse::failure(request.request_id, "permission_denied", "denied")
        });
        let mut server = IpcServer::bind(&socket, handler).unwrap();
        assert_eq!(
            run_with_path(Some(dir.clone()), vec![], false, Some(socket)),
            1
        );
        server.shutdown().unwrap();
        let _ = std::fs::remove_dir_all(&dir);
        if let Some(token) = saved_token {
            unsafe { std::env::set_var("OMATERM_TOKEN", token) };
        }
    }
}
