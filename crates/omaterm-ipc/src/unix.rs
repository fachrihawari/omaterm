//! Bounded newline-delimited JSON transport over owner-only Unix sockets.

use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write};
use std::os::fd::AsRawFd;
use std::os::unix::fs::{FileTypeExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use omaterm_protocol::{
    IpcRequest, IpcResponse, MAX_CONNECTIONS, MAX_REQUEST_FRAME, MAX_RESPONSE_FRAME,
    encode_response, validate_request,
};

#[derive(Debug, thiserror::Error)]
pub enum IpcError {
    #[error("invalid or unsafe socket directory: {0}")]
    UnsafeDirectory(PathBuf),
    #[error("socket endpoint already exists: {0}")]
    EndpointExists(PathBuf),
    #[error("IPC I/O error: {0}")]
    Io(#[from] std::io::Error),
    #[error("IPC server thread failed")]
    Thread,
}

pub type RequestHandler = Arc<dyn Fn(IpcRequest, Instant) -> IpcResponse + Send + Sync + 'static>;

/// Transport deadlines. Production defaults are 5s for an incomplete frame
/// and 10s total per request; tests use shorter values via
/// [`IpcServer::bind_with_timeouts`] so slow-client behavior is covered
/// without sleeping for seconds.
#[derive(Debug, Clone, Copy)]
pub struct ServerTimeouts {
    pub frame: Duration,
    pub request: Duration,
}

impl ServerTimeouts {
    pub const DEFAULT: Self = Self {
        frame: Duration::from_secs(5),
        request: Duration::from_secs(10),
    };
}

/// Running server. The handler must enqueue work to the desktop owner; it must
/// not mutate workspace/UI state on a connection thread.
pub struct IpcServer {
    path: PathBuf,
    identity: (u64, u64),
    stopping: Arc<AtomicBool>,
    accept_thread: Option<JoinHandle<()>>,
    _lock: File,
}

impl IpcServer {
    pub fn default_socket_path() -> Result<PathBuf, IpcError> {
        if let Some(runtime) = std::env::var_os("XDG_RUNTIME_DIR") {
            let runtime = PathBuf::from(runtime);
            validate_private_directory(&runtime)?;
            return Ok(runtime.join("omaterm.sock"));
        }
        let fallback = PathBuf::from(format!("/tmp/omaterm-{}", unsafe { libc::getuid() }));
        match fs::create_dir(&fallback) {
            Ok(()) => fs::set_permissions(&fallback, fs::Permissions::from_mode(0o700))?,
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {}
            Err(error) => return Err(IpcError::Io(error)),
        }
        validate_private_directory(&fallback)?;
        Ok(fallback.join("omaterm.sock"))
    }

    pub fn bind(path: impl AsRef<Path>, handler: RequestHandler) -> Result<Self, IpcError> {
        Self::bind_with_timeouts(path, handler, ServerTimeouts::DEFAULT)
    }

    pub fn bind_with_timeouts(
        path: impl AsRef<Path>,
        handler: RequestHandler,
        timeouts: ServerTimeouts,
    ) -> Result<Self, IpcError> {
        let path = path.as_ref().to_path_buf();
        validate_private_parent(&path)?;
        let lock_path = path.with_extension("lock");
        let existing_lock = match fs::symlink_metadata(&lock_path) {
            Ok(metadata) => {
                if !metadata.is_file()
                    || metadata.uid() != unsafe { libc::getuid() }
                    || metadata.permissions().mode() & 0o077 != 0
                {
                    return Err(IpcError::UnsafeDirectory(lock_path));
                }
                Some((metadata.dev(), metadata.ino()))
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
            Err(error) => return Err(IpcError::Io(error)),
        };
        let lock = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .mode(0o600)
            .custom_flags(libc::O_CLOEXEC | libc::O_NOFOLLOW | libc::O_NONBLOCK)
            .open(&lock_path)?;
        let lock_metadata = lock.metadata()?;
        if !lock_metadata.is_file()
            || lock_metadata.uid() != unsafe { libc::getuid() }
            || existing_lock.is_some_and(|id| id != (lock_metadata.dev(), lock_metadata.ino()))
        {
            return Err(IpcError::UnsafeDirectory(path));
        }
        if lock_metadata.permissions().mode() & 0o077 != 0 {
            return Err(IpcError::UnsafeDirectory(lock_path));
        }
        // Serialize endpoint inspection/bind across competing startups.
        // SAFETY: flock operates on this live file descriptor and has no pointer arguments.
        if unsafe { libc::flock(lock.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } != 0 {
            return Err(IpcError::EndpointExists(path));
        }
        if fs::symlink_metadata(&path).is_ok() {
            let metadata = fs::symlink_metadata(&path)?;
            if !metadata.file_type().is_socket() || metadata.uid() != unsafe { libc::getuid() } {
                return Err(IpcError::EndpointExists(path));
            }
            // Never unlink an active server. A successful connect proves that
            // another listener owns this endpoint; failed connect permits stale cleanup.
            match UnixStream::connect(&path) {
                Ok(_) => return Err(IpcError::EndpointExists(path)),
                Err(error)
                    if matches!(
                        error.kind(),
                        std::io::ErrorKind::ConnectionRefused | std::io::ErrorKind::NotFound
                    ) =>
                {
                    fs::remove_file(&path)?
                }
                Err(_) => return Err(IpcError::EndpointExists(path)),
            }
        }
        let listener = UnixListener::bind(&path)?;
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600))?;
        let metadata = fs::symlink_metadata(&path)?;
        let identity = (metadata.dev(), metadata.ino());
        listener.set_nonblocking(true)?;
        let stopping = Arc::new(AtomicBool::new(false));
        let accept_stopping = stopping.clone();
        let accept_thread = thread::Builder::new()
            .name("omaterm-ipc-accept".into())
            .spawn(move || {
                let active = Arc::new(AtomicUsize::new(0));
                let mut workers: Vec<JoinHandle<()>> = Vec::new();
                while !accept_stopping.load(Ordering::Acquire) {
                    let mut index = 0;
                    while index < workers.len() {
                        if workers[index].is_finished() {
                            let worker = workers.swap_remove(index);
                            let _ = worker.join();
                        } else {
                            index += 1;
                        }
                    }
                    match listener.accept() {
                        Ok((stream, _)) => {
                            if active.fetch_add(1, Ordering::AcqRel) >= MAX_CONNECTIONS {
                                active.fetch_sub(1, Ordering::AcqRel);
                                drop(stream);
                                continue;
                            }
                            let active = active.clone();
                            let handler = handler.clone();
                            let worker_stopping = accept_stopping.clone();
                            workers.push(thread::spawn(move || {
                                let _guard = ConnectionGuard(active);
                                let _ =
                                    handle_connection(stream, handler, worker_stopping, timeouts);
                            }));
                        }
                        Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                            thread::sleep(Duration::from_millis(10))
                        }
                        Err(_) => break,
                    }
                }
                for worker in workers {
                    let _ = worker.join();
                }
            })
            .map_err(IpcError::Io)?;
        Ok(Self {
            path,
            identity,
            stopping,
            accept_thread: Some(accept_thread),
            _lock: lock,
        })
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn shutdown(&mut self) -> Result<(), IpcError> {
        self.stopping.store(true, Ordering::Release);
        if let Some(thread) = self.accept_thread.take() {
            thread.join().map_err(|_| IpcError::Thread)?;
        }
        self.remove_owned_endpoint();
        Ok(())
    }

    fn remove_owned_endpoint(&self) {
        if let Ok(metadata) = fs::symlink_metadata(&self.path)
            && metadata.file_type().is_socket()
            && (metadata.dev(), metadata.ino()) == self.identity
        {
            let _ = fs::remove_file(&self.path);
        }
    }
}

impl Drop for IpcServer {
    fn drop(&mut self) {
        self.stopping.store(true, Ordering::Release);
        if let Some(thread) = self.accept_thread.take() {
            let _ = thread.join();
        }
        self.remove_owned_endpoint();
    }
}

struct ConnectionGuard(Arc<AtomicUsize>);
impl Drop for ConnectionGuard {
    fn drop(&mut self) {
        self.0.fetch_sub(1, Ordering::AcqRel);
    }
}

fn validate_private_parent(path: &Path) -> Result<(), IpcError> {
    let Some(parent) = path.parent() else {
        return Err(IpcError::UnsafeDirectory(path.to_path_buf()));
    };
    let metadata = fs::symlink_metadata(parent)
        .map_err(|_| IpcError::UnsafeDirectory(parent.to_path_buf()))?;
    validate_private_directory_metadata(parent, &metadata)
}

fn validate_private_directory(path: &Path) -> Result<(), IpcError> {
    let metadata =
        fs::symlink_metadata(path).map_err(|_| IpcError::UnsafeDirectory(path.to_path_buf()))?;
    validate_private_directory_metadata(path, &metadata)
}

fn validate_private_directory_metadata(
    path: &Path,
    metadata: &fs::Metadata,
) -> Result<(), IpcError> {
    let mode = metadata.permissions().mode() & 0o777;
    if !metadata.is_dir()
        || metadata.file_type().is_symlink()
        || metadata.uid() != unsafe { libc::getuid() }
        || mode & 0o077 != 0
    {
        return Err(IpcError::UnsafeDirectory(path.to_path_buf()));
    }
    Ok(())
}

fn handle_connection(
    mut stream: UnixStream,
    handler: RequestHandler,
    stopping: Arc<AtomicBool>,
    timeouts: ServerTimeouts,
) -> std::io::Result<()> {
    if !peer_authorized(peer_uid(&stream)?, unsafe { libc::getuid() }) {
        return Ok(());
    }
    loop {
        if stopping.load(Ordering::Acquire) {
            return Ok(());
        }
        let started = Instant::now();
        let frame_deadline = started + timeouts.frame;
        let request_deadline = started + timeouts.request;
        let mut frame = Vec::with_capacity(1024);
        let mut byte = [0u8; 1];
        loop {
            if stopping.load(Ordering::Acquire) {
                return Ok(());
            }
            let remaining = frame_deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Ok(());
            }
            stream.set_read_timeout(Some(remaining.min(Duration::from_millis(200))))?;
            match stream.read(&mut byte) {
                Ok(0) => return Ok(()),
                Ok(_) if byte[0] == b'\n' => break,
                Ok(_) => {
                    if frame.len() >= MAX_REQUEST_FRAME - 1 {
                        write_response(
                            &mut stream,
                            &IpcResponse::failure(
                                String::new(),
                                "invalid_request",
                                "request exceeds the 64 KiB frame limit",
                            ),
                        )?;
                        return Ok(());
                    }
                    frame.push(byte[0]);
                }
                Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
                Err(error)
                    if matches!(
                        error.kind(),
                        std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut
                    ) =>
                {
                    continue;
                }
                Err(error) => return Err(error),
            }
        }
        let request: IpcRequest = match serde_json::from_slice(&frame) {
            Ok(request) => request,
            Err(_) => {
                write_response(
                    &mut stream,
                    &IpcResponse::failure(
                        String::new(),
                        "invalid_request",
                        "malformed JSON request",
                    ),
                )?;
                return Ok(());
            }
        };
        let request_id = request.request_id.clone();
        let response = match validate_request(&request) {
            Ok(()) => handler(request, request_deadline),
            Err(error) => IpcResponse::failure(request_id, error.code, error.message),
        };
        let remaining = request_deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            stream.set_write_timeout(Some(Duration::from_secs(1)))?;
            write_response(
                &mut stream,
                &IpcResponse::failure(response.request_id, "timeout", "request deadline exceeded"),
            )?;
            return Ok(());
        }
        stream.set_write_timeout(Some(remaining))?;
        write_response(&mut stream, &response)?;
    }
}

fn write_response(stream: &mut UnixStream, response: &IpcResponse) -> std::io::Result<()> {
    match encode_response(response) {
        Ok(frame) => stream.write_all(&frame),
        Err(_) => {
            let frame = encode_response(&IpcResponse::failure(
                response.request_id.clone(),
                "response_too_large",
                "response exceeds the 1 MiB frame limit",
            ))
            .unwrap_or_else(|_| b"{}\n".to_vec());
            stream.write_all(&frame)
        }
    }
}

/// Same-UID policy for socket peers. The syscall stays in `handle_connection`;
/// this pure predicate carries the unit-testable decision. Cross-UID coverage
/// needs a multi-user harness, which this environment does not provide.
fn peer_authorized(actual: u32, expected: u32) -> bool {
    actual == expected
}

fn peer_uid(stream: &UnixStream) -> std::io::Result<u32> {
    let mut cred = libc::ucred {
        pid: 0,
        uid: 0,
        gid: 0,
    };
    let mut len = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
    // SAFETY: cred and len are valid writable buffers for SO_PEERCRED.
    let result = unsafe {
        libc::getsockopt(
            stream.as_raw_fd(),
            libc::SOL_SOCKET,
            libc::SO_PEERCRED,
            (&mut cred as *mut libc::ucred).cast(),
            &mut len,
        )
    };
    if result == 0 {
        Ok(cred.uid)
    } else {
        Err(std::io::Error::last_os_error())
    }
}

pub struct IpcClient {
    stream: UnixStream,
}
impl IpcClient {
    pub fn connect(path: impl AsRef<Path>) -> std::io::Result<Self> {
        Ok(Self {
            stream: UnixStream::connect(path)?,
        })
    }
    pub fn request(&mut self, request: &IpcRequest) -> std::io::Result<IpcResponse> {
        let deadline = Instant::now() + Duration::from_secs(10);
        let mut bytes = serde_json::to_vec(request)?;
        bytes.push(b'\n');
        if bytes.len() > MAX_REQUEST_FRAME {
            return Err(std::io::Error::new(
                std::io::ErrorKind::InvalidInput,
                "request exceeds frame limit",
            ));
        }
        self.stream
            .set_write_timeout(Some(Duration::from_secs(10)))?;
        self.stream.write_all(&bytes)?;
        let mut frame = Vec::with_capacity(1024);
        let mut byte = [0u8; 1];
        loop {
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::TimedOut,
                    "IPC request deadline exceeded",
                ));
            }
            self.stream.set_read_timeout(Some(remaining))?;
            self.stream.read_exact(&mut byte)?;
            if byte[0] == b'\n' {
                break;
            }
            if frame.len() >= MAX_RESPONSE_FRAME - 1 {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::InvalidData,
                    "response exceeds frame limit",
                ));
            }
            frame.push(byte[0]);
        }
        serde_json::from_slice(&frame).map_err(std::io::Error::other)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use omaterm_protocol::PROTOCOL_VERSION;

    #[test]
    fn client_server_round_trip_and_shutdown_removes_only_owned_socket() {
        let root = std::env::temp_dir().join(format!(
            "omaterm-ipc-{}-{}",
            unsafe { libc::getuid() },
            std::process::id()
        ));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let path = root.join("omaterm.sock");
        let handler: RequestHandler = Arc::new(|request, _deadline| {
            IpcResponse::success(request.request_id, serde_json::json!({"pong":true}))
        });
        let mut server = IpcServer::bind(&path, handler).unwrap();
        let request = IpcRequest {
            version: PROTOCOL_VERSION,
            request_id: "test-1".into(),
            method: "test.ping".into(),
            params: serde_json::json!({}),
            token: None,
        };
        let response = IpcClient::connect(&path)
            .unwrap()
            .request(&request)
            .unwrap();
        assert_eq!(response.result, Some(serde_json::json!({"pong":true})));
        let mut client = IpcClient::connect(&path).unwrap();
        assert!(client.request(&request).unwrap().ok);
        assert!(
            client.request(&request).unwrap().ok,
            "sequential requests reuse the same connection"
        );
        server.shutdown().unwrap();
        assert!(!path.exists());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn active_endpoint_is_not_unlinked_and_stale_socket_is_reclaimed() {
        use std::os::unix::net::UnixListener;

        let root = std::env::temp_dir().join(format!(
            "omaterm-ipc-lifecycle-{}-{}",
            unsafe { libc::getuid() },
            std::process::id()
        ));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let path = root.join("omaterm.sock");
        let handler: RequestHandler = Arc::new(|request, _deadline| {
            IpcResponse::success(request.request_id, serde_json::json!({}))
        });
        let server = IpcServer::bind(&path, handler.clone()).unwrap();
        assert!(matches!(
            IpcServer::bind(&path, handler.clone()),
            Err(IpcError::EndpointExists(_))
        ));
        assert!(path.exists());
        drop(server);
        let stale = UnixListener::bind(&path).unwrap();
        drop(stale);
        let server = IpcServer::bind(&path, handler).unwrap();
        assert!(path.exists());
        drop(server);
        assert!(!path.exists());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn rejects_group_or_world_accessible_socket_directories() {
        let root = std::env::temp_dir().join(format!(
            "omaterm-ipc-unsafe-{}-{}",
            unsafe { libc::getuid() },
            std::process::id()
        ));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o755)).unwrap();
        let handler: RequestHandler = Arc::new(|request, _deadline| {
            IpcResponse::success(request.request_id, serde_json::json!({}))
        });
        assert!(matches!(
            IpcServer::bind(root.join("omaterm.sock"), handler),
            Err(IpcError::UnsafeDirectory(_))
        ));
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn refuses_a_symlink_or_unsafe_preexisting_lock_without_changing_it() {
        use std::os::unix::fs::symlink;
        let root = std::env::temp_dir().join(format!("omaterm-ipc-lock-{}", std::process::id()));
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let path = root.join("omaterm.sock");
        let lock = root.join("omaterm.lock");
        let target = root.join("target");
        fs::write(&target, b"keep").unwrap();
        symlink(&target, &lock).unwrap();
        let handler: RequestHandler =
            Arc::new(|request, _| IpcResponse::success(request.request_id, serde_json::json!({})));
        assert!(matches!(
            IpcServer::bind(&path, handler.clone()),
            Err(IpcError::UnsafeDirectory(_))
        ));
        assert_eq!(fs::read(&target).unwrap(), b"keep");
        fs::remove_file(&lock).unwrap();
        fs::write(&lock, b"keep").unwrap();
        fs::set_permissions(&lock, fs::Permissions::from_mode(0o666)).unwrap();
        assert!(matches!(
            IpcServer::bind(&path, handler),
            Err(IpcError::UnsafeDirectory(_))
        ));
        assert_eq!(
            fs::metadata(&lock).unwrap().permissions().mode() & 0o777,
            0o666
        );
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn concurrent_clients_get_uncorrupted_responses_and_disconnects_are_safe() {
        let root =
            std::env::temp_dir().join(format!("omaterm-ipc-concurrent-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let path = root.join("omaterm.sock");
        let handler: RequestHandler = Arc::new(|request, _| {
            IpcResponse::success(
                request.request_id.clone(),
                serde_json::json!({"echo": request.request_id}),
            )
        });
        let mut server = IpcServer::bind(&path, handler).unwrap();
        // Abrupt disconnect without a request must not break the server.
        drop(UnixStream::connect(&path).unwrap());
        let workers: Vec<_> = (0..8)
            .map(|i| {
                let path = path.clone();
                std::thread::spawn(move || {
                    let request = IpcRequest {
                        version: PROTOCOL_VERSION,
                        request_id: format!("concurrent-{i}"),
                        method: "test.ping".into(),
                        params: serde_json::json!({}),
                        token: None,
                    };
                    let response = IpcClient::connect(&path)
                        .unwrap()
                        .request(&request)
                        .unwrap();
                    assert!(response.ok);
                    assert_eq!(
                        response.result,
                        Some(serde_json::json!({"echo": format!("concurrent-{i}")}))
                    );
                })
            })
            .collect();
        for worker in workers {
            worker.join().unwrap();
        }
        server.shutdown().unwrap();
        assert!(!path.exists());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn frame_limit_rejects_oversize_before_json_parse() {
        let root = std::env::temp_dir().join(format!("omaterm-ipc-frame-{}", std::process::id()));
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let path = root.join("omaterm.sock");
        let handler: RequestHandler =
            Arc::new(|request, _| IpcResponse::success(request.request_id, serde_json::json!({})));
        let mut server = IpcServer::bind(&path, handler).unwrap();
        let mut stream = UnixStream::connect(&path).unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(2)))
            .unwrap();
        stream.write_all(&vec![b'x'; MAX_REQUEST_FRAME]).unwrap();
        let mut result = Vec::new();
        stream.read_to_end(&mut result).unwrap();
        let response: IpcResponse = serde_json::from_slice(&result).unwrap();
        assert_eq!(response.error.unwrap().code, "invalid_request");
        server.shutdown().unwrap();
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn slowloris_frame_times_out_but_the_server_stays_healthy() {
        let root = std::env::temp_dir().join(format!("omaterm-ipc-slow-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let path = root.join("omaterm.sock");
        let handler: RequestHandler =
            Arc::new(|request, _| IpcResponse::success(request.request_id, serde_json::json!({})));
        let timeouts = ServerTimeouts {
            frame: Duration::from_millis(200),
            request: Duration::from_secs(2),
        };
        let mut server = IpcServer::bind_with_timeouts(&path, handler, timeouts).unwrap();
        // Send a partial frame and stall past the frame deadline.
        let mut slow = UnixStream::connect(&path).unwrap();
        slow.set_read_timeout(Some(Duration::from_secs(2))).unwrap();
        slow.write_all(b"{\"version\":1").unwrap();
        std::thread::sleep(Duration::from_millis(500));
        let mut drained = Vec::new();
        // The server gives up on the stalled frame: EOF without a response.
        assert_eq!(slow.read_to_end(&mut drained).unwrap(), 0);
        // The server still serves the next client.
        let request = IpcRequest {
            version: PROTOCOL_VERSION,
            request_id: "after-slowloris".into(),
            method: "test.ping".into(),
            params: serde_json::json!({}),
            token: None,
        };
        let response = IpcClient::connect(&path)
            .unwrap()
            .request(&request)
            .unwrap();
        assert!(response.ok);
        assert_eq!(response.request_id, "after-slowloris");
        server.shutdown().unwrap();
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn request_deadline_exceeded_reports_timeout_with_request_id() {
        let root =
            std::env::temp_dir().join(format!("omaterm-ipc-deadline-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let path = root.join("omaterm.sock");
        let handler: RequestHandler = Arc::new(|request, _| {
            std::thread::sleep(Duration::from_millis(500));
            IpcResponse::success(request.request_id, serde_json::json!({}))
        });
        let timeouts = ServerTimeouts {
            frame: Duration::from_secs(2),
            request: Duration::from_millis(100),
        };
        let mut server = IpcServer::bind_with_timeouts(&path, handler, timeouts).unwrap();
        let request = IpcRequest {
            version: PROTOCOL_VERSION,
            request_id: "slow-handler".into(),
            method: "test.ping".into(),
            params: serde_json::json!({}),
            token: None,
        };
        let response = IpcClient::connect(&path)
            .unwrap()
            .request(&request)
            .unwrap();
        assert!(!response.ok);
        assert_eq!(response.request_id, "slow-handler");
        assert_eq!(response.error.unwrap().code, "timeout");
        server.shutdown().unwrap();
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn connection_cap_sheds_load_without_breaking_the_server() {
        let root = std::env::temp_dir().join(format!("omaterm-ipc-cap-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let path = root.join("omaterm.sock");
        let release = Arc::new(std::sync::Barrier::new(MAX_CONNECTIONS + 1));
        let inside = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        let handler: RequestHandler = {
            let release = release.clone();
            let inside = inside.clone();
            Arc::new(move |request, _| {
                inside.fetch_add(1, std::sync::atomic::Ordering::AcqRel);
                release.wait();
                IpcResponse::success(request.request_id, serde_json::json!({}))
            })
        };
        let mut server = IpcServer::bind(&path, handler).unwrap();
        let workers: Vec<_> = (0..MAX_CONNECTIONS)
            .map(|i| {
                let path = path.clone();
                std::thread::spawn(move || {
                    let request = IpcRequest {
                        version: PROTOCOL_VERSION,
                        request_id: format!("held-{i}"),
                        method: "test.ping".into(),
                        params: serde_json::json!({}),
                        token: None,
                    };
                    IpcClient::connect(&path).unwrap().request(&request)
                })
            })
            .collect();
        // Wait until all 32 holders are inside their handlers, so every
        // connection slot is occupied before probing the cap.
        let start = Instant::now();
        while inside.load(std::sync::atomic::Ordering::Acquire) < MAX_CONNECTIONS {
            assert!(
                start.elapsed() < Duration::from_secs(5),
                "holders did not occupy the server"
            );
            std::thread::sleep(Duration::from_millis(5));
        }
        // The 33rd connection is shed: EOF without a response frame.
        let mut shed = UnixStream::connect(&path).unwrap();
        shed.set_read_timeout(Some(Duration::from_secs(2))).unwrap();
        shed.write_all(
            b"{\"version\":1,\"request_id\":\"shed\",\"method\":\"test.ping\",\"params\":{}}\n",
        )
        .unwrap();
        // The 33rd connection is shed without a response frame: the server
        // drops it unread, which surfaces as EOF or a connection reset.
        let mut drained = Vec::new();
        match shed.read_to_end(&mut drained) {
            Ok(0) => {}
            Err(error) if error.kind() == std::io::ErrorKind::ConnectionReset => {}
            other => panic!("shed connection should carry no response, got {other:?}"),
        }
        assert!(drained.is_empty());
        release.wait();
        for worker in workers {
            let response = worker.join().unwrap().unwrap();
            assert!(response.ok);
        }
        server.shutdown().unwrap();
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn peer_policy_accepts_only_the_owner_uid() {
        assert!(peer_authorized(unsafe { libc::getuid() }, unsafe {
            libc::getuid()
        }));
        assert!(!peer_authorized(unsafe { libc::getuid() } + 1, unsafe {
            libc::getuid()
        }));
    }

    #[test]
    fn validation_errors_preserve_request_id_and_malformed_frames_do_not() {
        let root = std::env::temp_dir().join(format!("omaterm-ipc-reqid-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let path = root.join("omaterm.sock");
        let handler: RequestHandler =
            Arc::new(|request, _| IpcResponse::success(request.request_id, serde_json::json!({})));
        let mut server = IpcServer::bind(&path, handler).unwrap();
        // Unsupported version on a parsed request keeps its request_id.
        let mut stream = UnixStream::connect(&path).unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(2)))
            .unwrap();
        stream
            .write_all(b"{\"version\":99,\"request_id\":\"keep-me\",\"method\":\"pane.list\",\"params\":{}}\n")
            .unwrap();
        let mut line = Vec::new();
        let mut byte = [0u8; 1];
        loop {
            stream.read_exact(&mut byte).unwrap();
            if byte[0] == b'\n' {
                break;
            }
            line.push(byte[0]);
        }
        let response: IpcResponse = serde_json::from_slice(&line).unwrap();
        assert_eq!(response.request_id, "keep-me");
        assert_eq!(response.error.unwrap().code, "unsupported_version");
        // Malformed JSON has no trustworthy request_id.
        let mut stream = UnixStream::connect(&path).unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(2)))
            .unwrap();
        stream.write_all(b"not json\n").unwrap();
        let mut line = Vec::new();
        loop {
            stream.read_exact(&mut byte).unwrap();
            if byte[0] == b'\n' {
                break;
            }
            line.push(byte[0]);
        }
        let response: IpcResponse = serde_json::from_slice(&line).unwrap();
        assert_eq!(response.request_id, "");
        assert_eq!(response.error.unwrap().code, "invalid_request");
        server.shutdown().unwrap();
        fs::remove_dir_all(root).unwrap();
    }
}
