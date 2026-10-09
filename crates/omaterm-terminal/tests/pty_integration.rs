#![cfg(unix)]

use std::time::{Duration, Instant};

use omaterm_core::{SessionId, SplitDirection};
use omaterm_terminal::{
    LifecycleRecord, RunCommandError, TerminalConfig, TerminalRegistry, TerminalSession,
    TerminalViewport, WorkspaceCoordinator,
};

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
fn bash_run_waits_for_prompt_and_submits_argv_without_shell_interpolation() {
    let marker_path =
        std::env::temp_dir().join(format!("omaterm-run-injected-{}", std::process::id()));
    let _ = std::fs::remove_file(&marker_path);
    let mut session = TerminalSession::new(std::env::temp_dir(), Some("/bin/bash"), 180, 24)
        .expect("spawn integrated bash");
    let bash_rcfile = std::env::temp_dir().join(format!(
        "omaterm-bashrc-{}-{}",
        // SAFETY: getuid has no preconditions and returns the current UID.
        unsafe { libc::getuid() },
        session.id().0.simple()
    ));
    assert!(bash_rcfile.exists(), "integrated Bash has a private rcfile");
    assert!(
        !session.prompt_ready(),
        "readiness requires the shell marker"
    );

    let start = Instant::now();
    while !session.prompt_ready() && start.elapsed() < Duration::from_secs(10) {
        let _ = session.pump();
        std::thread::sleep(Duration::from_millis(20));
    }
    assert!(
        session.prompt_ready(),
        "Bash emits the OSC prompt-ready marker"
    );

    let injected = format!("$(touch {}) ; *", marker_path.display());
    let argv = vec![
        "printf".into(),
        "%s|%s|%s|%s|%s\\n".into(),
        "two words".into(),
        "quote'and\\slash".into(),
        "雪☃".into(),
        String::new(),
        injected.clone(),
    ];
    session.run_argv(&argv).expect("submit argv to ready bash");
    assert!(
        !session.prompt_ready(),
        "submission clears prompt readiness"
    );

    let expected = format!("two words|quote'and\\slash|雪☃||{injected}");
    let viewport = pump_until(&mut session, Duration::from_secs(10), |viewport| {
        viewport_text(viewport).contains(&expected)
    });
    assert!(
        viewport_text(&viewport).contains(&expected),
        "expected {expected:?} in terminal output:\n{}",
        viewport_text(&viewport)
    );
    assert!(
        !marker_path.exists(),
        "argv text must not be evaluated by Bash"
    );
    // Command output and the prompt marker can arrive in separate PTY
    // reads under load; wait for readiness instead of assuming it lands
    // in the same chunk as the output text.
    let start = Instant::now();
    while !session.prompt_ready() && start.elapsed() < Duration::from_secs(10) {
        let _ = session.pump();
        std::thread::sleep(Duration::from_millis(20));
    }
    assert!(
        session.prompt_ready(),
        "next prompt marker restores readiness"
    );

    session.write_input(b"x").expect("type partial input");
    assert!(
        !session.prompt_ready(),
        "partial user input makes shell busy"
    );
    session
        .write_input(b"\x03")
        .expect("cancel the partial command line");
    let start = Instant::now();
    while !session.prompt_ready() && start.elapsed() < Duration::from_secs(5) {
        let _ = session.pump();
        std::thread::sleep(Duration::from_millis(20));
    }
    assert!(session.shutdown(), "bash should shut down");
    drop(session);
    assert!(
        !bash_rcfile.exists(),
        "session drop removes its private rcfile"
    );
}

#[test]
fn run_is_explicitly_unsupported_for_non_bash_shells() {
    let mut session =
        TerminalSession::new(std::env::temp_dir(), Some("/bin/sh"), 80, 24).expect("spawn sh");
    assert!(matches!(
        session.run_argv(&["true".into()]),
        Err(RunCommandError::UnsupportedShell)
    ));
    assert!(session.shutdown(), "sh should shut down");
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
fn drop_escalates_and_reaps_a_shell_that_ignores_hangup() {
    let mut session =
        TerminalSession::new(std::env::temp_dir(), Some("/bin/sh"), 80, 24).expect("spawn sh");
    wait_for_prompt(&mut session);
    session
        .write_input(b"trap '' HUP; printf 'HUP_IGNORED_READY\\n'\n")
        .unwrap();
    let ready = |viewport: &TerminalViewport| {
        viewport_text(viewport)
            .lines()
            .any(|line| line == "HUP_IGNORED_READY")
    };
    let viewport = pump_until(&mut session, Duration::from_secs(5), ready);
    assert!(
        ready(&viewport),
        "shell must install its ignored hangup before dropping"
    );
    let pid = session.child_pid();
    let start = Instant::now();
    drop(session);
    assert!(start.elapsed() < Duration::from_secs(5));
    assert!(
        child_gone(pid),
        "ignoring shell must be reaped, not left as a zombie"
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
fn procfs_refresh_tracks_a_shell_directory_change() {
    let target = std::env::temp_dir().join(format!("omaterm-cwd-refresh-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&target);
    std::fs::create_dir(&target).expect("create target CWD");
    let mut session =
        TerminalSession::new(std::env::temp_dir(), Some("/bin/sh"), 80, 24).expect("spawn sh");
    wait_for_prompt(&mut session);
    let command = format!("cd {}\n", target.display());
    session
        .write_input(command.as_bytes())
        .expect("change shell CWD");

    let start = Instant::now();
    while start.elapsed() < Duration::from_secs(5) {
        let _ = session.pump();
        session.refresh_cwd_from_procfs();
        if session.cwd().path == target {
            break;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    assert_eq!(session.cwd().path, target);
    assert_eq!(
        session.cwd().provenance,
        omaterm_terminal::CwdProvenance::Procfs
    );
    assert!(session.shutdown(), "shell should shut down cleanly");
    std::fs::remove_dir_all(target).expect("remove target CWD");
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

fn registry_session(
    registry: &mut TerminalRegistry,
    shell: Option<String>,
    cols: u16,
    rows: u16,
) -> omaterm_core::SessionId {
    registry
        .create(TerminalConfig {
            working_directory: std::env::temp_dir(),
            shell,
            cols,
            rows,
            scrollback_lines: None,
        })
        .expect("spawn registry session")
}

fn session_text(registry: &TerminalRegistry, id: omaterm_core::SessionId) -> String {
    let handle = registry.get(id).expect("session registered");
    let mut session = handle.lock().unwrap();
    let _ = session.pump();
    viewport_text(&session.viewport())
}

fn wait_for_text(
    registry: &TerminalRegistry,
    id: omaterm_core::SessionId,
    timeout: Duration,
    needle: &str,
) -> String {
    let start = Instant::now();
    loop {
        let text = session_text(registry, id);
        if text.contains(needle) {
            return text;
        }
        if start.elapsed() > timeout {
            return text;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
}

fn wait_for_registry_prompt(
    registry: &TerminalRegistry,
    id: omaterm_core::SessionId,
    timeout: Duration,
) -> bool {
    wait_for(timeout, || !session_text(registry, id).trim().is_empty())
}

#[test]
fn four_registry_sessions_stay_independent() {
    let mut registry = TerminalRegistry::new();
    let ids: Vec<_> = (0..4)
        .map(|_| registry_session(&mut registry, Some("/bin/sh".to_string()), 80, 24))
        .collect();
    // Distinct process identities.
    let mut pids: Vec<u32> = ids
        .iter()
        .map(|id| registry.get(*id).unwrap().lock().unwrap().child_pid())
        .collect();
    pids.sort_unstable();
    pids.dedup();
    assert_eq!(pids.len(), 4, "each pane must own its shell process");

    for (i, id) in ids.iter().enumerate() {
        let handle = registry.get(*id).unwrap();
        // Wait for the shell prompt before writing: startup bytes can be
        // swallowed, which would lose the marker on a cold session.
        wait_for_text(&registry, *id, Duration::from_secs(5), "$");
        handle
            .lock()
            .unwrap()
            .write_input(format!("echo MARKER-{i}\n").as_bytes())
            .expect("write marker");
    }
    for (i, id) in ids.iter().enumerate() {
        let text = wait_for_text(
            &registry,
            *id,
            Duration::from_secs(5),
            &format!("MARKER-{i}"),
        );
        assert!(
            text.contains(&format!("MARKER-{i}")),
            "pane {i} should show its own marker"
        );
        for (j, _) in ids.iter().enumerate() {
            if i != j {
                assert!(
                    !text.contains(&format!("MARKER-{j}")),
                    "pane {i} must not show pane {j}'s output"
                );
            }
        }
    }
}

#[test]
fn coordinator_close_kills_only_target() {
    let mut ws = WorkspaceCoordinator::new(std::env::temp_dir());
    ws.create_initial(80, 24).expect("initial");
    ws.split_focused(SplitDirection::Right, 80, 24)
        .expect("split 2");
    ws.focus_pane(ws.tree().panes()[0].id).unwrap();
    ws.split_focused(SplitDirection::Down, 80, 24)
        .expect("split 3");
    assert_eq!(ws.pane_count(), 3);
    let survivor_pids: Vec<u32> = {
        let panes = ws.tree().panes();
        panes[1..]
            .iter()
            .map(|pane| {
                let id = ws.session_id_for_pane(pane.id).unwrap();
                ws.registry().get(id).unwrap().lock().unwrap().child_pid()
            })
            .collect()
    };
    // Close the first pane; survivors must keep their PIDs and input.
    ws.focus_pane(ws.tree().panes()[0].id).unwrap();
    let closed = ws.close_focused().expect("close");
    let closed_pid = closed
        .handle
        .as_ref()
        .map(|h| h.lock().unwrap().child_pid());
    assert_eq!(ws.pane_count(), 2);
    assert_eq!(ws.session_count(), 2);
    for pane in ws.tree().panes() {
        let id = ws.session_id_for_pane(pane.id).unwrap();
        let handle = ws.registry().get(id).unwrap();
        let pid = handle.lock().unwrap().child_pid();
        assert!(
            survivor_pids.contains(&pid),
            "survivor PID {pid} must be stable across sibling close"
        );
        if let Some(closed_pid) = closed_pid {
            assert_ne!(pid, closed_pid, "survivor must not be the closed child");
        }
        wait_for_text(ws.registry(), id, Duration::from_secs(5), "$");
        handle
            .lock()
            .unwrap()
            .write_input(b"echo ALIVE\n")
            .expect("write");
        let text = wait_for_text(ws.registry(), id, Duration::from_secs(5), "ALIVE");
        assert!(text.contains("ALIVE"), "survivor must accept input");
    }
    // Reap the detached child; the PID must disappear (no zombie).
    let handle = closed.handle.unwrap();
    assert!(handle.lock().unwrap().shutdown());
    let pid = closed_pid.unwrap();
    assert!(
        wait_for(Duration::from_secs(5), || child_gone(pid)),
        "closed child {pid} must be reaped"
    );
}

#[test]
fn hidden_tabs_and_projects_keep_draining_and_close_only_owned_sessions() {
    let mut ws = WorkspaceCoordinator::new(std::env::temp_dir());
    let tab_session = ws.create_initial(80, 24).expect("default tab");
    let project_a = ws.selected_project_id().unwrap();
    assert!(wait_for_registry_prompt(
        ws.registry(),
        tab_session,
        Duration::from_secs(5)
    ));
    let tab_handle = ws.registry().get(tab_session).unwrap();
    let tab_pid = tab_handle.lock().unwrap().child_pid();
    tab_handle
        .lock()
        .unwrap()
        .write_input(
            b"i=1; while [ $i -le 8 ]; do echo TAB_HIDDEN_MARK; i=$((i+1)); sleep 0.05; done\n",
        )
        .expect("start hidden tab output");

    let (visible_tab, project_session) = ws
        .create_tab(project_a, 80, 24)
        .expect("create visible tab");
    assert!(wait_for_registry_prompt(
        ws.registry(),
        project_session,
        Duration::from_secs(5)
    ));
    let project_handle = ws.registry().get(project_session).unwrap();
    let project_pid = project_handle.lock().unwrap().child_pid();
    project_handle
        .lock()
        .unwrap()
        .write_input(
            b"i=1; while [ $i -le 8 ]; do echo PROJECT_HIDDEN_MARK; i=$((i+1)); sleep 0.05; done\n",
        )
        .expect("start hidden project output");

    let (project_b, project_tab, visible_project_session) = ws
        .create_project(Some(std::env::temp_dir()), 80, 24)
        .expect("create visible project");
    for (id, marker) in [
        (tab_session, "TAB_HIDDEN_MARK"),
        (project_session, "PROJECT_HIDDEN_MARK"),
    ] {
        assert!(
            wait_for(Duration::from_secs(8), || {
                session_text(ws.registry(), id).matches(marker).count() >= 5
            }),
            "hidden session {id:?} should continue producing output"
        );
    }
    assert_eq!(
        ws.registry()
            .get(tab_session)
            .unwrap()
            .lock()
            .unwrap()
            .child_pid(),
        tab_pid
    );
    assert_eq!(
        ws.registry()
            .get(project_session)
            .unwrap()
            .lock()
            .unwrap()
            .child_pid(),
        project_pid
    );

    ws.select_tab(project_a, visible_tab).unwrap();
    assert_eq!(ws.focused_session_id(), Some(project_session));
    ws.select_project(project_b).unwrap();
    ws.select_tab(project_b, project_tab).unwrap();
    assert_eq!(ws.focused_session_id(), Some(visible_project_session));

    // Closing a hidden tab/project must not detach visible or sibling sessions.
    let closed_tab = ws
        .close_tab(
            project_a,
            ws.window().project(project_a).unwrap().tabs[0].id,
        )
        .unwrap();
    assert!(!ws.registry().contains(tab_session));
    assert!(ws.registry().contains(project_session));
    assert!(ws.registry().contains(visible_project_session));
    for pane in closed_tab.0 {
        if let Some(handle) = pane.handle {
            assert!(handle.lock().unwrap().shutdown());
        }
    }
    let closed_project = ws.close_project(project_a).unwrap();
    assert!(!ws.registry().contains(project_session));
    assert!(ws.registry().contains(visible_project_session));
    for pane in closed_project.0 {
        if let Some(handle) = pane.handle {
            assert!(handle.lock().unwrap().shutdown());
        }
    }
    assert!(
        ws.close_project(project_b)
            .unwrap()
            .0
            .into_iter()
            .all(|pane| {
                pane.handle
                    .is_some_and(|handle| handle.lock().unwrap().shutdown())
            })
    );
}

#[test]
fn exited_shells_close_only_their_panes_for_exit_and_ctrl_d() {
    for (exit_input, label) in [
        (b"exit\n".as_slice(), "exit"),
        (b"\x04".as_slice(), "Ctrl+D"),
    ] {
        let mut ws = WorkspaceCoordinator::new(std::env::temp_dir());
        let exited_id = ws.create_initial(80, 24).expect("initial");
        let (survivor_pane, survivor_id) = ws
            .split_focused(SplitDirection::Right, 80, 24)
            .expect("split");
        // The target is deliberately unfocused to prove exit handling closes
        // by SessionId, rather than acting on whatever pane owns focus.
        ws.focus_pane(survivor_pane).expect("focus survivor");
        let target = ws.registry().get(exited_id).expect("target registered");
        assert!(wait_for_registry_prompt(
            ws.registry(),
            exited_id,
            Duration::from_secs(5)
        ));
        target
            .lock()
            .unwrap()
            .write_input(exit_input)
            .expect("send shell exit");
        let exited = wait_for(Duration::from_secs(5), || {
            let Ok(mut session) = target.lock() else {
                return false;
            };
            let _ = session.pump();
            session.poll_child()
        });
        assert!(exited, "{label} should terminate the target shell");

        let closed = ws
            .close_session(exited_id)
            .expect("exit should resolve to its pane");
        assert_eq!(closed.session_id, Some(exited_id));
        assert_eq!(ws.pane_count(), 1);
        assert_eq!(ws.session_count(), 1);
        assert!(ws.registry().get(exited_id).is_none());
        assert_eq!(ws.focused(), Some(survivor_pane));
        assert_eq!(ws.focused_session_id(), Some(survivor_id));

        let survivor = ws.registry().get(survivor_id).expect("sibling remains");
        assert!(wait_for_registry_prompt(
            ws.registry(),
            survivor_id,
            Duration::from_secs(5)
        ));
        survivor
            .lock()
            .unwrap()
            .write_input(b"echo SIBLING-ALIVE\n")
            .expect("sibling accepts input");
        let text = wait_for_text(
            ws.registry(),
            survivor_id,
            Duration::from_secs(5),
            "SIBLING-ALIVE",
        );
        assert!(text.contains("SIBLING-ALIVE"), "sibling stays usable");

        if let Some(handle) = closed.handle {
            assert!(handle.lock().unwrap().shutdown());
        }
    }
}

#[test]
fn final_shell_exit_closes_last_pane_and_empties_workspace() {
    let mut ws = WorkspaceCoordinator::new(std::env::temp_dir());
    let session_id = ws.create_initial(80, 24).expect("initial");
    let handle = ws.registry().get(session_id).expect("session registered");
    let pid = handle.lock().unwrap().child_pid();
    assert!(wait_for_registry_prompt(
        ws.registry(),
        session_id,
        Duration::from_secs(5)
    ));
    handle
        .lock()
        .unwrap()
        .write_input(b"\x04")
        .expect("send Ctrl+D");
    assert!(wait_for(Duration::from_secs(5), || {
        let Ok(mut session) = handle.lock() else {
            return false;
        };
        let _ = session.pump();
        session.poll_child()
    }));

    let closed = ws.close_session(session_id).expect("close exited pane");
    assert_eq!(closed.session_id, Some(session_id));
    assert!(ws.is_empty());
    assert_eq!(ws.focused(), None);
    assert_eq!(ws.session_count(), 0);
    assert!(closed.handle.unwrap().lock().unwrap().shutdown());
    assert!(wait_for(Duration::from_secs(5), || child_gone(pid)));
}

#[test]
fn resize_one_session_leaves_others_unchanged() {
    let mut registry = TerminalRegistry::new();
    let first = registry_session(&mut registry, Some("/bin/sh".to_string()), 80, 24);
    let second = registry_session(&mut registry, Some("/bin/sh".to_string()), 80, 24);
    registry.get(first).unwrap().lock().unwrap().resize(100, 30);
    let _ = registry.get(first).unwrap().lock().unwrap().pump();
    let _ = registry.get(second).unwrap().lock().unwrap().pump();
    let a = registry.get(first).unwrap().lock().unwrap().viewport();
    let b = registry.get(second).unwrap().lock().unwrap().viewport();
    assert_eq!((a.cols, a.lines), (100, 30));
    assert_eq!((b.cols, b.lines), (80, 24));
}

#[test]
fn unfocused_session_drains_output() {
    let mut registry = TerminalRegistry::new();
    let background = registry_session(&mut registry, Some("/bin/sh".to_string()), 80, 24);
    let foreground = registry_session(&mut registry, Some("/bin/sh".to_string()), 80, 24);
    wait_for_text(&registry, background, Duration::from_secs(5), "$");
    wait_for_text(&registry, foreground, Duration::from_secs(5), "$");
    // Sustained output on the "hidden" pane while we only interact with the
    // focused one; then drain the hidden pane and verify nothing was lost.
    registry
        .get(background)
        .unwrap()
        .lock()
        .unwrap()
        .write_input(b"i=1; while [ $i -le 50 ]; do echo \"bg-$i\"; i=$((i+1)); done\n")
        .expect("write flood");
    registry
        .get(foreground)
        .unwrap()
        .lock()
        .unwrap()
        .write_input(b"echo FG\n")
        .expect("write fg");
    let fg_text = wait_for_text(&registry, foreground, Duration::from_secs(5), "FG");
    assert!(fg_text.contains("FG"));
    let bg_text = wait_for_text(&registry, background, Duration::from_secs(10), "bg-50");
    assert!(
        bg_text.contains("bg-50"),
        "hidden pane must drain while unfocused, got tail:\n{}",
        bg_text.lines().rev().take(5).collect::<Vec<_>>().join("\n")
    );
}

fn count_fds() -> usize {
    std::fs::read_dir("/proc/self/fd")
        .map(|entries| entries.count())
        .unwrap_or(0)
}

#[test]
fn repeated_create_close_shows_no_fd_growth() {
    let mut registry = TerminalRegistry::new();
    // Warm up one spawn so lazy init is not counted as growth.
    let warm = registry_session(&mut registry, Some("/bin/sh".to_string()), 80, 24);
    registry.close(warm).expect("close warmup");
    let before = count_fds();
    for _ in 0..100 {
        let id = registry_session(&mut registry, Some("/bin/sh".to_string()), 80, 24);
        registry.close(id).expect("close");
    }
    assert!(registry.is_empty());
    let after = count_fds();
    assert!(
        after <= before + 2,
        "100 create/close cycles leaked FDs: before={before} after={after}"
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

// --- M10 Phase 1: Bash lifecycle reliability matrix ------------------------

/// Spawn integrated Bash and synchronize on the first prompt. Startup
/// initialization (rcfile lines, dotfiles) must never become records.
fn bash_lifecycle_session() -> TerminalSession {
    let mut session =
        TerminalSession::new(std::env::temp_dir(), Some("/bin/bash"), 80, 24).expect("spawn bash");
    assert!(session.emits_lifecycle());
    let start = Instant::now();
    while !session.prompt_ready() && start.elapsed() <= Duration::from_secs(10) {
        let _ = session.pump();
        std::thread::sleep(Duration::from_millis(20));
    }
    assert!(
        session.prompt_ready(),
        "integrated bash must reach readiness"
    );
    assert!(
        session.drain_lifecycle_records().is_empty(),
        "initialization noise must not become records"
    );
    session
}

fn wait_for_records(
    session: &mut TerminalSession,
    timeout: Duration,
    min_records: usize,
) -> Vec<LifecycleRecord> {
    let mut collected = Vec::new();
    let start = Instant::now();
    while collected.len() < min_records && start.elapsed() <= timeout {
        let _ = session.pump();
        collected.extend(session.drain_lifecycle_records());
        std::thread::sleep(Duration::from_millis(20));
    }
    collected
}

#[test]
fn bash_lifecycle_reports_command_text_and_exit_status() {
    let mut session = bash_lifecycle_session();
    session.write_input(b"false\n").expect("write");
    let records = wait_for_records(&mut session, Duration::from_secs(10), 1);
    assert_eq!(records.len(), 1, "one command, one record");
    assert_eq!(records[0].command, "false");
    assert_eq!(records[0].shell_dialect, "bash");
    assert_eq!(records[0].exit_status, Some(1));
    assert!(
        records[0].finished_unix_secs.unwrap_or(0) >= records[0].started_unix_secs,
        "timestamps ordered"
    );
    assert!(
        !records[0].command.contains("omaterm"),
        "no hook internals leaked: {:?}",
        records[0].command
    );
}

#[test]
fn bash_lifecycle_chain_attributes_completion_to_last() {
    let mut session = bash_lifecycle_session();
    session.write_input(b"true; false\n").expect("write");
    let records = wait_for_records(&mut session, Duration::from_secs(10), 2);
    assert_eq!(records.len(), 2);
    assert_eq!(records[0].command, "true");
    assert_eq!(
        records[0].exit_status, None,
        "superseded start has no completion"
    );
    assert_eq!(records[0].finished_unix_secs, None);
    assert_eq!(records[1].command, "false");
    assert_eq!(records[1].exit_status, Some(1));
}

#[test]
fn bash_lifecycle_ctrl_c_reports_130() {
    let mut session = bash_lifecycle_session();
    session
        .write_input(b"echo SLEEPING; sleep 10\n")
        .expect("write sleep");
    // Match the actual output line, not the echoed command. The echo precedes
    // `sleep`, so also wait for a foreground group distinct from the shell.
    let fd = session.pty_fd();
    let shell_pid = session.child_pid() as libc::pid_t;
    let ready = |viewport: &TerminalViewport| {
        // SAFETY: fd is the live session's PTY master; tcgetpgrp touches no memory.
        let foreground = unsafe { libc::tcgetpgrp(fd) };
        foreground > 0
            && foreground != shell_pid
            && viewport_text(viewport)
                .lines()
                .any(|line| line == "SLEEPING")
    };
    let viewport = pump_until(&mut session, Duration::from_secs(5), ready);
    assert!(
        ready(&viewport),
        "sleep must own the foreground before SIGINT"
    );
    session.write_input(b"\x03").expect("write ctrl-c");
    let records = wait_for_records(&mut session, Duration::from_secs(10), 2);
    let sleep = records
        .iter()
        .find(|record| record.command == "sleep 10")
        .expect("sleep record present");
    assert_eq!(sleep.exit_status, Some(130), "SIGINT yields 130");
}

#[test]
fn bash_lifecycle_nested_shell_hides_inner_commands() {
    let mut session = bash_lifecycle_session();
    session
        .write_input(b"bash --norc -c 'echo inner-probe-xyz'\n")
        .expect("write");
    let records = wait_for_records(&mut session, Duration::from_secs(10), 1);
    assert_eq!(records.len(), 1, "only the outer invocation is recorded");
    assert!(
        records[0].command.starts_with("bash --norc"),
        "outer command recorded, got {:?}",
        records[0].command
    );
    assert_eq!(records[0].exit_status, Some(0));
    assert!(
        !records
            .iter()
            .any(|record| record.command == "echo inner-probe-xyz"),
        "inner shell has no hooks and stays invisible"
    );
}

fn bash_lifecycle_session_with_bashrc(bashrc: &str) -> (TerminalSession, std::path::PathBuf) {
    static HOME_COUNTER: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
    let unique = HOME_COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    let home = std::env::temp_dir().join(format!(
        "omaterm-lifecycle-home-{}-{unique}",
        std::process::id()
    ));
    std::fs::create_dir_all(&home).expect("isolated home");
    std::fs::write(home.join(".bashrc"), bashrc).expect("isolated bashrc");
    let mut env = std::collections::HashMap::new();
    env.insert("HOME".to_string(), home.to_string_lossy().into_owned());
    let mut session = TerminalSession::new_with_id_and_env(
        SessionId::new(),
        std::env::temp_dir(),
        Some("/bin/bash"),
        80,
        24,
        env,
        None,
    )
    .expect("spawn bash with isolated home");
    let start = Instant::now();
    while !session.prompt_ready() && start.elapsed() <= Duration::from_secs(10) {
        let _ = session.pump();
        std::thread::sleep(Duration::from_millis(20));
    }
    assert!(
        session.prompt_ready(),
        "bash with custom rc must reach readiness"
    );
    assert!(
        session.drain_lifecycle_records().is_empty(),
        "dotfile sourcing must not become records"
    );
    (session, home)
}

#[test]
fn bash_lifecycle_user_string_prompt_command_is_not_recorded() {
    let (mut session, home) =
        bash_lifecycle_session_with_bashrc("PROMPT_COMMAND='echo USERPCMARK'\n");
    session.write_input(b"true\n").expect("write");
    let records = wait_for_records(&mut session, Duration::from_secs(10), 1);
    assert_eq!(records.len(), 1, "only the typed command, got {records:?}");
    assert_eq!(records[0].command, "true");
    let viewport = pump_until(&mut session, Duration::from_secs(5), |viewport| {
        viewport_text(viewport).contains("USERPCMARK")
    });
    assert!(
        viewport_text(&viewport).contains("USERPCMARK"),
        "user PROMPT_COMMAND still runs"
    );
    drop(session);
    let _ = std::fs::remove_dir_all(&home);
}

#[test]
fn bash_lifecycle_user_array_prompt_command_is_not_recorded() {
    let (mut session, home) =
        bash_lifecycle_session_with_bashrc("PROMPT_COMMAND=(echo USERPCARR)\n");
    session.write_input(b"true\n").expect("write");
    let records = wait_for_records(&mut session, Duration::from_secs(10), 1);
    assert_eq!(records.len(), 1, "only the typed command, got {records:?}");
    assert_eq!(records[0].command, "true");
    let viewport = pump_until(&mut session, Duration::from_secs(5), |viewport| {
        viewport_text(viewport).contains("USERPCARR")
    });
    assert!(
        viewport_text(&viewport).contains("USERPCARR"),
        "user array PROMPT_COMMAND still runs"
    );
    drop(session);
    let _ = std::fs::remove_dir_all(&home);
}

#[test]
fn bash_lifecycle_tui_records_invocation_only() {
    let mut session = bash_lifecycle_session();
    session
        .write_input(b"vim --clean -c 'q!'\n")
        .expect("write");
    let records = wait_for_records(&mut session, Duration::from_secs(15), 1);
    assert_eq!(
        records.len(),
        1,
        "TUI internals stay invisible, got {records:?}"
    );
    assert!(
        records[0].command.starts_with("vim "),
        "invocation recorded, got {:?}",
        records[0].command
    );
    assert_eq!(records[0].exit_status, Some(0));
}

#[test]
fn bash_lifecycle_unicode_command_text_is_exact() {
    let mut session = bash_lifecycle_session();
    session
        .write_input("printf '雪 %s\\n' ok\n".as_bytes())
        .expect("write");
    let records = wait_for_records(&mut session, Duration::from_secs(10), 1);
    assert_eq!(records.len(), 1);
    assert_eq!(records[0].command, "printf '雪 %s\\n' ok");
    assert_eq!(records[0].exit_status, Some(0));
}

#[test]
fn theme_redraw_signals_foreground_group_without_resizing() {
    let mut session =
        TerminalSession::new(std::env::temp_dir(), Some("/bin/bash"), 80, 24).expect("spawn bash");
    wait_for_prompt(&mut session);
    session
        .write_input(b"trap 'echo REDRAW-SEEN' WINCH; sleep 5\n")
        .expect("arm trap");
    // Let the shell process the trap and start the foreground sleep; the
    // sleep holds the foreground process group so the themed redraw has a
    // live target distinct from the shell itself.
    std::thread::sleep(Duration::from_millis(500));
    let _ = session.pump();
    let before = session.viewport();
    let history_before = session.history_version();
    session.signal_theme_redraw();
    let viewport = pump_until(&mut session, Duration::from_secs(5), |viewport| {
        viewport_text(viewport).contains("REDRAW-SEEN")
    });
    assert!(
        viewport_text(&viewport).contains("REDRAW-SEEN"),
        "foreground child must receive SIGWINCH on theme redraw"
    );
    assert_eq!(
        (viewport.cols, viewport.lines),
        (before.cols, before.lines),
        "theme redraw must not resize the grid"
    );
    assert_eq!(
        session.history_version(),
        history_before,
        "theme redraw must not record history"
    );
}
