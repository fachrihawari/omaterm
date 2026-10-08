//! Socket resolution, credential loading, and thin IPC round-trips.
//!
//! The CLI performs no workspace logic. It resolves the socket path,
//! attaches the correct credential for that socket, sends one request,
//! and returns the response. Subcommands never auto-launch the desktop.

use std::fs;
use std::path::{Path, PathBuf};

use omaterm_ipc::{IpcClient, IpcServer};
use omaterm_protocol::{CapabilityToken, IpcRequest, IpcResponse};

/// Failure with a stable code and process exit status.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CliFailure {
    /// Stable machine-readable code (`connection_error`, `auth_error`, ...).
    pub code: &'static str,
    pub message: String,
    /// 2 = connection failure, 1 = command/authorization failure.
    pub exit_code: i32,
}

impl CliFailure {
    pub fn connection(message: impl Into<String>) -> Self {
        Self {
            code: "connection_error",
            message: message.into(),
            exit_code: 2,
        }
    }

    pub fn auth(message: impl Into<String>) -> Self {
        Self {
            code: "auth_error",
            message: message.into(),
            exit_code: 1,
        }
    }
}

/// Resolve the socket without proving liveness: explicit `--socket`, then
/// `OMATERM_SOCKET`, then the validated M8 default.
pub fn resolve_socket(socket_override: Option<&Path>) -> Result<PathBuf, CliFailure> {
    if let Some(path) = socket_override {
        return Ok(path.to_path_buf());
    }
    if let Some(env) = std::env::var_os("OMATERM_SOCKET")
        && !env.is_empty()
    {
        return Ok(PathBuf::from(env));
    }
    IpcServer::default_socket_path()
        .map_err(|error| CliFailure::connection(format!("cannot resolve OmaTerm socket: {error}")))
}

/// Load the credential that matches the resolved socket.
///
/// In-app callers provide `OMATERM_TOKEN` and never touch the credential
/// file. Outside callers read only the owner-only credential stored beside
/// the resolved socket, so a default-instance credential is never sent to
/// an unrelated override socket. A present but invalid `OMATERM_TOKEN`
/// fails without falling back to the file.
pub fn load_token(socket: &Path) -> Result<Option<CapabilityToken>, CliFailure> {
    if let Ok(env) = std::env::var("OMATERM_TOKEN") {
        if env.is_empty() {
            return Err(CliFailure::auth(
                "OMATERM_TOKEN is set but empty; refusing to fall back to the credential file",
            ));
        }
        return CapabilityToken::from_secret(env)
            .map(Some)
            .ok_or_else(|| CliFailure::auth("OMATERM_TOKEN has an invalid encoding or length"));
    }
    let credential = socket.with_extension("credential");
    let metadata = fs::symlink_metadata(&credential).map_err(|_| {
        CliFailure::connection("OmaTerm is not running. Start it first with `omaterm`.")
    })?;
    if !metadata.is_file() {
        return Err(CliFailure::auth(
            "OmaTerm credential path is not a regular file; refusing to use it",
        ));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::{MetadataExt, PermissionsExt};
        if metadata.uid() != unsafe { libc::getuid() } {
            return Err(CliFailure::auth(
                "OmaTerm credential is not owned by this user; refusing to use it",
            ));
        }
        if metadata.permissions().mode() & 0o077 != 0 {
            return Err(CliFailure::auth(
                "OmaTerm credential file has unsafe permissions; refusing to use it",
            ));
        }
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if metadata.file_attributes() & 0x400 != 0 {
            return Err(CliFailure::auth(
                "OmaTerm credential path is a reparse point; refusing to use it",
            ));
        }
    }
    let secret = fs::read_to_string(&credential).map_err(|error| {
        CliFailure::connection(format!(
            "OmaTerm credential is unreadable ({error}); is OmaTerm running?"
        ))
    })?;
    let secret = secret.trim().to_owned();
    CapabilityToken::from_secret(secret)
        .map(Some)
        .ok_or_else(|| CliFailure::auth("OmaTerm credential has an invalid encoding or length"))
}

/// Connect and perform exactly one request. Connect failures mean no
/// instance is listening (exit 2). Post-connect transport failures have an
/// ambiguous outcome and must not be retried blindly (exit 1).
pub fn send(socket: &Path, request: &IpcRequest) -> Result<IpcResponse, CliFailure> {
    let mut client = IpcClient::connect(socket).map_err(|_| {
        CliFailure::connection("OmaTerm is not running. Start it first with `omaterm`.")
    })?;
    client.request(request).map_err(|error| CliFailure {
        code: "transport_error",
        message: format!(
            "IPC request failed ({error}); outcome may be unknown, do not retry mutations"
        ),
        exit_code: 1,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{Mutex, OnceLock};

    fn env_lock() -> &'static Mutex<()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(()))
    }

    #[test]
    fn socket_override_wins_over_environment() {
        let _guard = env_lock().lock().unwrap();
        unsafe { std::env::set_var("OMATERM_SOCKET", "/tmp/env.sock") };
        let resolved = resolve_socket(Some(Path::new("/tmp/flag.sock"))).unwrap();
        assert_eq!(resolved, PathBuf::from("/tmp/flag.sock"));
        unsafe { std::env::remove_var("OMATERM_SOCKET") };
    }

    #[test]
    fn in_app_token_never_reads_the_file() {
        let _guard = env_lock().lock().unwrap();
        let dir = std::env::temp_dir().join(format!("omaterm-cli-conn-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        let socket = dir.join("omaterm.sock");
        let token = format!("{}-token-ok", "a".repeat(40));
        unsafe { std::env::set_var("OMATERM_TOKEN", &token) };
        // No credential file exists; the env token must still load.
        let loaded = load_token(&socket).unwrap().unwrap();
        assert_eq!(loaded.expose_for_transport(), token);
        unsafe { std::env::remove_var("OMATERM_TOKEN") };
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn missing_credential_file_is_a_connection_failure() {
        let _guard = env_lock().lock().unwrap();
        unsafe { std::env::remove_var("OMATERM_TOKEN") };
        let dir = std::env::temp_dir().join(format!("omaterm-cli-missing-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        let socket = dir.join("omaterm.sock");
        let failure = load_token(&socket).unwrap_err();
        assert_eq!(failure.exit_code, 2);
        let _ = fs::remove_dir_all(&dir);
    }
}
