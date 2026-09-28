//! `omaterm` — thin CLI/launcher over the M8 Unix socket.
//!
//! No workspace logic lives here. Subcommands build one `IpcRequest`,
//! send it to the running desktop, and format the `IpcResponse`. With no
//! subcommand the binary launches `omaterm-desktop` (sibling binary) or
//! acknowledges a running instance. Compositor focus is deferred (non-goal).

mod commands;
mod connection;
mod launcher;
mod output;

use std::path::PathBuf;

use clap::{Parser, Subcommand};
use omaterm_protocol::{IpcRequest, PROTOCOL_VERSION};

use commands::WireCall;
use commands::history::HistoryCmd;
use commands::pane::PaneCmd;
use commands::project::ProjectCmd;
use commands::tab::TabCmd;
use commands::terminal::TerminalCmd;

#[derive(Debug, Parser)]
#[command(
    name = "omaterm",
    version,
    about = "Semantic control for the OmaTerm terminal workspace"
)]
struct Cli {
    /// Machine-readable JSON output (also valid after the subcommand).
    #[arg(long, global = true)]
    json: bool,
    /// Override socket path (also valid after the subcommand).
    #[arg(long, global = true, value_name = "PATH")]
    socket: Option<PathBuf>,
    #[command(subcommand)]
    command: Option<Commands>,
}

#[derive(Debug, Clone, Subcommand)]
enum Commands {
    /// Manage projects.
    Project {
        #[command(subcommand)]
        cmd: ProjectCmd,
    },
    /// Manage tabs.
    Tab {
        #[command(subcommand)]
        cmd: TabCmd,
    },
    /// Manage panes.
    Pane {
        #[command(subcommand)]
        cmd: PaneCmd,
    },
    /// Manage terminal sessions.
    Terminal {
        #[command(subcommand)]
        cmd: TerminalCmd,
    },
    /// Manage opt-in encrypted history.
    History {
        #[command(subcommand)]
        cmd: HistoryCmd,
    },
}

fn main() {
    std::process::exit(run());
}

fn run() -> i32 {
    let raw: Vec<String> = std::env::args().collect();
    let wants_json = raw.iter().any(|arg| arg == "--json");
    match Cli::try_parse_from(&raw) {
        Ok(cli) => dispatch(cli),
        Err(error) => {
            use clap::error::ErrorKind;
            match error.kind() {
                ErrorKind::DisplayHelp | ErrorKind::DisplayVersion => {
                    // Help/version go to stdout with success status.
                    let _ = error.print();
                    0
                }
                _ => {
                    if wants_json {
                        output::local_error("usage_error", &error.to_string(), true, 64)
                    } else {
                        let _ = error.print();
                        64
                    }
                }
            }
        }
    }
}

fn dispatch(cli: Cli) -> i32 {
    let as_json = cli.json;
    let socket_override = cli.socket.clone();
    match cli.command {
        None => launcher::run(as_json, socket_override),
        Some(command) => run_command(&command, as_json, socket_override.as_deref()),
    }
}

fn build_wire_call(command: &Commands) -> Result<(WireCall, String), String> {
    match command {
        Commands::Project { cmd } => commands::project::build(cmd).map(|call| {
            let method = call.method.clone();
            (call, method)
        }),
        Commands::Tab { cmd } => {
            commands::tab::build(cmd).map(|call| (call.clone(), call.method.clone()))
        }
        Commands::Pane { cmd } => {
            commands::pane::build(cmd).map(|call| (call.clone(), call.method.clone()))
        }
        Commands::Terminal { cmd } => {
            commands::terminal::build(cmd).map(|call| (call.clone(), call.method.clone()))
        }
        Commands::History { cmd } => {
            commands::history::build(cmd).map(|call| (call.clone(), call.method.clone()))
        }
    }
}

fn run_command(
    command: &Commands,
    as_json: bool,
    socket_override: Option<&std::path::Path>,
) -> i32 {
    let (call, method) = match build_wire_call(command) {
        Ok(pair) => pair,
        Err(message) => return output::local_error("usage_error", &message, as_json, 64),
    };
    let socket = match connection::resolve_socket(socket_override) {
        Ok(socket) => socket,
        Err(failure) => {
            return output::local_error(failure.code, &failure.message, as_json, failure.exit_code);
        }
    };
    let token = match connection::load_token(&socket) {
        Ok(token) => token,
        Err(failure) => {
            return output::local_error(failure.code, &failure.message, as_json, failure.exit_code);
        }
    };
    let request = IpcRequest {
        version: PROTOCOL_VERSION,
        request_id: uuid::Uuid::new_v4().to_string(),
        method: call.method,
        params: call.params,
        token,
    };
    match connection::send(&socket, &request) {
        Ok(response) => {
            if response.ok {
                output::success(&method, &response, as_json)
            } else {
                output::server_error(&response, as_json)
            }
        }
        Err(failure) => {
            output::local_error(failure.code, &failure.message, as_json, failure.exit_code)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use clap::CommandFactory;
    use std::fs;
    #[cfg(unix)]
    use std::os::unix::fs::PermissionsExt;
    use std::sync::{Mutex, OnceLock};

    fn env_lock() -> &'static Mutex<()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(()))
    }

    fn parse(args: &[&str]) -> Cli {
        Cli::try_parse_from(args).expect("CLI args should parse")
    }

    #[test]
    fn all_coverage_rows_parse() {
        // One representative invocation per coverage-table row.
        let cases: &[&[&str]] = &[
            &["omaterm", "project", "list"],
            &["omaterm", "project", "open", "/tmp"],
            &["omaterm", "project", "select", "p1"],
            &["omaterm", "tab", "list"],
            &["omaterm", "tab", "new"],
            &["omaterm", "tab", "close", "t1"],
            &["omaterm", "pane", "list"],
            &["omaterm", "pane", "split", "--right"],
            &["omaterm", "pane", "focus", "pane-1"],
            &["omaterm", "pane", "close", "pane-1"],
            &[
                "omaterm",
                "pane",
                "resize",
                "--split",
                "s1",
                "--fraction",
                "0.4",
            ],
            &["omaterm", "pane", "equalize"],
            &["omaterm", "terminal", "list"],
            &["omaterm", "terminal", "new"],
            &["omaterm", "terminal", "send", "--pane", "pane-1", "hello"],
            &[
                "omaterm", "terminal", "run", "--pane", "pane-1", "--", "cargo", "test",
            ],
            &["omaterm", "terminal", "read", "--pane", "pane-1"],
            &["omaterm", "history", "enable"],
            &["omaterm", "history", "disable"],
            &["omaterm", "history", "status"],
            &["omaterm", "history", "list", "--pane", "pane-1"],
            &[
                "omaterm", "history", "list", "--pane", "pane-1", "--limit", "5",
            ],
            &["omaterm", "history", "pause", "--pane", "pane-1"],
            &["omaterm", "history", "resume", "--pane", "pane-1"],
            &["omaterm", "history", "clear", "--pane", "pane-1"],
            &["omaterm", "history", "clear", "--project", "p1"],
            &["omaterm", "history", "clear", "--all"],
        ];
        assert_eq!(cases.len(), 27);
        for args in cases {
            let cli = Cli::try_parse_from(*args);
            assert!(cli.is_ok(), "{args:?}: {cli:?}");
            let cli = cli.unwrap();
            let command = cli.command.as_ref().expect("subcommand must parse");
            assert!(build_wire_call(command).is_ok(), "{args:?}");
        }
    }

    #[test]
    fn history_clear_requires_exactly_one_scope() {
        // No scope.
        let cli = parse(&["omaterm", "history", "clear"]);
        match cli.command {
            Some(Commands::History { cmd }) => {
                assert!(commands::history::build(&cmd).is_err())
            }
            other => panic!("expected history clear, got {other:?}"),
        }
        // Conflicting scopes are rejected by clap itself.
        assert!(
            Cli::try_parse_from(["omaterm", "history", "clear", "--pane", "p", "--all"]).is_err()
        );
        // Each single scope maps to its own wire method.
        for (args, method) in [
            (
                &["omaterm", "history", "clear", "--pane", "p1"][..],
                "history.clear-pane",
            ),
            (
                &["omaterm", "history", "clear", "--project", "p1"][..],
                "history.clear-project",
            ),
            (
                &["omaterm", "history", "clear", "--all"][..],
                "history.clear-all",
            ),
        ] {
            let cli = parse(args);
            match cli.command {
                Some(Commands::History { cmd }) => {
                    let call = commands::history::build(&cmd).expect("single scope builds");
                    assert_eq!(call.method, method);
                }
                other => panic!("expected history clear, got {other:?}"),
            }
        }
    }

    #[test]
    fn global_flags_parse_in_either_position() {
        let before = parse(&[
            "omaterm",
            "--json",
            "--socket",
            "/tmp/a.sock",
            "pane",
            "list",
        ]);
        assert!(before.json);
        assert_eq!(before.socket, Some(PathBuf::from("/tmp/a.sock")));
        let after = parse(&[
            "omaterm",
            "pane",
            "list",
            "--json",
            "--socket",
            "/tmp/a.sock",
        ]);
        assert!(after.json);
        assert_eq!(after.socket, Some(PathBuf::from("/tmp/a.sock")));
        // run argv after `--` keeps metacharacters intact.
        let cli = parse(&[
            "omaterm", "terminal", "run", "--pane", "p1", "--", "echo", "a b", "$(x)",
        ]);
        match cli.command {
            Some(Commands::Terminal { cmd }) => match cmd {
                TerminalCmd::Run { argv, .. } => {
                    assert_eq!(argv, vec!["echo", "a b", "$(x)"]);
                }
                other => panic!("unexpected terminal subcommand: {other:?}"),
            },
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn clap_usage_errors_are_detected() {
        // No direction flag.
        assert!(
            Cli::try_parse_from(["omaterm", "pane", "split"]).is_ok(),
            "clap accepts missing flags; our builder must reject"
        );
        let cli = parse(&["omaterm", "pane", "split"]);
        match cli.command {
            Some(Commands::Pane { cmd }) => assert!(commands::pane::build(&cmd).is_err()),
            other => panic!("unexpected: {other:?}"),
        }
        // Missing required argv after `--` is a clap usage error.
        assert!(Cli::try_parse_from(["omaterm", "terminal", "run", "--pane", "p1"]).is_err());
    }

    #[test]
    fn end_to_end_success_failure_and_scoped_denial_against_a_fake_server() {
        use omaterm_ipc::{IpcServer, RequestHandler};
        use omaterm_protocol::IpcResponse;
        use std::sync::Arc;

        let _guard = env_lock().lock().unwrap();
        unsafe {
            std::env::remove_var("OMATERM_TOKEN");
            std::env::remove_var("OMATERM_SOCKET");
            std::env::remove_var("OMATERM_PROJECT_ID");
            std::env::remove_var("OMATERM_PANE_ID");
        }
        let dir = std::env::temp_dir().join(format!("omaterm-cli-e2e-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o700)).unwrap();
        let socket = dir.join("omaterm.sock");
        // Owner-only credential the CLI must read (matching socket).
        let secret = format!("{}e2e", "b".repeat(41));
        assert!(secret.len() >= 43);
        let credential = socket.with_extension("credential");
        fs::write(&credential, &secret).unwrap();
        fs::set_permissions(&credential, fs::Permissions::from_mode(0o600)).unwrap();

        let handler: RequestHandler = Arc::new(|request, _| match request.method.as_str() {
            "pane.list" => IpcResponse::success(
                request.request_id,
                serde_json::json!({"panes": [{"id": "p1", "session_id": "s1", "focused": true}], "truncated": false}),
            ),
            "pane.split" => IpcResponse::success(
                request.request_id,
                serde_json::json!({"new_pane_id": "p2", "new_session_id": "s2"}),
            ),
            _ => IpcResponse::failure(request.request_id, "permission_denied", "denied for test"),
        });
        let server = IpcServer::bind(&socket, handler).unwrap();

        // pane list succeeds through the real token + socket path.
        let token = connection::load_token(&socket).unwrap();
        assert!(token.is_some());
        let request = IpcRequest {
            version: PROTOCOL_VERSION,
            request_id: "e2e-1".into(),
            method: "pane.list".into(),
            params: serde_json::json!({}),
            token,
        };
        let response = connection::send(&socket, &request).unwrap();
        assert!(response.ok);
        assert_eq!(output::success("pane.list", &response, false), 0);
        assert_eq!(output::success("pane.list", &response, true), 0);

        // Denied targets surface exit 1 in both modes.
        let request = IpcRequest {
            version: PROTOCOL_VERSION,
            request_id: "e2e-2".into(),
            method: "tab.close".into(),
            params: serde_json::json!({"tab_id": "foreign"}),
            token: connection::load_token(&socket).unwrap(),
        };
        let denied = connection::send(&socket, &request).unwrap();
        assert!(!denied.ok);
        assert_eq!(output::server_error(&denied, false), 1);
        assert_eq!(output::server_error(&denied, true), 1);

        // A socket with no listener is a connection failure (exit 2), never
        // an auto-launch.
        let missing = dir.join("missing.sock");
        let failure = connection::send(
            &missing,
            &IpcRequest {
                version: PROTOCOL_VERSION,
                request_id: "e2e-3".into(),
                method: "pane.list".into(),
                params: serde_json::json!({}),
                token: None,
            },
        )
        .unwrap_err();
        assert_eq!(failure.exit_code, 2);
        assert_eq!(
            output::local_error(failure.code, &failure.message, true, failure.exit_code),
            2
        );

        let mut server = server;
        server.shutdown().unwrap();
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn scoped_env_context_fills_optional_selectors() {
        let _guard = env_lock().lock().unwrap();
        unsafe {
            std::env::set_var("OMATERM_PROJECT_ID", "env-project");
            std::env::remove_var("OMATERM_TOKEN");
        }
        let call = commands::tab::build(&TabCmd::List { project: None }).unwrap();
        assert_eq!(call.params["project_id"], "env-project");
        let call = commands::tab::build(&TabCmd::List {
            project: Some("explicit".into()),
        })
        .unwrap();
        assert_eq!(call.params["project_id"], "explicit");
        unsafe {
            std::env::remove_var("OMATERM_PROJECT_ID");
        }
    }

    #[test]
    fn factory_renders_without_panicking() {
        // Guards against malformed clap configuration.
        let mut command = Cli::command();
        let _ = command.render_help();
    }
}
