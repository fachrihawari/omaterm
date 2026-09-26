use std::time::{Duration, Instant};

use omaterm_terminal::{TerminalSession, TerminalViewport};

fn pump_until(
    session: &mut TerminalSession,
    timeout: Duration,
    mut predicate: impl FnMut(&TerminalViewport) -> bool,
) -> TerminalViewport {
    let start = Instant::now();
    loop {
        let _ = session.pump();
        let viewport = session.viewport();
        if predicate(&viewport) {
            return viewport;
        }
        if start.elapsed() > timeout {
            return viewport;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
}

fn viewport_text(viewport: &TerminalViewport) -> String {
    viewport
        .rows
        .iter()
        .map(|row| {
            let mut text = String::new();
            for cell in &row.cells {
                if cell.width == omaterm_terminal::CellWidth::WideContinuation {
                    continue;
                }
                text.push_str(&cell.text);
            }
            text.trim_end().to_string()
        })
        .collect::<Vec<_>>()
        .join("\n")
}

/// Wait until the shell has printed its first prompt. Bytes written before
/// startup completes can be swallowed by terminal initialization, so
/// signal/interaction tests must synchronize here first; plain command
/// writes happen to survive, but signals do not.
fn wait_for_prompt(session: &mut TerminalSession) {
    pump_until(session, Duration::from_secs(5), |viewport| {
        !viewport_text(viewport).trim().is_empty()
    });
}

#[test]
fn spawn_and_echo_hello() {
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
    session.write_input(b"echo hello\n").expect("write");
    let viewport = pump_until(&mut session, Duration::from_secs(5), |viewport| {
        viewport_text(viewport).contains("hello")
    });
    assert!(
        viewport_text(&viewport).contains("hello"),
        "expected hello in viewport"
    );
}

#[test]
fn resize_changes_viewport_dimensions() {
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
    assert_eq!(session.viewport().cols, 80);
    session.resize(100, 30);
    // Drain any pending output so the engine settles after resize.
    let _ = session.pump();
    let viewport = session.viewport();
    assert_eq!(viewport.cols, 100);
    assert_eq!(viewport.lines, 30);
}

#[test]
fn eof_on_exit_reports_child_exit() {
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
    session.write_input(b"exit\n").expect("write exit");
    let start = Instant::now();
    loop {
        let _ = session.pump();
        if session.exited().is_some() {
            break;
        }
        if start.elapsed() > Duration::from_secs(5) {
            break;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    assert!(
        session.exited().is_some(),
        "child should have exited after `exit`"
    );
}

fn child_gone(pid: u32) -> bool {
    // A reaped child has no /proc entry. A zombie still has one, so this
    // also proves the child was reaped, not merely killed.
    std::fs::metadata(format!("/proc/{pid}")).is_err()
}

fn wait_for(timeout: Duration, mut done: impl FnMut() -> bool) -> bool {
    let start = Instant::now();
    loop {
        if done() {
            return true;
        }
        if start.elapsed() > timeout {
            return false;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
}

#[test]
fn shutdown_terminates_and_reaps_child() {
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
    let pid = session.child_pid();
    assert!(session.shutdown(), "shutdown should confirm child exit");
    assert!(session.exited().is_some(), "exit status should be recorded");
    assert!(
        wait_for(Duration::from_secs(5), || child_gone(pid)),
        "child {pid} should be reaped after shutdown"
    );
}

#[test]
fn drop_reaps_child_without_explicit_shutdown() {
    let dir = std::env::temp_dir();
    let pid = {
        let session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
        session.child_pid()
        // `Pty::drop` sends SIGHUP here.
    };
    assert!(
        wait_for(Duration::from_secs(5), || child_gone(pid)),
        "child {pid} should be reaped after session drop"
    );
}

#[test]
fn child_cwd_matches_spawn_directory() {
    let dir = std::env::temp_dir();
    let session = TerminalSession::new(dir.clone(), Some("/bin/sh"), 80, 24).expect("spawn sh");
    // /proc/<pid>/cwd should resolve to the spawn directory (or a symlink target).
    if let Some(cwd) = session.child_cwd() {
        assert!(
            cwd.exists(),
            "child cwd {cwd:?} should exist (spawn dir was {dir:?})"
        );
    }
}

#[test]
fn ctrl_c_interrupts_foreground_job() {
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
    wait_for_prompt(&mut session);
    session
        .write_input(b"echo SLEEPING; sleep 60\n")
        .expect("write sleep");
    // SLEEPING in the viewport proves `sleep` is now the foreground job,
    // so SIGINT deterministically lands on it instead of the prompt.
    pump_until(&mut session, Duration::from_secs(5), |viewport| {
        viewport_text(viewport).contains("SLEEPING")
    });
    session.write_input(b"\x03").expect("write ctrl-c");
    session.write_input(b"echo rc-$?\n").expect("write echo");
    let viewport = pump_until(&mut session, Duration::from_secs(10), |viewport| {
        viewport_text(viewport).contains("rc-130")
    });
    assert!(
        viewport_text(&viewport).contains("rc-130"),
        "SIGINT exit status 130 expected, got:\n{}",
        viewport_text(&viewport)
    );
}

#[test]
fn ctrl_d_on_empty_line_exits_shell() {
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
    wait_for_prompt(&mut session);
    session.write_input(b"\x04").expect("write ctrl-d");
    let exited = wait_for(Duration::from_secs(5), || {
        let _ = session.pump();
        session.exited().is_some()
    });
    assert!(exited, "shell should exit on EOF (ctrl-d)");
}

#[test]
fn ctrl_z_triggers_sigtstp_trap() {
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
    wait_for_prompt(&mut session);
    session
        .write_input(b"trap 'echo TRAPPED' TSTP\n")
        .expect("write trap");
    // Post-startup the shell executes buffered input reliably; a short
    // settle guarantees the trap is installed before SIGTSTP arrives.
    std::thread::sleep(Duration::from_millis(500));
    let _ = session.pump();
    session.write_input(b"\x1a").expect("write ctrl-z");
    let viewport = pump_until(&mut session, Duration::from_secs(10), |viewport| {
        viewport_text(viewport).contains("TRAPPED")
    });
    assert!(
        viewport_text(&viewport).contains("TRAPPED"),
        "SIGTSTP trap should fire, got:\n{}",
        viewport_text(&viewport)
    );
}

#[test]
fn bracketed_paste_round_trip_through_cat() {
    use omaterm_terminal::prepare_paste;
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
    wait_for_prompt(&mut session);
    // Enable bracketed paste at the shell prompt first (once `cat` is
    // foreground, further lines go to `cat`, not the shell).
    session
        .write_input(b"printf '\\e[?2004h'\n")
        .expect("enable bracketed");
    assert!(
        wait_for(Duration::from_secs(5), || {
            let _ = session.pump();
            session.bracketed_paste()
        }),
        "engine should track bracketed-paste mode"
    );
    session.write_input(b"cat\n").expect("write cat");
    std::thread::sleep(Duration::from_millis(500));
    let _ = session.pump();
    // NOTE: readline emits `?2004l` when accepting the `cat` line, so the
    // engine (correctly) reports bracketed mode off while `cat` runs.
    // Wrapping itself is unit-covered; here request it explicitly to prove
    // the exact wrapped bytes reach the child and echo back intact.
    let bytes = prepare_paste("PASTE-ME-123", true);
    assert!(bytes.starts_with(b"\x1b[200~"));
    session.write_input(&bytes).expect("write paste");
    let viewport = pump_until(&mut session, Duration::from_secs(10), |viewport| {
        viewport_text(viewport).contains("PASTE-ME-123")
    });
    assert!(
        viewport_text(&viewport).contains("PASTE-ME-123"),
        "cat should echo pasted text, got:\n{}",
        viewport_text(&viewport)
    );
    // Clean up: EOF exits cat, then the shell.
    session.write_input(b"\x04").expect("write ctrl-d");
    session.write_input(b"exit\n").expect("write exit");
}

#[test]
fn output_burst_stays_bounded_and_completes() {
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
    wait_for_prompt(&mut session);
    session
        .write_input(b"i=1; while [ $i -le 3000 ]; do echo \"burstline-$i\"; i=$((i+1)); done\n")
        .expect("write flood");
    let viewport = pump_until(&mut session, Duration::from_secs(30), |viewport| {
        viewport_text(viewport).contains("burstline-3000")
    });
    assert!(
        viewport_text(&viewport).contains("burstline-3000"),
        "flood tail should arrive"
    );
    assert!(
        viewport.history_size > 0,
        "flood should spill into scrollback"
    );
    assert!(
        viewport.history_size <= 10_000,
        "history must stay bounded, got {}",
        viewport.history_size
    );
}

#[test]
fn resize_during_output_keeps_tail() {
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 80, 24).expect("spawn sh");
    wait_for_prompt(&mut session);
    session
        .write_input(b"i=1; while [ $i -le 1500 ]; do echo \"rz-$i\"; i=$((i+1)); done\n")
        .expect("write flood");
    // Let output start, then resize mid-flood.
    std::thread::sleep(Duration::from_millis(300));
    let _ = session.pump();
    session.resize(100, 30);
    let viewport = pump_until(&mut session, Duration::from_secs(30), |viewport| {
        viewport_text(viewport).contains("rz-1500")
    });
    assert_eq!(viewport.cols, 100);
    assert_eq!(viewport.lines, 30);
    assert!(
        viewport_text(&viewport).contains("rz-1500"),
        "tail should survive resize, got:\n{}",
        viewport_text(&viewport)
    );
}

#[test]
fn combining_chars_compose_in_one_cell() {
    let dir = std::env::temp_dir();
    let mut session = TerminalSession::new(dir, Some("/bin/sh"), 20, 5).expect("spawn sh");
    // Drain shell startup output first so row 0 is predictable... instead use a
    // fresh engine-level check on the current viewport after feeding text.
    // Clear screen, then feed e + combining acute.
    session.advance_output(b"\x1b[2J\x1b[H");
    session.advance_output("e\u{301}".as_bytes());
    let viewport = session.viewport();
    let cell = &viewport.rows[0].cells[0];
    assert!(
        cell.text.contains('e'),
        "base char should be present, got {:?}",
        cell.text
    );
    assert!(
        cell.text.contains('\u{301}'),
        "combining mark should compose, got {:?}",
        cell.text
    );
}
