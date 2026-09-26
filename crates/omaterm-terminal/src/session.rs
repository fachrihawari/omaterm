use std::path::PathBuf;

use omaterm_core::SessionId;

use crate::alacritty::AlacrittyEngine;
use crate::engine::{EngineOutput, ScrollCommand, TerminalEngine, TerminalViewport};
use crate::pty::{PtyError, PtyProcess};

/// Errors from session creation.
#[derive(Debug, thiserror::Error)]
pub enum SessionError {
    #[error(transparent)]
    Pty(#[from] PtyError),
    #[error("invalid grid size {cols}x{rows}")]
    InvalidSize { cols: u16, rows: u16 },
}

/// Durable terminal identity: PTY + engine + metadata.
///
/// The session survives UI changes (tab switch, focus, layout). Views hold
/// only the `SessionId`; the registry (M4) will own the session itself.
pub struct TerminalSession {
    pub id: SessionId,
    pub working_directory: PathBuf,
    pty: PtyProcess,
    engine: AlacrittyEngine,
    title: Option<String>,
    exited: Option<std::process::ExitStatus>,
}

impl TerminalSession {
    pub fn new(
        working_directory: PathBuf,
        shell: Option<&str>,
        cols: u16,
        rows: u16,
    ) -> Result<Self, SessionError> {
        if cols < 2 || rows < 1 {
            return Err(SessionError::InvalidSize { cols, rows });
        }
        let pty = PtyProcess::spawn(shell, cols, rows, &working_directory)?;
        let engine = AlacrittyEngine::new(cols, rows);
        Ok(Self {
            id: SessionId::new(),
            working_directory,
            pty,
            engine,
            title: None,
            exited: None,
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
                    let out = self.engine.advance_output(&buf[..n]);
                    if !out.reply_bytes.is_empty() {
                        // Best effort: a failed reply write surfaces next pump.
                        let _ = self.pty.write_all(&out.reply_bytes);
                    }
                    combined.events.extend(out.events);
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
        let out = self.engine.advance_output(bytes);
        self.after_pump_collect(&out);
        out
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
        // OSC-7 style title updates already handled; CWD tracking (Phase C)
        // reads child_cwd() on demand.
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
        self.pty.write_all(bytes)
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

    pub fn child_cwd(&self) -> Option<PathBuf> {
        self.pty.child_cwd()
    }

    pub fn engine(&self) -> &AlacrittyEngine {
        &self.engine
    }

    pub fn engine_mut(&mut self) -> &mut AlacrittyEngine {
        &mut self.engine
    }
}
