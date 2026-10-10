use std::collections::VecDeque;
use std::path::PathBuf;

use omaterm_core::SessionId;

use crate::alacritty::AlacrittyEngine;
use crate::engine::{EngineOutput, ScrollCommand, TerminalEngine, TerminalViewport};
use crate::history::{HistoryRecorder, RecordedEvent, RecorderLimits};
use crate::lifecycle::{
    LifecycleEvent, LifecycleKind, LifecycleParser, decode_command, decode_exit,
};
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
    lifecycle: Option<LifecycleParser>,
    pending_command: Option<PendingCommand>,
    lifecycle_records: VecDeque<LifecycleRecord>,
    recorder: HistoryRecorder,
    history_enabled: bool,
    history_paused: bool,
    /// Armed only after restored scrollback is seeded. Drops the fresh
    /// console's leading erase until the first graphic byte.
    startup_clear: crate::history::StartupClearFilter,
    /// Program passed to the PTY, after `$SHELL` / default resolution.
    shell_program: String,
    prompt_ready: bool,
    /// A prompt has been observed at least once. Command starts reported
    /// before the first prompt are shell initialization (rcfile lines,
    /// user dotfiles) rather than user commands, and are dropped.
    seen_prompt: bool,
}

/// A command the shell reported starting but no prompt has completed yet.
#[derive(Debug, Clone, PartialEq, Eq)]
struct PendingCommand {
    command: String,
    started_unix_secs: u64,
    working_directory: PathBuf,
}

/// One authoritative shell lifecycle record: a reported command start,
/// optionally completed by the next prompt with the shell's own exit
/// status. Created only from token-authenticated lifecycle events —
/// never from terminal text. Drained by the history writer (M10 Phase 2).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LifecycleRecord {
    pub command: String,
    pub shell_dialect: String,
    pub working_directory: PathBuf,
    pub started_unix_secs: u64,
    pub finished_unix_secs: Option<u64>,
    pub exit_status: Option<i32>,
}

/// Bounded completed-record queue: oldest records drop first so a chatty
/// shell cannot grow memory without a history drain.
const MAX_QUEUED_RECORDS: usize = 512;

fn now_unix_secs() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|elapsed| elapsed.as_secs())
        .unwrap_or(0)
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
        Self::new_with_id_and_env(
            id,
            working_directory,
            shell,
            cols,
            rows,
            Default::default(),
            None,
        )
    }

    pub fn new_with_id_and_env(
        id: SessionId,
        working_directory: PathBuf,
        shell: Option<&str>,
        cols: u16,
        rows: u16,
        child_env: std::collections::HashMap<String, String>,
        scrollback_lines: Option<usize>,
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
        // Only Bash shells receive the lifecycle rcfile, so only they get a
        // parser bound to the session token. Other shells run normally with
        // no journal capture.
        let lifecycle = pty
            .supports_run()
            .then(|| LifecycleParser::new(&prompt_token));
        let engine = match scrollback_lines {
            Some(lines) => AlacrittyEngine::with_scrollback(cols, rows, lines.max(1)),
            None => AlacrittyEngine::new(cols, rows),
        };
        let cwd = CurrentDirectory {
            path: working_directory.clone(),
            provenance: CwdProvenance::Launch,
        };
        let shell_program = pty.program().to_string();
        Ok(Self {
            id,
            working_directory,
            cwd,
            osc7: Osc7Parser::new(),
            pty,
            engine,
            title: None,
            exited: None,
            lifecycle,
            pending_command: None,
            lifecycle_records: VecDeque::new(),
            recorder: HistoryRecorder::new(RecorderLimits::default()),
            history_enabled: false,
            history_paused: false,
            startup_clear: crate::history::StartupClearFilter::default(),
            shell_program,
            prompt_ready: false,
            seen_prompt: false,
        })
    }

    pub fn id(&self) -> SessionId {
        self.id
    }

    /// Program this session was spawned with.
    #[must_use]
    pub fn shell_program(&self) -> &str {
        &self.shell_program
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
                    let filtered = self.startup_clear.apply(&buf[..n]);
                    let chunk = filtered.as_ref();
                    let was_alt = self.engine.is_alt_screen();
                    let out = self.engine.advance_output(chunk);
                    if !out.reply_bytes.is_empty() {
                        // Best effort: a failed reply write surfaces next pump.
                        let _ = self.pty.write_all(&out.reply_bytes);
                    }
                    combined.events.extend(out.events);
                    self.observe_osc7(chunk, &mut combined);
                    self.observe_lifecycle(chunk);
                    self.observe_history(chunk, was_alt);
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
        let filtered = self.startup_clear.apply(bytes);
        let bytes = filtered.as_ref();
        let was_alt = self.engine.is_alt_screen();
        let mut out = self.engine.advance_output(bytes);
        self.after_pump_collect(&out);
        self.observe_osc7(bytes, &mut out);
        self.observe_lifecycle(bytes);
        self.observe_history(bytes, was_alt);
        out
    }

    /// Record one PTY chunk into the scrollback recorder. Hot path: bounded
    /// memcopy only, never crypto/IO. Alt-screen policy mirrors the journal
    /// channel — bytes from alternate-screen periods never enter the record.
    fn observe_history(&mut self, chunk: &[u8], was_alt: bool) {
        if !self.history_enabled {
            return;
        }
        let is_alt = self.engine.is_alt_screen();
        self.recorder.set_alt_screen(is_alt);
        self.recorder.observe_output(chunk, was_alt, is_alt);
    }

    /// Opt the session into (or out of) scrollback recording. Enabling
    /// starts a new record — output from before opt-in is never backfilled.
    /// Disabling discards the in-memory record.
    pub fn set_history_enabled(&mut self, enabled: bool) {
        self.history_enabled = enabled;
        self.recorder.set_enabled(enabled);
    }

    /// Start recording with a restored record: the verified pre-restart
    /// events become the record prefix so the next flush persists merged
    /// history instead of discarding the restored prefix.
    pub fn start_history_with_seed(&mut self, events: &[RecordedEvent]) {
        self.history_enabled = true;
        self.recorder.set_enabled(true);
        self.recorder.seed(events);
        if events
            .iter()
            .any(|event| matches!(event, RecordedEvent::Output(bytes) if !bytes.is_empty()))
        {
            self.startup_clear.arm(self.viewport().cursor.row);
        }
    }

    /// Pause or resume capture for this session without dropping the record.
    pub fn set_history_paused(&mut self, paused: bool) {
        self.history_paused = paused;
        self.recorder.set_paused(paused);
    }

    #[must_use]
    pub fn history_enabled(&self) -> bool {
        self.history_enabled
    }

    #[must_use]
    pub fn history_paused(&self) -> bool {
        self.history_paused
    }

    /// Snapshot the current scrollback record for the background writer.
    #[must_use]
    pub fn history_snapshot(&self) -> Vec<RecordedEvent> {
        self.recorder.snapshot()
    }

    /// Recorder mutation counter for writer dirty tracking.
    #[must_use]
    pub fn history_version(&self) -> u64 {
        self.recorder.version()
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

    /// Run the token-authenticated lifecycle parser over raw PTY bytes.
    ///
    /// `PromptReady` sets prompt readiness (the legacy markerless form
    /// included) and completes the pending command, if any, with the
    /// shell's own exit status. `CommandStart` stores the pending command;
    /// a new start flushes an uncompleted predecessor as an unfinished
    /// record (`cmd1; cmd2` attributes completion to `cmd2`). Anything that
    /// fails authentication or decoding leaves all state untouched.
    fn observe_lifecycle(&mut self, bytes: &[u8]) {
        let Some(parser) = self.lifecycle.as_mut() else {
            return;
        };
        let events: Vec<LifecycleEvent> = parser.feed(bytes);
        for event in events {
            match event.kind {
                LifecycleKind::CommandStart => self.on_command_start(&event.payload),
                LifecycleKind::PromptReady => self.on_prompt_ready(&event.payload),
            }
        }
    }

    fn on_command_start(&mut self, payload: &str) {
        if !self.seen_prompt {
            return;
        }
        let Some(command) = decode_command(payload) else {
            return;
        };
        if let Some(pending) = self.pending_command.take() {
            self.push_record(LifecycleRecord {
                command: pending.command,
                shell_dialect: "bash".to_string(),
                working_directory: pending.working_directory,
                started_unix_secs: pending.started_unix_secs,
                finished_unix_secs: None,
                exit_status: None,
            });
        }
        self.pending_command = Some(PendingCommand {
            command,
            started_unix_secs: now_unix_secs(),
            working_directory: self.cwd.path.clone(),
        });
    }

    fn on_prompt_ready(&mut self, payload: &str) {
        self.prompt_ready = true;
        self.seen_prompt = true;
        let Some(exit) = decode_exit(payload) else {
            return;
        };
        if let Some(pending) = self.pending_command.take() {
            let finished = now_unix_secs();
            self.push_record(LifecycleRecord {
                command: pending.command,
                shell_dialect: "bash".to_string(),
                working_directory: pending.working_directory,
                started_unix_secs: pending.started_unix_secs,
                finished_unix_secs: Some(finished),
                exit_status: exit,
            });
        }
    }

    fn push_record(&mut self, record: LifecycleRecord) {
        self.lifecycle_records.push_back(record);
        while self.lifecycle_records.len() > MAX_QUEUED_RECORDS {
            self.lifecycle_records.pop_front();
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
            self.startup_clear.disarm();
        }
        self.pty.write_all(bytes)
    }

    pub fn prompt_ready(&self) -> bool {
        self.prompt_ready
    }

    /// Drain completed lifecycle records for the history writer (M10).
    /// Bounded: the queue drops its oldest records past the cap, so a
    /// session that is never drained cannot grow memory without bound.
    pub fn drain_lifecycle_records(&mut self) -> Vec<LifecycleRecord> {
        self.lifecycle_records.drain(..).collect()
    }

    /// Whether this session emits authenticated lifecycle events (Bash with
    /// the private rcfile). Other shells run normally with no records.
    pub fn emits_lifecycle(&self) -> bool {
        self.lifecycle.is_some()
    }

    /// Session token for tests that feed crafted lifecycle sequences.
    #[cfg(test)]
    fn lifecycle_token_for_test(&self) -> Option<String> {
        // The parser owns the token; re-derive it the same way the PTY
        // layer does so tests stay coupled to the real association.
        self.lifecycle
            .as_ref()
            .map(|_| self.id.0.simple().to_string())
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
        // Main-screen resizes are part of the replay record; alt-screen
        // resizes are skipped by the recorder's alt gate.
        self.recorder.set_alt_screen(self.engine.is_alt_screen());
        self.recorder.observe_resize(cols, rows);
    }

    /// Ask children to repaint after a theme switch without changing the
    /// grid: signals the PTY foreground process group with `SIGWINCH`
    /// exactly as the kernel does on a real resize, so shells and TUIs
    /// redraw against the new palette. A same-size `TIOCSWINSZ` sends no
    /// signal (verified on Linux), and signaling only the direct child
    /// would miss foreground TUIs behind non-forwarding shells — hence the
    /// foreground group. The engine and the replay record are untouched —
    /// dimensions did not change, only colors did. Failures (no foreground
    /// group, exited shell) are ignored: the desktop still republishes the
    /// snapshot, so chrome is always correct.
    pub fn signal_theme_redraw(&mut self) {
        #[cfg(unix)]
        {
            // SAFETY: raw fd + signal number touch no memory. The fd belongs
            // to our own PTY master, and the target group lives in the
            // child's session — never our own process group.
            unsafe {
                let pgid = libc::tcgetpgrp(self.pty.as_raw_fd());
                if pgid > 1 {
                    let _ = libc::killpg(pgid, libc::SIGWINCH);
                }
            }
        }
    }

    pub fn scroll(&mut self, command: ScrollCommand) {
        self.engine.scroll(command);
    }

    pub fn viewport(&self) -> TerminalViewport {
        self.engine.viewport()
    }

    pub fn viewport_following_row(&self) -> Option<crate::engine::TerminalRow> {
        self.engine.viewport_following_row()
    }

    pub fn read_visible_text(&self, max_lines: usize, max_columns: usize) -> String {
        self.engine.read_visible_text(max_lines, max_columns)
    }

    /// Bounded full-scrollback dump for search, oldest line first.
    pub fn scrollback_text(&self, max_lines: usize) -> Vec<String> {
        self.engine.scrollback_text(max_lines)
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

    /// Wait token for the reader thread. On Linux this copies the PTY master fd.
    pub fn output_wait(&self) -> crate::pty::OutputWait {
        self.pty.output_wait()
    }

    /// Raw PTY master FD for kernel-side waiting (stable for the lifetime).
    #[cfg(unix)]
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
    /// Allows 2s for hangup then 250ms for forced termination; `PtyProcess`
    /// also escalates before the dependency's reaping drop if never called.
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
        self.pty.terminate_force();
        let deadline = std::time::Instant::now() + std::time::Duration::from_millis(250);
        while std::time::Instant::now() < deadline {
            if self.poll_child() {
                return true;
            }
            std::thread::sleep(std::time::Duration::from_millis(5));
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
        #[cfg(unix)]
        {
            self.refresh_cwd_with(&crate::platform::LinuxProcessInspector)
        }
        #[cfg(not(unix))]
        {
            false
        }
    }

    /// CWD refresh through the [`ProcessInspector`] seam (unit-testable with
    /// a stub; production passes the Linux adapter).
    pub fn refresh_cwd_with(&mut self, inspector: &dyn crate::platform::ProcessInspector) -> bool {
        let Some(path) = inspector.cwd_of(self.pty.child_pid()) else {
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

    /// Active application mouse-reporting mode (DECSET 1000/1002/1003/1006).
    pub fn mouse_mode(&self) -> crate::engine::MouseMode {
        self.engine.mouse_mode()
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
    fn configured_scrollback_caps_engine_history() {
        let mut limited = TerminalSession::new_with_id_and_env(
            SessionId::new(),
            std::env::temp_dir(),
            Some("/bin/sh"),
            80,
            24,
            Default::default(),
            Some(10),
        )
        .expect("spawn sh with scrollback cap");
        let mut default = test_session();
        let mut flood = Vec::new();
        for line in 0..60 {
            flood.extend_from_slice(format!("line {line:03}\n").as_bytes());
        }
        limited.advance_output(&flood);
        default.advance_output(&flood);
        assert!(
            limited.viewport().history_size <= 10,
            "capped session history must stay bounded, got {}",
            limited.viewport().history_size
        );
        assert!(
            default.viewport().history_size > limited.viewport().history_size,
            "default session must retain more than the capped one"
        );
    }

    #[test]
    fn cwd_refresh_uses_the_inspector_seam() {
        use crate::platform::{ProcessInfo, ProcessInspector};
        use std::path::PathBuf;

        struct Stub {
            cwd: Option<PathBuf>,
        }
        impl ProcessInspector for Stub {
            fn process_info(&self, _: u32) -> Option<ProcessInfo> {
                None
            }
            fn descendants(&self, _: u32) -> Vec<ProcessInfo> {
                Vec::new()
            }
            fn cwd_of(&self, _: u32) -> Option<PathBuf> {
                self.cwd.clone()
            }
            fn listening_ports(&self, _: u32) -> Vec<crate::platform::ListeningPort> {
                Vec::new()
            }
        }

        let mut session = test_session();
        assert!(!session.refresh_cwd_with(&Stub { cwd: None }));
        assert_eq!(session.cwd().provenance, CwdProvenance::Launch);
        // Same path is a no-op (no spurious state change).
        let launch = session.cwd().path.clone();
        assert!(!session.refresh_cwd_with(&Stub { cwd: Some(launch) }));
        // A new existing directory updates state with Procfs provenance.
        let dir = std::env::temp_dir().join(format!("omaterm-cwd-stub-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        assert!(session.refresh_cwd_with(&Stub {
            cwd: Some(dir.clone())
        }));
        assert_eq!(session.cwd().path, dir);
        assert_eq!(session.cwd().provenance, CwdProvenance::Procfs);
        let _ = std::fs::remove_dir(&dir);
        // Missing directories never apply.
        assert!(!session.refresh_cwd_with(&Stub {
            cwd: Some(PathBuf::from("/definitely/missing/omaterm-cwd")),
        }));
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

    fn bash_session() -> TerminalSession {
        TerminalSession::new(std::env::temp_dir(), Some("/bin/bash"), 80, 24).expect("spawn bash")
    }

    fn encode_command(text: &str) -> String {
        use base64::Engine;
        base64::engine::general_purpose::STANDARD.encode(text)
    }

    fn lifecycle_start(token: &str, command: &str) -> Vec<u8> {
        format!("\x1b]133;B;{token};{}\x07", encode_command(command)).into_bytes()
    }

    fn lifecycle_prompt(token: &str, status: &str) -> Vec<u8> {
        format!("\x1b]133;A;{token};{status}\x07").into_bytes()
    }

    /// Establish the first prompt (as a real shell startup does); startup
    /// itself must leave no records behind.
    fn establish_first_prompt(session: &mut TerminalSession, token: &str) {
        assert!(!session.prompt_ready());
        session.advance_output(&lifecycle_prompt(token, "0"));
        assert!(session.prompt_ready());
        assert!(session.drain_lifecycle_records().is_empty());
    }

    #[test]
    fn lifecycle_start_then_prompt_yields_completed_record() {
        let mut session = bash_session();
        assert!(session.emits_lifecycle());
        let token = session
            .lifecycle_token_for_test()
            .expect("bash has a token");
        establish_first_prompt(&mut session, &token);
        session.advance_output(&lifecycle_start(&token, "echo hello"));
        assert!(session.drain_lifecycle_records().is_empty());
        session.advance_output(&lifecycle_prompt(&token, "0"));
        assert!(session.prompt_ready());
        let records = session.drain_lifecycle_records();
        assert_eq!(records.len(), 1);
        assert_eq!(records[0].command, "echo hello");
        assert_eq!(records[0].shell_dialect, "bash");
        assert_eq!(records[0].working_directory, std::env::temp_dir());
        assert_eq!(records[0].exit_status, Some(0));
        assert!(records[0].finished_unix_secs.unwrap_or(0) >= records[0].started_unix_secs);
        assert!(session.drain_lifecycle_records().is_empty());
    }

    #[test]
    fn lifecycle_second_start_flushes_first_unfinished() {
        let mut session = bash_session();
        let token = session
            .lifecycle_token_for_test()
            .expect("bash has a token");
        establish_first_prompt(&mut session, &token);
        session.advance_output(&lifecycle_start(&token, "true"));
        session.advance_output(&lifecycle_start(&token, "false"));
        session.advance_output(&lifecycle_prompt(&token, "1"));
        let records = session.drain_lifecycle_records();
        assert_eq!(records.len(), 2);
        assert_eq!(records[0].command, "true");
        assert_eq!(records[0].finished_unix_secs, None);
        assert_eq!(records[0].exit_status, None);
        assert_eq!(records[1].command, "false");
        assert_eq!(records[1].exit_status, Some(1));
    }

    #[test]
    fn lifecycle_spoofed_token_changes_nothing() {
        let mut session = bash_session();
        session.advance_output(b"\x1b]133;B;attacker;Y21k\x07");
        session.advance_output(b"\x1b]133;A;attacker;0\x07");
        assert!(!session.prompt_ready());
        assert!(session.drain_lifecycle_records().is_empty());
        // The real token still works afterwards.
        let token = session
            .lifecycle_token_for_test()
            .expect("bash has a token");
        session.advance_output(&lifecycle_prompt(&token, "0"));
        assert!(session.prompt_ready());
    }

    #[test]
    fn lifecycle_legacy_prompt_sets_readiness_and_closes_pending_without_status() {
        let mut session = bash_session();
        let token = session
            .lifecycle_token_for_test()
            .expect("bash has a token");
        establish_first_prompt(&mut session, &token);
        session.advance_output(&lifecycle_start(&token, "legacy-cmd"));
        let legacy = format!("\x1b]133;A;{token}\x07").into_bytes();
        session.advance_output(&legacy);
        assert!(session.prompt_ready());
        let records = session.drain_lifecycle_records();
        assert_eq!(records.len(), 1);
        assert_eq!(records[0].command, "legacy-cmd");
        assert_eq!(records[0].exit_status, None);
    }

    #[test]
    fn lifecycle_out_of_range_exit_sets_readiness_but_keeps_pending() {
        let mut session = bash_session();
        let token = session
            .lifecycle_token_for_test()
            .expect("bash has a token");
        establish_first_prompt(&mut session, &token);
        session.advance_output(&lifecycle_start(&token, "weird"));
        session.advance_output(&lifecycle_prompt(&token, "999"));
        assert!(session.prompt_ready(), "readiness is fail-open");
        assert!(
            session.drain_lifecycle_records().is_empty(),
            "completion is fail-closed"
        );
        session.advance_output(&lifecycle_prompt(&token, "3"));
        let records = session.drain_lifecycle_records();
        assert_eq!(records.len(), 1);
        assert_eq!(records[0].exit_status, Some(3));
    }

    #[test]
    fn lifecycle_starts_before_first_prompt_are_dropped_as_initialization() {
        let mut session = bash_session();
        let token = session
            .lifecycle_token_for_test()
            .expect("bash has a token");
        // rcfile/dotfile lines fire the DEBUG trap before the first prompt.
        session.advance_output(&lifecycle_start(&token, "printf ''"));
        session.advance_output(&lifecycle_prompt(&token, "0"));
        assert!(session.prompt_ready());
        assert!(session.drain_lifecycle_records().is_empty());
        // Commands after the first prompt record normally.
        session.advance_output(&lifecycle_start(&token, "real"));
        session.advance_output(&lifecycle_prompt(&token, "0"));
        assert_eq!(session.drain_lifecycle_records().len(), 1);
    }

    #[test]
    fn history_recording_is_disabled_by_default() {
        let mut session = test_session();
        assert!(!session.history_enabled());
        session.advance_output(b"before opt-in\r\n");
        session.resize(100, 30);
        assert!(session.history_snapshot().is_empty());
    }

    #[test]
    fn history_enable_records_output_and_resize_without_backfill() {
        let mut session = test_session();
        session.advance_output(b"before opt-in\r\n");
        session.set_history_enabled(true);
        assert!(session.history_enabled());
        session.advance_output(b"after opt-in\r\n");
        session.resize(100, 30);
        let snapshot = session.history_snapshot();
        let combined: Vec<u8> = snapshot
            .iter()
            .filter_map(|event| match event {
                crate::history::RecordedEvent::Output(bytes) => Some(bytes.clone()),
                crate::history::RecordedEvent::Resize { .. } => None,
            })
            .flatten()
            .collect();
        let text = String::from_utf8_lossy(&combined);
        assert!(
            text.contains("after opt-in"),
            "records post-opt-in: {text:?}"
        );
        assert!(!text.contains("before opt-in"), "never backfills: {text:?}");
        assert!(
            snapshot.iter().any(|event| matches!(
                event,
                crate::history::RecordedEvent::Resize {
                    cols: 100,
                    rows: 30
                }
            )),
            "main-screen resize recorded"
        );
        session.set_history_enabled(false);
        assert!(session.history_snapshot().is_empty(), "disable discards");
    }

    #[test]
    fn history_pause_holds_the_record_and_resume_continues() {
        let mut session = test_session();
        session.set_history_enabled(true);
        session.advance_output(b"first\r\n");
        session.set_history_paused(true);
        assert!(session.history_paused());
        session.advance_output(b"while paused\r\n");
        session.set_history_paused(false);
        session.advance_output(b"resumed\r\n");
        let snapshot = session.history_snapshot();
        let combined: Vec<u8> = snapshot
            .iter()
            .filter_map(|event| match event {
                crate::history::RecordedEvent::Output(bytes) => Some(bytes.clone()),
                crate::history::RecordedEvent::Resize { .. } => None,
            })
            .flatten()
            .collect();
        let text = String::from_utf8_lossy(&combined);
        assert!(text.contains("first") && text.contains("resumed"));
        assert!(
            !text.contains("while paused"),
            "paused output dropped: {text:?}"
        );
    }

    #[test]
    fn history_session_drops_alt_screen_periods_end_to_end() {
        let mut session = test_session();
        session.set_history_enabled(true);
        session.advance_output(b"main-before\r\n");
        session.advance_output(b"\x1b[?1049h");
        session.advance_output(b"alt-content\r\n");
        session.advance_output(b"\x1b[?1049l");
        session.advance_output(b"main-after\r\n");
        let snapshot = session.history_snapshot();
        let combined: Vec<u8> = snapshot
            .iter()
            .filter_map(|event| match event {
                crate::history::RecordedEvent::Output(bytes) => Some(bytes.clone()),
                crate::history::RecordedEvent::Resize { .. } => None,
            })
            .flatten()
            .collect();
        let text = String::from_utf8_lossy(&combined);
        assert!(text.contains("main-before") && text.contains("main-after"));
        assert!(!text.contains("alt-content"), "alt bytes dropped: {text:?}");
    }

    #[test]
    fn start_history_with_seed_merges_restored_prefix_and_live_output() {
        let mut session = test_session();
        let seed = vec![
            crate::history::RecordedEvent::Output(b"pre-restart line\r\n".to_vec()),
            crate::history::RecordedEvent::Resize {
                cols: 100,
                rows: 30,
            },
        ];
        session.start_history_with_seed(&seed);
        assert!(session.history_enabled());
        session.advance_output(b"post-restart line\r\n");
        let snapshot = session.history_snapshot();
        assert!(snapshot.len() >= 3, "seed plus live output");
        let combined: Vec<u8> = snapshot
            .iter()
            .filter_map(|event| match event {
                crate::history::RecordedEvent::Output(bytes) => Some(bytes.clone()),
                crate::history::RecordedEvent::Resize { .. } => None,
            })
            .flatten()
            .collect();
        let text = String::from_utf8_lossy(&combined);
        assert!(text.contains("pre-restart line") && text.contains("post-restart line"));
    }

    #[test]
    fn non_bash_shell_emits_no_lifecycle_records() {
        let mut session = test_session();
        assert!(!session.emits_lifecycle());
        session.advance_output(b"\x1b]133;B;whatever;Y21k\x07");
        session.advance_output(b"\x1b]133;A;whatever;0\x07");
        assert!(!session.prompt_ready());
        assert!(session.drain_lifecycle_records().is_empty());
    }

    #[test]
    fn lifecycle_record_queue_is_bounded() {
        let mut session = bash_session();
        let token = session
            .lifecycle_token_for_test()
            .expect("bash has a token");
        establish_first_prompt(&mut session, &token);
        for index in 0..700 {
            let start = lifecycle_start(&token, &format!("cmd{index}"));
            session.advance_output(&start);
        }
        let records = session.drain_lifecycle_records();
        assert!(records.len() <= 512, "queue capped, got {}", records.len());
        // cmd699 is still pending (no prompt yet); the newest *record* is
        // the flushed predecessor.
        assert_eq!(records.last().expect("records remain").command, "cmd698");
        session.advance_output(&lifecycle_prompt(&token, "0"));
        let records = session.drain_lifecycle_records();
        assert_eq!(records.len(), 1);
        assert_eq!(records[0].command, "cmd699");
        assert_eq!(records[0].exit_status, Some(0));
    }
}
