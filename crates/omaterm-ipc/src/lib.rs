//! Bounded newline-delimited JSON transport.
//!
//! Linux keeps the owner-only Unix socket. Windows uses a named pipe whose
//! name is derived from that same filesystem path, restricted to the current
//! user. Callers still pass the path from [`IpcServer::default_socket_path`].

mod frame;

#[cfg(unix)]
#[path = "unix.rs"]
mod imp;

#[cfg(windows)]
#[path = "windows.rs"]
mod imp;

pub use imp::*;
