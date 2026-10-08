use std::collections::HashMap;
use std::fs::OpenOptions;
use std::io::{Read, Write};
#[cfg(unix)]
use std::os::fd::{AsRawFd, RawFd};
#[cfg(unix)]
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
    program: String,
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
            .filter(|value| !value.is_empty())
            .or_else(|| {
                std::env::var("SHELL")
                    .ok()
                    .filter(|value| crate::shell::accept_shell_env(value))
            })
            .unwrap_or_else(default_shell);

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
            Some(write_bash_rcfile(prompt_token, working_directory).map_err(PtyError::Io)?)
        } else {
            None
        };
        let launch = bash_rc_file
            .as_ref()
            .map(|path| crate::shell::bash_shell_launch(&program, path, working_directory));
        let shell_args = launch
            .as_ref()
            .map(|launch| launch.args.clone())
            .unwrap_or_default();
        let launch_directory = launch
            .as_ref()
            .map(|launch| launch.directory.clone())
            .unwrap_or_else(|| working_directory.to_path_buf());

        let options = Options {
            shell: Some(Shell::new(program.clone(), shell_args)),
            working_directory: Some(launch_directory),
            drain_on_exit: true,
            env,
            #[cfg(windows)]
            escape_args: true,
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
        #[cfg(unix)]
        let child_pid = pty.child().id();
        #[cfg(windows)]
        let child_pid = pty
            .child_watcher()
            .pid()
            .map(std::num::NonZeroU32::get)
            .unwrap_or(0);
        Ok(Self {
            pty,
            child_pid,
            supports_run,
            bash_rc_file,
            program,
        })
    }

    /// Program actually passed to the PTY, after `$SHELL` / default resolution.
    #[must_use]
    pub fn program(&self) -> &str {
        &self.program
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
        #[cfg(unix)]
        {
            // SAFETY: `kill` with a signal number touches no memory.
            let result = unsafe { libc::kill(self.child_pid as libc::pid_t, libc::SIGHUP) };
            if result != 0 {
                Err(std::io::Error::last_os_error())
            } else {
                Ok(())
            }
        }
        #[cfg(windows)]
        {
            terminate_process(self.pty.child_watcher().raw_handle())
        }
    }

    /// Escalate only while this PID is still our unreaped child. `WNOWAIT`
    /// leaves reaping to Alacritty's `Child`, avoiding PID reuse and a second
    /// owner of the child's exit status. Windows `TerminateProcess` is already
    /// final, so the force path is the same call.
    pub fn terminate_force(&self) {
        #[cfg(unix)]
        if !self.child_exit_ready() {
            // SAFETY: the unreaped child retains its PID; kill touches no memory.
            unsafe { libc::kill(self.child_pid as libc::pid_t, libc::SIGKILL) };
        }
        #[cfg(windows)]
        {
            let _ = terminate_process(self.pty.child_watcher().raw_handle());
        }
    }

    fn child_exit_ready(&self) -> bool {
        #[cfg(unix)]
        {
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
                (unsafe { info.si_pid() }) != 0
            } else {
                std::io::Error::last_os_error().raw_os_error() == Some(libc::ECHILD)
            }
        }
        #[cfg(windows)]
        {
            // `TerminateProcess` is already final, so the force path does not
            // wait for the child to exit before calling it again.
            true
        }
    }

    pub fn child_pid(&self) -> u32 {
        self.child_pid
    }

    pub fn supports_run(&self) -> bool {
        self.supports_run
    }

    /// Best-effort Linux CWD discovery via `/proc/<pid>/cwd`.
    pub fn child_cwd(&self) -> Option<PathBuf> {
        #[cfg(unix)]
        {
            let link = format!("/proc/{}/cwd", self.child_pid);
            std::fs::read_link(link).ok()
        }
        #[cfg(windows)]
        {
            None
        }
    }

    /// Copy of the wait token. On Linux this is the PTY master fd, which stays
    /// open for the session lifetime so the reader can sleep without the lock.
    pub fn output_wait(&self) -> OutputWait {
        #[cfg(unix)]
        {
            OutputWait {
                fd: self.pty.file().as_raw_fd(),
            }
        }
        #[cfg(windows)]
        {
            OutputWait
        }
    }

    /// Raw master FD for `poll()`-based waiting. Stable for the session lifetime.
    #[cfg(unix)]
    pub fn as_raw_fd(&self) -> RawFd {
        self.pty.file().as_raw_fd()
    }

    /// Block up to `timeout_ms` until PTY output (or hangup/error) is
    /// available. Returns `true` when a `read()` is worthwhile. This keeps an
    /// idle terminal near zero CPU: the reader thread sleeps in the kernel
    /// instead of spin-polling.
    pub fn poll_readable(&self, timeout_ms: i32) -> std::io::Result<bool> {
        self.output_wait().poll(timeout_ms)
    }
}

/// Wait token copied out of a session so the reader thread can sleep without
/// holding the session lock.
#[cfg(unix)]
#[derive(Clone, Copy)]
pub struct OutputWait {
    fd: RawFd,
}

#[cfg(unix)]
impl OutputWait {
    pub fn poll(self, timeout_ms: i32) -> std::io::Result<bool> {
        poll_fd_readable(self.fd, timeout_ms)
    }
}

/// ConPTY delivery is internal to Alacritty's reader thread, so there is no
/// kernel fd to sleep on. The desktop reader wakes on this interval and pumps
/// a non-blocking read. That wake is a sleep, so an idle terminal still
/// burns a timer tick instead of blocking in the kernel.
#[cfg(windows)]
#[derive(Clone, Copy)]
pub struct OutputWait;

#[cfg(windows)]
impl OutputWait {
    pub fn poll(self, timeout_ms: i32) -> std::io::Result<bool> {
        if timeout_ms > 0 {
            std::thread::sleep(std::time::Duration::from_millis(timeout_ms as u64));
        }
        Ok(true)
    }
}

/// Block up to `timeout_ms` until `fd` is readable (or hung up / errored).
/// Lock-free helper so the reader thread can wait without holding the
/// session lock.
#[cfg(unix)]
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

fn bash_rc_owner() -> u32 {
    #[cfg(unix)]
    {
        // SAFETY: getuid has no preconditions and returns the current process UID.
        unsafe { libc::getuid() }
    }
    #[cfg(windows)]
    {
        std::process::id()
    }
}

fn default_shell() -> String {
    #[cfg(windows)]
    {
        "powershell.exe".to_string()
    }
    #[cfg(not(windows))]
    {
        "/bin/bash".to_string()
    }
}

#[cfg(windows)]
fn terminate_process(handle: windows_sys::Win32::Foundation::HANDLE) -> std::io::Result<()> {
    if handle.is_null() {
        return Ok(());
    }
    // SAFETY: the handle is the child process owned by this PTY. The exit
    // code is an arbitrary non-zero status.
    let ok = unsafe { windows_sys::Win32::System::Threading::TerminateProcess(handle, 1) };
    if ok == 0 {
        Err(std::io::Error::last_os_error())
    } else {
        Ok(())
    }
}

fn write_bash_rcfile(token: &str, working_directory: &Path) -> std::io::Result<PathBuf> {
    let path = std::env::temp_dir().join(format!("omaterm-bashrc-{}-{token}", bash_rc_owner()));
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        options
            .mode(0o600)
            .custom_flags(libc::O_CLOEXEC | libc::O_NOFOLLOW);
    }
    let mut file = options.open(&path)?;
    // Git Bash cannot be given `--login` and an absolute `--rcfile` (it exits
    // before the prompt). The process starts in this file's directory, so the
    // file moves to the requested directory and then loads the login profile
    // the Git Bash shortcut would have loaded.
    #[cfg(windows)]
    let prelude = format!(
        "cd {}\n[ -f /etc/profile ] && . /etc/profile\n",
        crate::shell::quote_bash_argument(&crate::shell::bash_directory(working_directory))
    );
    #[cfg(not(windows))]
    let prelude = String::new();
    let _ = working_directory;
    let body = format!("{prelude}{}", crate::shell::bash_rcfile(token));
    if let Err(error) = file.write_all(body.as_bytes()) {
        let _ = std::fs::remove_file(&path);
        return Err(error);
    }
    Ok(path)
}

#[cfg(all(test, windows))]
mod tests {
    use super::PtyProcess;
    use std::io::ErrorKind;
    use std::time::{Duration, Instant};

    #[test]
    fn git_bash_stays_open_in_the_requested_directory() {
        let Ok(resolved) = crate::shell::WindowsShell::GitBash.resolve() else {
            return;
        };
        let directory =
            std::env::temp_dir().join(format!("omaterm-gitbash-cwd-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&directory);
        std::fs::create_dir_all(&directory).unwrap();
        let mut pty = match PtyProcess::spawn(Some(&resolved.program), 80, 24, &directory, "probe")
        {
            Ok(pty) => pty,
            Err(error) => {
                let _ = std::fs::remove_dir_all(&directory);
                panic!("git bash did not start: {error}");
            }
        };
        let deadline = Instant::now() + Duration::from_millis(1500);
        let mut saw_exit = false;
        while Instant::now() < deadline {
            if pty.try_child_event().is_some() {
                saw_exit = true;
                break;
            }
            std::thread::sleep(Duration::from_millis(50));
        }
        let write = pty.write_all(b"echo OMATERM_DIR:$PWD\n");
        std::thread::sleep(Duration::from_millis(400));
        let mut buf = vec![0u8; 16384];
        let mut text = String::new();
        let read_deadline = Instant::now() + Duration::from_secs(3);
        while Instant::now() < read_deadline {
            match pty.read(&mut buf) {
                Ok(0) => break,
                Ok(n) => text.push_str(&String::from_utf8_lossy(&buf[..n])),
                Err(error) if error.kind() == ErrorKind::WouldBlock => {
                    if text.contains("OMATERM_DIR:") {
                        break;
                    }
                    std::thread::sleep(Duration::from_millis(50));
                }
                Err(error) => panic!("{error}"),
            }
        }
        if pty.try_child_event().is_some() {
            saw_exit = true;
        }
        let _ = pty.terminate();
        let _ = std::fs::remove_dir_all(&directory);
        let flat: String = text.chars().filter(|ch| !ch.is_control()).collect();
        assert!(!saw_exit, "git bash exited before the prompt:\n{flat}");
        assert!(write.is_ok(), "could not write to git bash: {write:?}");
        let folder = directory
            .file_name()
            .and_then(|name| name.to_str())
            .unwrap_or("");
        assert!(
            flat.contains("OMATERM_DIR:") && flat.contains(folder),
            "git bash did not stay in the requested directory:\n{flat}"
        );
        assert!(
            !flat.contains("invalid option"),
            "git bash rejected its launch arguments:\n{flat}"
        );
    }
}
