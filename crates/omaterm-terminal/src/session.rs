use std::path::PathBuf;

use omaterm_core::SessionId;

use crate::alacritty::AlacrittyEngine;
use crate::engine::{EngineOutput, ScrollCommand, TerminalEngine, TerminalViewport};
use crate::osc7::Osc7Parser;
use crate::pty::{PtyError, PtyProcess};

/// Errors from session creation.
#[derive(Debug, thiserror::Error)]
pub enum SessionError {
    #[error(transparent)]
    Pty(#[from] PtyError),
    #[error("invalid grid size {cols}x{rows}")]
    InvalidSize { cols: u16, rows: u16 },
}

#[derive(Debug, thiserror::Error)]
pub enum RunCommandError {
    #[error("terminal.run is not supported for this shell")]
    UnsupportedShell,
    #[error("shell is not at a confirmed ready prompt")]
    ShellBusy,
    #[error("argv must contain at least one argument")]
    EmptyArgv,
    #[error("terminal input write failed: {0}")]
    Io(#[from] std::io::Error),
}

/// Where the session's current directory was confirmed from.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CwdProvenance {
    /// Directory the child was spawned in (initial, always trusted).
    Launch,
    /// Validated local OSC 7 report from the shell.
    Osc7,
    /// Best-effort Linux `/proc/<pid>/cwd` read (diagnostic fallback).
    Procfs,
}

/// Last confirmed shell directory plus how it was confirmed. The launch
/// directory is retained whenever an OSC 7 report is rejected.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CurrentDirectory {
    pub path: PathBuf,
    pub provenance: CwdProvenance,
}

/// Durable terminal identity: PTY + engine + metadata.
///
/// The session survives UI changes (tab switch, focus, layout). Views hold
/// only the `SessionId`; the registry (M4) will own the session itself.
pub struct TerminalSession {
    pub id: SessionId,
    pub working_directory: PathBuf,
    cwd: CurrentDirectory,
    osc7: Osc7Parser,
    pty: PtyProcess,
    engine: AlacrittyEngine,
    title: Option<String>,
    exited: Option<std::process::ExitStatus>,
    prompt_marker: Option<Vec<u8>>,
    prompt_scan: Vec<u8>,
    prompt_ready: bool,
}

impl TerminalSession {
    pub fn new(
        working_directory: PathBuf,
        shell: Option<&str>,
        cols: u16,
        rows: u16,
    ) -> Result<Self, SessionError> {
        Self::new_with_id(SessionId::new(), working_directory, shell, cols, rows)
    }

    /// Spawn a session with an identity allocated by the application owner.
    ///
    /// Background launch workers use this so their completion can be matched
    /// to a pending pane without mutating the registry themselves.
    pub fn new_with_id(
        id: SessionId,
        working_directory: PathBuf,
        shell: Option<&str>,
        cols: u16,
        rows: u16,
    ) -> Result<Self, SessionError> {
        Self::new_with_id_and_env(id, working_directory, shell, cols, rows, Default::default())
    }

    pub fn new_with_id_and_env(
        id: SessionId,
        working_directory: PathBuf,
        shell: Option<&str>,
        cols: u16,
        rows: u16,
        child_env: std::collections::HashMap<String, String>,
    ) -> Result<Self, SessionError> {
        if cols < 2 || rows < 1 {
            return Err(SessionError::InvalidSize { cols, rows });
        }
        let prompt_token = id.0.simple().to_string();
        let pty = PtyProcess::spawn_with_env(
            shell,
            cols,
            rows,
            &working_directory,
            &prompt_token,
            child_env,
        )?;
        let prompt_marker = pty
            .supports_run()
            .then(|| format!("\x1b]133;A;{prompt_token}\x07").into_bytes());
        let engine = AlacrittyEngine::new(cols, rows);
        let cwd = CurrentDirectory {
            path: working_directory.clone(),
            provenance: CwdProvenance::Launch,
        };
        Ok(Self {
            id,
            working_directory,
            cwd,
            osc7: Osc7Parser::new(),
            pty,
            engine,
            title: None,
            exited: None,
            prompt_marker,
            prompt_scan: Vec::new(),
            prompt_ready: false,
        })
    }

    pub fn id(&self) -> SessionId {
        self.id
    }

    /// Pump available PTY output into the engine. Call from the reader thread.
    ///
    /// Reads until `WouldBlock`, forwards engine replies to the PTY, and
    /// records title / exit state. Returns the combined engine output plus
    /// the number of PTY bytes consumed, so the caller can skip viewport
    /// snapshots (full grid clones) when nothing arrived. This keeps idle
    /// CPU near zero: an idle shell produces no bytes, hence no snapshots.
    pub fn pump(&mut self) -> std::io::Result<(EngineOutput, usize)> {
        let mut combined = EngineOutput::default();
        let mut bytes_read = 0usize;
        let mut buf = [0u8; 8192];
        let mut read_error: Option<std::io::Error> = None;
        loop {
            match self.pty.read(&mut buf) {
                Ok(0) => break, // EOF: child closed the PTY
                Ok(n) => {
                    bytes_read += n;
                    let chunk = &buf[..n];
                    let out = self.engine.advance_output(chunk);
                    if !out.reply_bytes.is_empty() {
                        // Best effort: a failed reply write surfaces next pump.
                        let _ = self.pty.write_all(&out.reply_bytes);
                    }
                    combined.events.extend(out.events);
                    self.observe_osc7(chunk, &mut combined);
                    self.observe_prompt_marker(chunk);
                }
                Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => break,
                Err(e) if e.kind() == std::io::ErrorKind::Interrupted => continue,
                // Linux reports EIO on the master once the slave side closes.
                // Treat as drain-end: fall through to child-exit detection below.
                Err(e) => {
                    read_error = Some(e);
                    break;
                }
            }
        }
        self.after_pump(&mut combined);
        // If the child exited, that explains the read error; report success
        // with the exit event instead of surfacing EIO.
        if self.exited.is_some() {
            return Ok((combined, bytes_read));
        }
        if let Some(e) = read_error {
            return Err(e);
        }
        Ok((combined, bytes_read))
    }

    /// Feed bytes without PTY I/O (tests).
    pub fn advance_output(&mut self, bytes: &[u8]) -> EngineOutput {
        let mut out = self.engine.advance_output(bytes);
        self.after_pump_collect(&out);
        self.observe_osc7(bytes, &mut out);
        self.observe_prompt_marker(bytes);
        out
    }

    /// Run the streaming OSC 7 extractor over raw PTY bytes. Accepted local
    /// directories update the session CWD (provenance `Osc7`) and surface a
    /// domain event; rejected reports leave state untouched.
    fn observe_osc7(&mut self, bytes: &[u8], out: &mut EngineOutput) {
        for path in self.osc7.feed(bytes) {
            if self.cwd.path != path {
                self.cwd = CurrentDirectory {
                    path: path.clone(),
                    provenance: CwdProvenance::Osc7,
                };
                out.events
                    .push(crate::events::TerminalEvent::CwdChanged(path));
            }
        }
    }

    fn observe_prompt_marker(&mut self, bytes: &[u8]) {
        let Some(marker) = self.prompt_marker.as_ref() else {
            return;
        };
        self.prompt_scan.extend_from_slice(bytes);
        if self
            .prompt_scan
            .windows(marker.len())
            .any(|window| window == marker)
        {
            self.prompt_ready = true;
            self.prompt_scan.clear();
        } else if self.prompt_scan.len() >= marker.len() {
            let keep = marker.len().saturating_sub(1);
            let remove = self.prompt_scan.len() - keep;
            self.prompt_scan.drain(..remove);
        }
    }

    fn after_pump(&mut self, combined: &mut EngineOutput) {
        // Title cache.
        if let Some(title) = self.engine.title() {
            let title = title.to_string();
            if self.title.as_deref() != Some(&title) {
                self.title = Some(title);
            }
        }
        // Child exit (race-free via SIGCHLD pipe).
        if self.exited.is_none() {
            // `next_child_event` may report Exited(None) while reaping; poll once.
            if let Some(event) = self.pty.try_child_event() {
                match event {
                    alacritty_terminal::tty::ChildEvent::Exited(status) => {
                        if let Some(status) = status {
                            self.exited = Some(status);
                            combined
                                .events
                                .push(crate::events::TerminalEvent::ChildExited(status));
                        }
                    }
                }
            }
        }
    }

    fn after_pump_collect(&mut self, out: &EngineOutput) {
        let _ = out;
        if let Some(title) = self.engine.title() {
            self.title = Some(title.to_string());
        }
    }

    pub fn write_input(&mut self, bytes: &[u8]) -> std::io::Result<()> {
        // Any input while scrolled up returns to the bottom first (M3 behavior).
        if self.engine.display_offset() != 0 {
            self.engine.scroll(ScrollCommand::Bottom);
        }
        if !bytes.is_empty() {
            self.prompt_ready = false;
        }
        self.pty.write_all(bytes)
    }

    pub fn prompt_ready(&self) -> bool {
        self.prompt_ready
    }

    /// Submit a structured argv only after the trusted Bash prompt hook has
    /// confirmed the shell is idle. This acknowledges input submission only.
    pub fn run_argv(&mut self, argv: &[String]) -> Result<(), RunCommandError> {
        if !self.pty.supports_run() {
            return Err(RunCommandError::UnsupportedShell);
        }
        if !self.prompt_ready {
            return Err(RunCommandError::ShellBusy);
        }
        if argv.is_empty() {
            return Err(RunCommandError::EmptyArgv);
        }
        let mut command = crate::shell::encode_bash_argv(argv).into_bytes();
        command.push(b'\n');
        self.pty.write_all(&command)?;
        self.prompt_ready = false;
        Ok(())
    }

    pub fn resize(&mut self, cols: u16, rows: u16) {
        self.pty.resize(cols, rows);
        self.engine.resize(cols, rows);
    }

    pub fn scroll(&mut self, command: ScrollCommand) {
        self.engine.scroll(command);
    }

    pub fn viewport(&self) -> TerminalViewport {
        self.engine.viewport()
    }

    pub fn read_visible_text(&self, max_lines: usize, max_columns: usize) -> String {
        self.engine.read_visible_text(max_lines, max_columns)
    }

    pub fn title(&self) -> Option<&str> {
        self.title.as_deref().or_else(|| self.engine.title())
    }

    pub fn exited(&self) -> Option<std::process::ExitStatus> {
        self.exited
    }

    pub fn child_pid(&self) -> u32 {
        self.pty.child_pid()
    }

    /// Raw PTY master FD for kernel-side waiting (stable for the lifetime).
    pub fn pty_fd(&self) -> std::os::fd::RawFd {
        self.pty.as_raw_fd()
    }

    /// Block up to `timeout_ms` for PTY output. See `PtyProcess::poll_readable`.
    pub fn poll_readable(&self, timeout_ms: i32) -> std::io::Result<bool> {
        self.pty.poll_readable(timeout_ms)
    }

    /// Lightweight child-exit check without draining output. Used on poll
    /// timeouts so exit is noticed promptly even with no PTY bytes.
    /// Returns `true` when the child has exited.
    pub fn poll_child(&mut self) -> bool {
        if self.exited.is_some() {
            return true;
        }
        if let Some(alacritty_terminal::tty::ChildEvent::Exited(Some(status))) =
            self.pty.try_child_event()
        {
            self.exited = Some(status);
            return true;
        }
        false
    }

    /// Explicit shutdown: SIGHUP the child, then wait bounded for exit
    /// and reap. Returns `true` when the child is confirmed gone.
    /// Never blocks indefinitely (2s cap); the `PtyProcess` drop path
    /// still guarantees SIGHUP even if this is never called.
    pub fn shutdown(&mut self) -> bool {
        if self.exited.is_some() {
            return true;
        }
        let _ = self.pty.terminate();
        let start = std::time::Instant::now();
        while start.elapsed() < std::time::Duration::from_secs(2) {
            if self.poll_child() {
                return true;
            }
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        self.poll_child()
    }

    pub fn child_cwd(&self) -> Option<PathBuf> {
        self.pty.child_cwd()
    }

    /// Last confirmed shell directory and its provenance. Starts as the
    /// launch directory; validated OSC 7 reports supersede it.
    pub fn cwd(&self) -> &CurrentDirectory {
        &self.cwd
    }

    /// Best-effort Linux refresh from `/proc/<pid>/cwd`. Updates state only
    /// when the link resolves to an existing directory; records `Procfs`
    /// provenance so callers can distinguish it from shell reports.
    pub fn refresh_cwd_from_procfs(&mut self) -> bool {
        let Some(path) = self.pty.child_cwd() else {
            return false;
        };
        if !path.exists() || self.cwd.path == path {
            return false;
        }
        self.cwd = CurrentDirectory {
            path,
            provenance: CwdProvenance::Procfs,
        };
        true
    }

    /// Input-mode queries through the engine abstraction (no backend leak).
    pub fn app_cursor(&self) -> bool {
        self.engine.app_cursor()
    }

    /// Input-mode queries through the engine abstraction (no backend leak).
    pub fn app_keypad(&self) -> bool {
        self.engine.app_keypad()
    }

    /// Input-mode queries through the engine abstraction (no backend leak).
    pub fn bracketed_paste(&self) -> bool {
        self.engine.bracketed_paste()
    }

    pub fn engine(&self) -> &AlacrittyEngine {
        &self.engine
    }

    pub fn engine_mut(&mut self) -> &mut AlacrittyEngine {
        &mut self.engine
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::events::TerminalEvent;

    fn test_session() -> TerminalSession {
        TerminalSession::new(std::env::temp_dir(), Some("/bin/sh"), 80, 24).expect("spawn sh")
    }

    #[test]
    fn cwd_starts_at_launch_directory() {
        let session = test_session();
        assert_eq!(session.cwd().provenance, CwdProvenance::Launch);
        assert_eq!(session.cwd().path, std::env::temp_dir());
    }

    #[test]
    fn valid_osc7_updates_cwd_with_event() {
        let mut session = test_session();
        let out = session.advance_output(b"\x1b]7;file:///tmp/omaterm-cwd\x07");
        assert_eq!(session.cwd().path, PathBuf::from("/tmp/omaterm-cwd"));
        assert_eq!(session.cwd().provenance, CwdProvenance::Osc7);
        assert!(
            out.events
                .iter()
                .any(|e| matches!(e, TerminalEvent::CwdChanged(_))),
            "expected CwdChanged event, got {:?}",
            out.events
        );
    }

    #[test]
    fn invalid_osc7_keeps_launch_cwd() {
        let mut session = test_session();
        let launch = session.cwd().clone();
        let out = session.advance_output(b"\x1b]7;file://evil-host/etc\x07output");
        assert_eq!(session.cwd(), &launch);
        assert!(
            !out.events
                .iter()
                .any(|e| matches!(e, TerminalEvent::CwdChanged(_))),
            "rejected report must not emit CwdChanged"
        );
    }

    #[test]
    fn fragmented_osc7_reassembles() {
        let mut session = test_session();
        session.advance_output(b"prompt\x1b]7;file://");
        assert_eq!(session.cwd().provenance, CwdProvenance::Launch);
        session.advance_output(b"/tmp/frag\x07");
        assert_eq!(session.cwd().path, PathBuf::from("/tmp/frag"));
        assert_eq!(session.cwd().provenance, CwdProvenance::Osc7);
    }

    #[test]
    fn repeated_same_cwd_emits_no_duplicate_event() {
        let mut session = test_session();
        session.advance_output(b"\x1b]7;file:///tmp/dup\x07");
        let out = session.advance_output(b"\x1b]7;file:///tmp/dup\x07");
        assert!(
            !out.events
                .iter()
                .any(|e| matches!(e, TerminalEvent::CwdChanged(_))),
            "unchanged CWD must not re-emit"
        );
    }
}
