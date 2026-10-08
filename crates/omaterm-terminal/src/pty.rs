use std::collections::HashMap;
use std::fs::OpenOptions;
use std::io::{Read, Write};
use std::os::fd::{AsRawFd, RawFd};
use std::os::unix::fs::OpenOptionsExt;
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
    supports_run: bool,
    bash_rc_file: Option<PathBuf>,
}

impl PtyProcess {
    /// Spawn `shell` (or `$SHELL`, else `/bin/bash`) in `working_directory`.
    pub fn spawn(
        shell: Option<&str>,
        cols: u16,
        rows: u16,
        working_directory: &Path,
        prompt_token: &str,
    ) -> Result<Self, PtyError> {
        Self::spawn_with_env(
            shell,
            cols,
            rows,
            working_directory,
            prompt_token,
            HashMap::new(),
        )
    }

    pub fn spawn_with_env(
        shell: Option<&str>,
        cols: u16,
        rows: u16,
        working_directory: &Path,
        prompt_token: &str,
        child_env: HashMap<String, String>,
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
        // The child inherits OmaTerm's own launch environment, so a
        // `COLORFGBG` set by the host terminal (e.g. a light-themed
        // launcher) would leak in and mislead apps that read it before
        // querying OSC 10/11. OmaTerm's pane is dark; advertise that.
        env.insert("COLORFGBG".to_string(), "15;0".to_string());
        env.extend(child_env);

        let supports_run = crate::shell::is_bash(&program);
        let bash_rc_file = if supports_run {
            Some(write_bash_rcfile(prompt_token).map_err(PtyError::Io)?)
        } else {
            None
        };
        let shell_args = bash_rc_file
            .as_ref()
            .map(|path| vec!["--rcfile".into(), path.to_string_lossy().into_owned()])
            .unwrap_or_default();

        let options = Options {
            shell: Some(Shell::new(program.clone(), shell_args)),
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
        let pty = match alacritty_terminal::tty::new(&options, window_size, 0) {
            Ok(pty) => pty,
            Err(source) => {
                if let Some(path) = &bash_rc_file {
                    let _ = std::fs::remove_file(path);
                }
                return Err(PtyError::Spawn {
                    shell: program,
                    source,
                });
            }
        };
        let child_pid = pty.child().id();
        Ok(Self {
            pty,
            child_pid,
            supports_run,
            bash_rc_file,
        })
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

    /// Escalate only while this PID is still our unreaped child. `WNOWAIT`
    /// leaves reaping to Alacritty's `Child`, avoiding PID reuse and a second
    /// owner of the child's exit status.
    pub fn terminate_force(&self) {
        if !self.child_exit_ready() {
            // SAFETY: the unreaped child retains its PID; kill touches no memory.
            unsafe { libc::kill(self.child_pid as libc::pid_t, libc::SIGKILL) };
        }
    }

    fn child_exit_ready(&self) -> bool {
        // SAFETY: zero initialization is valid for siginfo_t and waitid fills
        // the valid pointer. WNOWAIT observes exit without reaping the child.
        let mut info: libc::siginfo_t = unsafe { std::mem::zeroed() };
        let result = unsafe {
            libc::waitid(
                libc::P_PID,
                self.child_pid,
                &mut info,
                libc::WEXITED | libc::WNOHANG | libc::WNOWAIT,
            )
        };
        if result == 0 {
            // SAFETY: waitid initializes the SIGCHLD payload; zero PID means
            // the child has not exited yet under WNOHANG.
            return unsafe { info.si_pid() } != 0;
        }
        std::io::Error::last_os_error().raw_os_error() == Some(libc::ECHILD)
    }

    pub fn child_pid(&self) -> u32 {
        self.child_pid
    }

    pub fn supports_run(&self) -> bool {
        self.supports_run
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
        let _ = self.terminate();
        let deadline = std::time::Instant::now() + std::time::Duration::from_millis(250);
        while !self.child_exit_ready() && std::time::Instant::now() < deadline {
            std::thread::sleep(std::time::Duration::from_millis(5));
        }
        self.terminate_force();
        if let Some(path) = self.bash_rc_file.take() {
            let _ = std::fs::remove_file(path);
        }
        // Alacritty's drop owns reaping; the fallback above prevents its
        // unbounded wait from hanging on shells that ignored the hangup.
    }
}

fn write_bash_rcfile(token: &str) -> std::io::Result<PathBuf> {
    let path = std::env::temp_dir().join(format!(
        "omaterm-bashrc-{}-{token}",
        // SAFETY: getuid has no preconditions and returns the current process UID.
        unsafe { libc::getuid() }
    ));
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .custom_flags(libc::O_CLOEXEC | libc::O_NOFOLLOW)
        .open(&path)?;
    if let Err(error) = file.write_all(crate::shell::bash_rcfile(token).as_bytes()) {
        let _ = std::fs::remove_file(&path);
        return Err(error);
    }
    Ok(path)
}
