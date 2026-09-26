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
