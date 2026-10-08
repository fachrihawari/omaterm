//! Per-user named pipe transport. The public path stays a filesystem path so
//! the credential file beside it is unchanged; both ends derive the pipe name
//! from that path.

use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write};
use std::os::windows::ffi::OsStrExt;
use std::os::windows::fs::MetadataExt;
use std::os::windows::io::{AsRawHandle, FromRawHandle};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use omaterm_protocol::{
    IpcRequest, IpcResponse, MAX_CONNECTIONS, MAX_REQUEST_FRAME, MAX_RESPONSE_FRAME,
    encode_response, validate_request,
};
use windows_sys::Win32::Foundation::{
    ERROR_PIPE_CONNECTED, ERROR_PIPE_LISTENING, FALSE, HANDLE, INVALID_HANDLE_VALUE, LocalFree,
};
use windows_sys::Win32::Security::Authorization::{
    ConvertSidToStringSidW, ConvertStringSecurityDescriptorToSecurityDescriptorW,
};
use windows_sys::Win32::Security::{
    GetTokenInformation, SECURITY_ATTRIBUTES, TOKEN_QUERY, TOKEN_USER, TokenUser,
};
use windows_sys::Win32::Storage::FileSystem::{
    LOCKFILE_EXCLUSIVE_LOCK, LOCKFILE_FAIL_IMMEDIATELY, LockFileEx, PIPE_ACCESS_DUPLEX,
};
use windows_sys::Win32::System::IO::OVERLAPPED;
use windows_sys::Win32::System::Pipes::{
    ConnectNamedPipe, CreateNamedPipeW, DisconnectNamedPipe, PIPE_READMODE_BYTE,
    PIPE_REJECT_REMOTE_CLIENTS, PIPE_TYPE_BYTE, PIPE_UNLIMITED_INSTANCES, PIPE_WAIT, PeekNamedPipe,
    SetNamedPipeHandleState,
};
use windows_sys::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};

const FILE_ATTRIBUTE_REPARSE_POINT: u32 = 0x400;
const PIPE_NOWAIT: u32 = 0x00000001;

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

pub struct IpcServer {
    path: PathBuf,
    stopping: Arc<AtomicBool>,
    accept_thread: Option<JoinHandle<()>>,
    _lock: File,
}

impl IpcServer {
    pub fn default_socket_path() -> Result<PathBuf, IpcError> {
        let base = std::env::var_os("LOCALAPPDATA")
            .map(PathBuf::from)
            .or_else(|| {
                std::env::var_os("USERPROFILE")
                    .map(|profile| PathBuf::from(profile).join("AppData").join("Local"))
            })
            .ok_or_else(|| IpcError::UnsafeDirectory(PathBuf::from("%LOCALAPPDATA%\\omaterm")))?;
        let directory = base.join("omaterm");
        fs::create_dir_all(&directory)?;
        validate_private_directory(&directory)?;
        Ok(directory.join("omaterm.sock"))
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
        let lock = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .open(&lock_path)?;
        // LockFileEx reads the lock offset from OVERLAPPED even for a
        // synchronous file. A null pointer is a read at address 0x10.
        let mut overlapped = unsafe { std::mem::zeroed::<OVERLAPPED>() };
        // SAFETY: the lock file is open, `overlapped` is zeroed and live for
        // this synchronous call, and its event handle is null. A held lock
        // means another desktop owns this endpoint.
        let locked = unsafe {
            LockFileEx(
                lock.as_raw_handle() as HANDLE,
                LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY,
                0,
                1,
                0,
                &mut overlapped,
            )
        };
        if locked == 0 {
            return Err(IpcError::EndpointExists(path));
        }
        let stopping = Arc::new(AtomicBool::new(false));
        let accept_stopping = stopping.clone();
        let pipe_path = path.clone();
        let accept_thread = thread::Builder::new()
            .name("omaterm-ipc-accept".into())
            .spawn(move || accept_loop(&pipe_path, handler, accept_stopping, timeouts))
            .map_err(IpcError::Io)?;
        Ok(Self {
            path,
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
        Ok(())
    }
}

impl Drop for IpcServer {
    fn drop(&mut self) {
        self.stopping.store(true, Ordering::Release);
        if let Some(thread) = self.accept_thread.take() {
            let _ = thread.join();
        }
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
    if parent.as_os_str().is_empty() {
        return Err(IpcError::UnsafeDirectory(path.to_path_buf()));
    }
    validate_private_directory(parent)
}

fn validate_private_directory(path: &Path) -> Result<(), IpcError> {
    let metadata =
        fs::symlink_metadata(path).map_err(|_| IpcError::UnsafeDirectory(path.to_path_buf()))?;
    if !metadata.is_dir() || metadata.file_attributes() & FILE_ATTRIBUTE_REPARSE_POINT != 0 {
        return Err(IpcError::UnsafeDirectory(path.to_path_buf()));
    }
    Ok(())
}

fn accept_loop(
    path: &Path,
    handler: RequestHandler,
    stopping: Arc<AtomicBool>,
    timeouts: ServerTimeouts,
) {
    let active = Arc::new(AtomicUsize::new(0));
    let mut workers: Vec<JoinHandle<()>> = Vec::new();
    while !stopping.load(Ordering::Acquire) {
        reap_workers(&mut workers);
        if active.load(Ordering::Acquire) >= MAX_CONNECTIONS {
            thread::sleep(Duration::from_millis(10));
            continue;
        }
        let pipe = match create_pipe(path) {
            Ok(pipe) => pipe,
            Err(_) => break,
        };
        let connected = wait_for_client(&pipe, &stopping);
        if !connected {
            drop(pipe);
            break;
        }
        if active.fetch_add(1, Ordering::AcqRel) >= MAX_CONNECTIONS {
            active.fetch_sub(1, Ordering::AcqRel);
            drop(pipe);
            continue;
        }
        let active = active.clone();
        let handler = handler.clone();
        let worker_stopping = stopping.clone();
        workers.push(thread::spawn(move || {
            let _guard = ConnectionGuard(active);
            let _ = handle_connection(pipe, handler, worker_stopping, timeouts);
        }));
    }
    for worker in workers {
        let _ = worker.join();
    }
}

fn reap_workers(workers: &mut Vec<JoinHandle<()>>) {
    let mut index = 0;
    while index < workers.len() {
        if workers[index].is_finished() {
            let worker = workers.swap_remove(index);
            let _ = worker.join();
        } else {
            index += 1;
        }
    }
}

fn wait_for_client(pipe: &File, stopping: &AtomicBool) -> bool {
    loop {
        if stopping.load(Ordering::Acquire) {
            return false;
        }
        let handle = pipe.as_raw_handle() as HANDLE;
        // SAFETY: handle is the pipe this thread owns until the function returns.
        let connected = unsafe { ConnectNamedPipe(handle, std::ptr::null_mut()) };
        if connected != 0 {
            switch_to_blocking(handle);
            return true;
        }
        let error = std::io::Error::last_os_error();
        match error.raw_os_error().map(|code| code as u32) {
            Some(ERROR_PIPE_CONNECTED) => {
                switch_to_blocking(handle);
                return true;
            }
            Some(ERROR_PIPE_LISTENING) => thread::sleep(Duration::from_millis(10)),
            _ => return false,
        }
    }
}

fn switch_to_blocking(handle: HANDLE) {
    let mut mode = PIPE_WAIT | PIPE_READMODE_BYTE;
    // SAFETY: mode is a valid pipe-state flag for this live pipe handle.
    unsafe {
        SetNamedPipeHandleState(
            handle,
            &mut mode,
            std::ptr::null_mut(),
            std::ptr::null_mut(),
        );
    }
}

fn create_pipe(path: &Path) -> std::io::Result<File> {
    let name = pipe_name(path);
    let descriptor = user_security_descriptor()?;
    let mut attributes = SECURITY_ATTRIBUTES {
        nLength: std::mem::size_of::<SECURITY_ATTRIBUTES>() as u32,
        lpSecurityDescriptor: descriptor,
        bInheritHandle: FALSE,
    };
    // SAFETY: the name is NUL-terminated and the descriptor is a valid SD
    // freed immediately after the call copies it into the pipe.
    let handle = unsafe {
        CreateNamedPipeW(
            name.as_ptr(),
            PIPE_ACCESS_DUPLEX,
            PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_NOWAIT | PIPE_REJECT_REMOTE_CLIENTS,
            PIPE_UNLIMITED_INSTANCES,
            64 * 1024,
            64 * 1024,
            0,
            &mut attributes,
        )
    };
    unsafe {
        LocalFree(descriptor as _);
    }
    if handle == INVALID_HANDLE_VALUE {
        return Err(std::io::Error::last_os_error());
    }
    // SAFETY: CreateNamedPipeW returned a fresh owned handle.
    Ok(unsafe { File::from_raw_handle(handle as _) })
}

fn pipe_name(path: &Path) -> Vec<u16> {
    let mut hash = 0xcbf29ce484222325u64;
    for unit in path.as_os_str().encode_wide() {
        hash ^= u64::from(unit);
        hash = hash.wrapping_mul(0x100000001b3);
    }
    let name = format!(r"\\.\pipe\omaterm-{hash:016x}");
    std::ffi::OsStr::new(&name)
        .encode_wide()
        .chain(std::iter::once(0))
        .collect()
}

fn user_security_descriptor() -> std::io::Result<*mut core::ffi::c_void> {
    unsafe {
        let mut token: HANDLE = std::ptr::null_mut();
        if OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token) == 0 {
            return Err(std::io::Error::last_os_error());
        }
        let mut needed = 0u32;
        GetTokenInformation(token, TokenUser, std::ptr::null_mut(), 0, &mut needed);
        let words = (needed as usize)
            .div_ceil(std::mem::size_of::<u64>())
            .max(1);
        let mut buffer = vec![0u64; words];
        let ok = GetTokenInformation(
            token,
            TokenUser,
            buffer.as_mut_ptr().cast(),
            needed,
            &mut needed,
        );
        windows_sys::Win32::Foundation::CloseHandle(token);
        if ok == 0 {
            return Err(std::io::Error::last_os_error());
        }
        let user = &*buffer.as_ptr().cast::<TOKEN_USER>();
        let mut sid = std::ptr::null_mut();
        if ConvertSidToStringSidW(user.User.Sid, &mut sid) == 0 {
            return Err(std::io::Error::last_os_error());
        }
        let sid_text = wide_to_string(sid);
        LocalFree(sid as _);
        let sddl: Vec<u16> = std::ffi::OsStr::new(&format!("D:P(A;;GA;;;{sid_text})"))
            .encode_wide()
            .chain(std::iter::once(0))
            .collect();
        let mut descriptor = std::ptr::null_mut();
        if ConvertStringSecurityDescriptorToSecurityDescriptorW(
            sddl.as_ptr(),
            1,
            &mut descriptor,
            std::ptr::null_mut(),
        ) == 0
        {
            return Err(std::io::Error::last_os_error());
        }
        Ok(descriptor)
    }
}

fn wide_to_string(ptr: *const u16) -> String {
    let mut len = 0usize;
    while unsafe { *ptr.add(len) } != 0 {
        len += 1;
    }
    String::from_utf16_lossy(unsafe { std::slice::from_raw_parts(ptr, len) })
}

struct PipeStream {
    file: File,
}

impl PipeStream {
    fn read_timeout(&mut self, buf: &mut [u8], timeout: Duration) -> std::io::Result<usize> {
        let deadline = Instant::now() + timeout;
        loop {
            let mut available = 0u32;
            // SAFETY: the handle belongs to this stream and the output pointer is valid.
            let peeked = unsafe {
                PeekNamedPipe(
                    self.file.as_raw_handle() as HANDLE,
                    std::ptr::null_mut(),
                    0,
                    std::ptr::null_mut(),
                    &mut available,
                    std::ptr::null_mut(),
                )
            };
            if peeked == 0 {
                return Err(std::io::Error::last_os_error());
            }
            if available > 0 {
                return self.file.read(buf);
            }
            if Instant::now() >= deadline {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::TimedOut,
                    "named pipe read timed out",
                ));
            }
            thread::sleep(Duration::from_millis(10));
        }
    }
}

impl Drop for PipeStream {
    fn drop(&mut self) {
        // SAFETY: disconnecting a pipe this stream owns wakes the peer with EOF.
        unsafe {
            DisconnectNamedPipe(self.file.as_raw_handle() as HANDLE);
        }
    }
}

fn handle_connection(
    file: File,
    handler: RequestHandler,
    stopping: Arc<AtomicBool>,
    timeouts: ServerTimeouts,
) -> std::io::Result<()> {
    let mut stream = PipeStream { file };
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
            match stream.read_timeout(&mut byte, remaining.min(Duration::from_millis(200))) {
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
                Err(error) if error.kind() == std::io::ErrorKind::TimedOut => continue,
                Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
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
            write_response(
                &mut stream,
                &IpcResponse::failure(response.request_id, "timeout", "request deadline exceeded"),
            )?;
            return Ok(());
        }
        write_response(&mut stream, &response)?;
    }
}

fn write_response(stream: &mut PipeStream, response: &IpcResponse) -> std::io::Result<()> {
    match encode_response(response) {
        Ok(frame) => stream.file.write_all(&frame),
        Err(_) => {
            let frame = encode_response(&IpcResponse::failure(
                response.request_id.clone(),
                "response_too_large",
                "response exceeds the 1 MiB frame limit",
            ))
            .unwrap_or_else(|_| b"{}\n".to_vec());
            stream.file.write_all(&frame)
        }
    }
}

pub struct IpcClient {
    stream: PipeStream,
}

impl IpcClient {
    pub fn connect(path: impl AsRef<Path>) -> std::io::Result<Self> {
        let name = pipe_name(path.as_ref());
        let mut last_error = std::io::Error::from_raw_os_error(2);
        for _ in 0..50 {
            // SAFETY: the name is NUL-terminated. The returned handle is owned.
            let handle = unsafe {
                windows_sys::Win32::Storage::FileSystem::CreateFileW(
                    name.as_ptr(),
                    windows_sys::Win32::Storage::FileSystem::FILE_GENERIC_READ
                        | windows_sys::Win32::Storage::FileSystem::FILE_GENERIC_WRITE,
                    0,
                    std::ptr::null(),
                    windows_sys::Win32::Storage::FileSystem::OPEN_EXISTING,
                    0,
                    std::ptr::null_mut(),
                )
            };
            if handle != INVALID_HANDLE_VALUE {
                return Ok(Self {
                    stream: PipeStream {
                        file: unsafe { File::from_raw_handle(handle as _) },
                    },
                });
            }
            last_error = std::io::Error::last_os_error();
            thread::sleep(Duration::from_millis(10));
        }
        Err(last_error)
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
        self.stream.file.write_all(&bytes)?;
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
            if self.stream.read_timeout(&mut byte, remaining)? == 0 {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::UnexpectedEof,
                    "IPC connection closed",
                ));
            }
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
    fn client_server_round_trip() {
        let root = std::env::temp_dir().join(format!("omaterm-ipc-win-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        let path = root.join("omaterm.sock");
        let handler: RequestHandler = Arc::new(|request, _deadline| {
            IpcResponse::success(request.request_id, serde_json::json!({"pong": true}))
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
        assert_eq!(response.result, Some(serde_json::json!({"pong": true})));
        server.shutdown().unwrap();
        let _ = fs::remove_dir_all(root);
    }
}
