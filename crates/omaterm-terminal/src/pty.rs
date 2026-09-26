use std::collections::HashMap;
use std::io::{Read, Write};
use std::os::fd::{AsRawFd, RawFd};
use std::path::{Path, PathBuf};

use alacritty_terminal::event::{OnResize, WindowSize};
use alacritty_terminal::tty::{ChildEvent, EventedPty, EventedReadWrite, Options, Pty, Shell};

/// Errors from PTY spawn / I/O.
#[derive(Debug, thiserror::Error)]
pub enum PtyError {
    #[error("failed to spawn shell '{shell}': {source}")]
    Spawn {
        shell: String,
        #[source]
        source: std::io::Error,
    },
    #[error("pty I/O error: {0}")]
    Io(#[from] std::io::Error),
}

/// A real PTY master + child shell.
///
/// Built on `alacritty_terminal::tty` so controlling-terminal, session,
/// non-blocking master, `TIOCSWINSZ`, and `SIGHUP`-on-drop semantics are
/// inherited instead of re-implemented. Child-only env is passed through
/// `Options::env`; the desktop process environment is never mutated.
pub struct PtyProcess {
    pty: Pty,
    child_pid: u32,
}

impl PtyProcess {
    /// Spawn `shell` (or `$SHELL`, else `/bin/bash`) in `working_directory`.
    pub fn spawn(
        shell: Option<&str>,
        cols: u16,
        rows: u16,
        working_directory: &Path,
    ) -> Result<Self, PtyError> {
        let program = shell
            .map(str::to_string)
            .or_else(|| std::env::var("SHELL").ok())
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| "/bin/bash".to_string());

        let mut env = HashMap::new();
        // Honest capability advertisement: alacritty parser handles 256 +
        // truecolor SGR, app-cursor, and bracketed paste.
        env.insert("TERM".to_string(), "xterm-256color".to_string());
        env.insert("COLORTERM".to_string(), "truecolor".to_string());
        env.insert("TERM_PROGRAM".to_string(), "OmaTerm".to_string());
        env.insert("OMATERM".to_string(), "1".to_string());

        let options = Options {
            shell: Some(Shell::new(program.clone(), Vec::new())),
            working_directory: Some(working_directory.to_path_buf()),
            drain_on_exit: true,
            env,
        };
        let window_size = WindowSize {
            num_lines: rows.max(1),
            num_cols: cols.max(2),
            cell_width: 0,
            cell_height: 0,
        };
        // Window ID is only used for ALACRITTY_WINDOW_ID/WINDOWID env seeding.
        let pty = alacritty_terminal::tty::new(&options, window_size, 0).map_err(|source| {
            PtyError::Spawn {
                shell: program,
                source,
            }
        })?;
        let child_pid = pty.child().id();
        Ok(Self { pty, child_pid })
    }

    /// Non-blocking read. Returns `WouldBlock` when no output is available.
    pub fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        self.pty.reader().read(buf)
    }

    /// Write bytes (keyboard input + engine replies). Loops on partial writes.
    pub fn write_all(&mut self, mut bytes: &[u8]) -> std::io::Result<()> {
        while !bytes.is_empty() {
            match self.pty.writer().write(bytes) {
                Ok(0) => {
                    return Err(std::io::Error::new(
                        std::io::ErrorKind::WriteZero,
                        "pty master wrote zero bytes",
                    ));
                }
                Ok(n) => bytes = &bytes[n..],
                Err(e) if e.kind() == std::io::ErrorKind::Interrupted => continue,
                Err(e) => return Err(e),
            }
        }
        Ok(())
    }

    /// Resize the kernel PTY (`TIOCSWINSZ` + `SIGWINCH` to the child).
    pub fn resize(&mut self, cols: u16, rows: u16) {
        self.pty.on_resize(WindowSize {
            num_lines: rows.max(1),
            num_cols: cols.max(2),
            cell_width: 0,
            cell_height: 0,
        });
    }

    /// Non-blocking child-exit check. `None` = still running.
    pub fn try_child_event(&mut self) -> Option<ChildEvent> {
        self.pty.next_child_event()
    }

    /// Ask the child to hang up, mirroring what `Drop` does on close.
    /// Closing a terminal view calls this first so shutdown is explicit and
    /// testable rather than implicit in the drop path.
    pub fn terminate(&self) -> std::io::Result<()> {
        // SAFETY: `kill` with a signal number touches no memory.
        let result = unsafe { libc::kill(self.child_pid as libc::pid_t, libc::SIGHUP) };
        if result != 0 {
            return Err(std::io::Error::last_os_error());
        }
        Ok(())
    }

    pub fn child_pid(&self) -> u32 {
        self.child_pid
    }

    /// Best-effort Linux CWD discovery via `/proc/<pid>/cwd`.
    pub fn child_cwd(&self) -> Option<PathBuf> {
        let link = format!("/proc/{}/cwd", self.child_pid);
        std::fs::read_link(link).ok()
    }

    /// Raw master FD for `poll()`-based waiting. Stable for the session lifetime.
    pub fn as_raw_fd(&self) -> RawFd {
        self.pty.file().as_raw_fd()
    }

    /// Block up to `timeout_ms` until PTY output (or hangup/error) is
    /// available. Returns `true` when a `read()` is worthwhile. This keeps an
    /// idle terminal near zero CPU: the reader thread sleeps in the kernel
    /// instead of spin-polling.
    pub fn poll_readable(&self, timeout_ms: i32) -> std::io::Result<bool> {
        poll_fd_readable(self.as_raw_fd(), timeout_ms)
    }
}

/// Block up to `timeout_ms` until `fd` is readable (or hung up / errored).
/// Lock-free helper so the reader thread can wait without holding the
/// session lock.
pub fn poll_fd_readable(fd: RawFd, timeout_ms: i32) -> std::io::Result<bool> {
    let mut poll_fd = libc::pollfd {
        fd,
        events: libc::POLLIN | libc::POLLERR | libc::POLLHUP,
        revents: 0,
    };
    // SAFETY: poll_fd points to one valid pollfd; timeout is milliseconds.
    let ready = unsafe { libc::poll(&mut poll_fd, 1, timeout_ms) };
    if ready < 0 {
        return Err(std::io::Error::last_os_error());
    }
    Ok(ready > 0)
}

impl Drop for PtyProcess {
    fn drop(&mut self) {
        // `Pty::drop` sends SIGHUP and reaps the child. Nothing extra needed;
        // this impl exists to document the guarantee.
    }
}
