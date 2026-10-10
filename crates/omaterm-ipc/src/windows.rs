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
};
use windows_sys::Win32::Foundation::{
    ERROR_PIPE_CONNECTED, ERROR_PIPE_LISTENING, FALSE, HANDLE, INVALID_HANDLE_VALUE, LocalFree,
};
use windows_sys::Win32::Security::Authorization::{
    ConvertSidToStringSidW, ConvertStringSecurityDescriptorToSecurityDescriptorW,
    ConvertStringSidToSidW, GetNamedSecurityInfoW, SE_FILE_OBJECT, SetNamedSecurityInfoW,
};
use windows_sys::Win32::Security::{
    ACE_HEADER, ACL, ACL_SIZE_INFORMATION, AclSizeInformation, DACL_SECURITY_INFORMATION, EqualSid,
    GetAce, GetAclInformation, GetSecurityDescriptorDacl, GetTokenInformation,
    OWNER_SECURITY_INFORMATION, PROTECTED_DACL_SECURITY_INFORMATION, PSID, SECURITY_ATTRIBUTES,
    TOKEN_QUERY, TOKEN_USER, TokenUser,
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

use crate::frame::FrameIo;

const FILE_ATTRIBUTE_REPARSE_POINT: u32 = 0x400;
const PIPE_NOWAIT: u32 = 0x00000001;
/// Polled wait while a pipe instance is not yet connected, and the pause
/// after a failed `CreateNamedPipe`. Short enough that shutdown stays
/// responsive, long enough that a failed create does not spin.
const PIPE_ACCEPT_RETRY: Duration = Duration::from_millis(50);
/// `ERROR_PIPE_BUSY` is normal while the server replaces an instance.
/// Fifty attempts at 10 ms covers half a second of that race.
const PIPE_CONNECT_ATTEMPTS: u32 = 50;
const PIPE_CONNECT_INTERVAL: Duration = Duration::from_millis(10);

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
        // The profile directory inherits Administrators and SYSTEM. Tighten it
        // before the owner check, or the default endpoint can never bind.
        restrict_to_current_user(&directory)?;
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
            .truncate(false)
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
    match private_to_current_user(path) {
        Ok(true) => Ok(()),
        _ => Err(IpcError::UnsafeDirectory(path.to_path_buf())),
    }
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
            // One transient failure must not kill the server. Shutdown is
            // observed on the next loop, so this sleep cannot outlive it.
            Err(_) => {
                thread::sleep(PIPE_ACCEPT_RETRY);
                continue;
            }
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
            // The pipe is non-blocking, so ConnectNamedPipe returns at once
            // and this wait is polled. Shutdown is checked on every iteration
            // instead of sitting inside a blocking connect.
            Some(ERROR_PIPE_LISTENING) => thread::sleep(PIPE_ACCEPT_RETRY),
            _ => return false,
        }
    }
}

fn switch_to_blocking(handle: HANDLE) {
    let mode = PIPE_WAIT | PIPE_READMODE_BYTE;
    // SAFETY: mode is a valid pipe-state flag for this live pipe handle.
    unsafe {
        SetNamedPipeHandleState(handle, &mode, std::ptr::null_mut(), std::ptr::null_mut());
    }
}

fn create_pipe(path: &Path) -> std::io::Result<File> {
    let name = pipe_name(path);
    let descriptor = user_security_descriptor()?;
    let attributes = SECURITY_ATTRIBUTES {
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
            &attributes,
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

/// Replace the DACL with one allow ACE for the current user and make that
/// user the owner. Directories also inherit the ACE onto new children, which
/// is the Windows equivalent of creating files `0600` inside a `0700` directory.
/// A reparse point is left untouched.
pub fn restrict_to_current_user(path: &Path) -> std::io::Result<()> {
    let metadata = fs::symlink_metadata(path)?;
    if metadata.file_attributes() & FILE_ATTRIBUTE_REPARSE_POINT != 0 {
        return Err(std::io::Error::new(
            std::io::ErrorKind::PermissionDenied,
            "refusing to change the ACL of a reparse point",
        ));
    }
    let sid_text = current_user_sid_string()?;
    let inherit = if metadata.is_dir() { "OICI" } else { "" };
    let sddl = format!("D:P(A;{inherit};FA;;;{sid_text})");
    let descriptor = security_descriptor(&sddl)?;
    let dacl = descriptor_dacl(descriptor.0)?;
    let owner = string_sid(&sid_text)?;
    let mut wide = wide_path(path);
    // SAFETY: `wide` is a mutable NUL-terminated path, `owner.0` is a SID this
    // function frees later, and `dacl` lives inside `descriptor` until
    // SetNamedSecurityInfoW copies it. The API may write a NUL into `wide`.
    let status = unsafe {
        SetNamedSecurityInfoW(
            wide.as_mut_ptr(),
            SE_FILE_OBJECT,
            OWNER_SECURITY_INFORMATION
                | DACL_SECURITY_INFORMATION
                | PROTECTED_DACL_SECURITY_INFORMATION,
            owner.0,
            std::ptr::null_mut(),
            dacl,
            std::ptr::null(),
        )
    };
    if status != 0 {
        return Err(std::io::Error::from_raw_os_error(status as i32));
    }
    Ok(())
}

/// True when `path` is not a reparse point, is owned by the current user, and
/// every allow ACE names that user or LocalSystem. A null DACL, Everyone,
/// Users, or Administrators allow fails closed. This is the named-pipe
/// stand-in for the Unix owner-plus-`0700` check.
pub fn private_to_current_user(path: &Path) -> std::io::Result<bool> {
    let metadata = fs::symlink_metadata(path)?;
    if metadata.file_attributes() & FILE_ATTRIBUTE_REPARSE_POINT != 0 {
        return Ok(false);
    }
    let user = string_sid(&current_user_sid_string()?)?;
    let system = string_sid("S-1-5-18")?;
    let mut wide = wide_path(path);
    let mut owner = std::ptr::null_mut();
    let mut dacl: *mut ACL = std::ptr::null_mut();
    let mut descriptor = std::ptr::null_mut();
    // SAFETY: `wide` is NUL-terminated. On success, `owner` and `dacl` point
    // into `descriptor`, which `LocalFree` releases below.
    let status = unsafe {
        GetNamedSecurityInfoW(
            wide.as_mut_ptr(),
            SE_FILE_OBJECT,
            OWNER_SECURITY_INFORMATION | DACL_SECURITY_INFORMATION,
            &mut owner,
            std::ptr::null_mut(),
            &mut dacl,
            std::ptr::null_mut(),
            &mut descriptor,
        )
    };
    let _descriptor = FreeLocal(descriptor);
    if status != 0 {
        return Err(std::io::Error::from_raw_os_error(status as i32));
    }
    if owner.is_null() || dacl.is_null() || unsafe { EqualSid(owner, user.0) } == 0 {
        return Ok(false);
    }
    let mut size = ACL_SIZE_INFORMATION {
        AceCount: 0,
        AclBytesInUse: 0,
        AclBytesFree: 0,
    };
    // SAFETY: `dacl` is the ACL returned above and `size` is a live out-param.
    let ok = unsafe {
        GetAclInformation(
            dacl,
            (&mut size as *mut ACL_SIZE_INFORMATION).cast(),
            std::mem::size_of::<ACL_SIZE_INFORMATION>() as u32,
            AclSizeInformation,
        )
    };
    if ok == 0 {
        return Err(std::io::Error::last_os_error());
    }
    let mut user_allowed = false;
    for index in 0..size.AceCount {
        let mut ace = std::ptr::null_mut();
        // SAFETY: `index` is inside the ACE count just read.
        if unsafe { GetAce(dacl, index, &mut ace) } == 0 {
            return Err(std::io::Error::last_os_error());
        }
        // SAFETY: GetAce returns a pointer to an ACE_HEADER at the start of the ACE.
        let header = unsafe { &*ace.cast::<ACE_HEADER>() };
        match header.AceType {
            1 => {}
            0 => {
                // ACCESS_ALLOWED_ACE: header (4) + mask (4) + SID. Reject an
                // ACE whose declared SID does not fit, instead of reading past it.
                let sid = allow_ace_sid(ace, header.AceSize);
                let Some(sid) = sid else {
                    return Ok(false);
                };
                if unsafe { EqualSid(sid, user.0) } != 0 {
                    user_allowed = true;
                } else if unsafe { EqualSid(sid, system.0) } == 0 {
                    return Ok(false);
                }
            }
            _ => return Ok(false),
        }
    }
    Ok(user_allowed)
}

struct FreeLocal(*mut core::ffi::c_void);

impl Drop for FreeLocal {
    fn drop(&mut self) {
        if !self.0.is_null() {
            // SAFETY: the pointer came from a LocalAlloc-family API.
            unsafe { LocalFree(self.0) };
        }
    }
}

fn current_user_sid_string() -> std::io::Result<String> {
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
        Ok(sid_text)
    }
}

fn string_sid(text: &str) -> std::io::Result<FreeLocal> {
    let wide: Vec<u16> = std::ffi::OsStr::new(text)
        .encode_wide()
        .chain(std::iter::once(0))
        .collect();
    let mut sid = std::ptr::null_mut();
    // SAFETY: `wide` is NUL-terminated. On success the SID is a LocalAlloc block.
    if unsafe { ConvertStringSidToSidW(wide.as_ptr(), &mut sid) } == 0 {
        return Err(std::io::Error::last_os_error());
    }
    Ok(FreeLocal(sid))
}

fn security_descriptor(sddl: &str) -> std::io::Result<FreeLocal> {
    let wide: Vec<u16> = std::ffi::OsStr::new(sddl)
        .encode_wide()
        .chain(std::iter::once(0))
        .collect();
    let mut descriptor = std::ptr::null_mut();
    // SAFETY: `wide` is NUL-terminated SDDL. Revision 1 is SDDL_REVISION_1.
    if unsafe {
        ConvertStringSecurityDescriptorToSecurityDescriptorW(
            wide.as_ptr(),
            1,
            &mut descriptor,
            std::ptr::null_mut(),
        )
    } == 0
    {
        return Err(std::io::Error::last_os_error());
    }
    Ok(FreeLocal(descriptor))
}

fn descriptor_dacl(descriptor: *mut core::ffi::c_void) -> std::io::Result<*mut ACL> {
    let mut present = FALSE;
    let mut defaulted = FALSE;
    let mut dacl: *mut ACL = std::ptr::null_mut();
    // SAFETY: `descriptor` is a security descriptor this caller owns.
    let ok =
        unsafe { GetSecurityDescriptorDacl(descriptor, &mut present, &mut dacl, &mut defaulted) };
    if ok == 0 || present == FALSE || dacl.is_null() {
        return Err(std::io::Error::other(
            "security descriptor has no DACL to apply",
        ));
    }
    Ok(dacl)
}

/// SID inside an `ACCESS_ALLOWED_ACE`, or `None` when `ace_size` is too small
/// for the sub-authority count stored in that SID.
fn allow_ace_sid(ace: *mut core::ffi::c_void, ace_size: u16) -> Option<PSID> {
    let ace_size = usize::from(ace_size);
    if ace_size < 16 {
        return None;
    }
    // SAFETY: GetAce returned this pointer and `ace_size` is the ACE length.
    // Only the two SID header bytes are read before the length check.
    let sid_bytes = unsafe { std::slice::from_raw_parts(ace.cast::<u8>().add(8), ace_size - 8) };
    let needed = 8 + usize::from(sid_bytes[1]) * 4;
    if needed > sid_bytes.len() {
        return None;
    }
    Some(sid_bytes.as_ptr().cast_mut().cast())
}

fn wide_path(path: &Path) -> Vec<u16> {
    path.as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect()
}

fn user_security_descriptor() -> std::io::Result<*mut core::ffi::c_void> {
    let sid_text = current_user_sid_string()?;
    let descriptor = security_descriptor(&format!("D:P(A;;GA;;;{sid_text})"))?;
    Ok(std::mem::ManuallyDrop::new(descriptor).0)
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
    crate::frame::serve_requests(
        &mut stream,
        handler.as_ref(),
        &stopping,
        timeouts.frame,
        timeouts.request,
    )
}

impl crate::frame::FrameIo for PipeStream {
    fn read_with_timeout(&mut self, buf: &mut [u8], timeout: Duration) -> std::io::Result<usize> {
        self.read_timeout(buf, timeout)
    }

    fn write_all_with_timeout(&mut self, buf: &[u8], timeout: Duration) -> std::io::Result<()> {
        // A byte-mode pipe write blocks in the kernel until the peer drains
        // it, so an unbounded write lets a wedged client wedge this worker
        // (and, transitively, server shutdown, which joins workers). Bound
        // it with a helper thread: response frames are already capped at
        // MAX_RESPONSE_FRAME, so the copy is bounded.
        let mut writer = self.file.try_clone()?;
        let pending: Vec<u8> = buf.to_vec();
        let (done_tx, done_rx) = std::sync::mpsc::channel();
        std::thread::spawn(move || {
            let result = writer.write_all(&pending);
            let _ = done_tx.send(result);
        });
        match done_rx.recv_timeout(timeout) {
            Ok(result) => result,
            Err(_) => Err(std::io::Error::new(
                std::io::ErrorKind::TimedOut,
                "named pipe write timed out",
            )),
        }
        // On timeout the writer thread is detached; dropping this stream
        // (DisconnectNamedPipe) wakes its blocked write with a pipe error.
        // serve_requests treats the timeout as a dead connection and closes
        // it, freeing the worker slot.
    }
}

pub struct IpcClient {
    stream: PipeStream,
}

impl IpcClient {
    pub fn connect(path: impl AsRef<Path>) -> std::io::Result<Self> {
        let name = pipe_name(path.as_ref());
        let mut last_error = std::io::Error::from_raw_os_error(2);
        for _ in 0..PIPE_CONNECT_ATTEMPTS {
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
            thread::sleep(PIPE_CONNECT_INTERVAL);
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
        self.stream
            .write_all_with_timeout(&bytes, Duration::from_secs(10))?;
        let frame =
            crate::frame::read_delimited_frame(&mut self.stream, deadline, MAX_RESPONSE_FRAME)?;
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
        restrict_to_current_user(&root).unwrap();
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

    #[test]
    fn directory_is_private_only_after_restrict() {
        let root = std::env::temp_dir().join(format!("omaterm-ipc-acl-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        assert!(
            validate_private_directory(&root).is_err(),
            "a new directory still inherits a shared ACL"
        );
        restrict_to_current_user(&root).unwrap();
        validate_private_directory(&root).unwrap();
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn fresh_file_is_private_only_after_restrict() {
        let root = std::env::temp_dir().join(format!("omaterm-ipc-file-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root).unwrap();
        let file = root.join("omaterm.credential");
        fs::write(&file, b"secret").unwrap();
        assert!(!private_to_current_user(&file).unwrap());
        restrict_to_current_user(&file).unwrap();
        assert!(private_to_current_user(&file).unwrap());
        let _ = fs::remove_dir_all(root);
    }
}
