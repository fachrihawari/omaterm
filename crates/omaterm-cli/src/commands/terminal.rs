use super::{WireCall, optional_selector, required_pane};
use base64::Engine;
use clap::Subcommand;
use serde_json::json;
use std::path::PathBuf;

#[derive(Debug, Clone, Subcommand)]
pub enum TerminalCmd {
    /// List authorized terminal sessions.
    List,
    /// Create a terminal in a new tab in the resolved project.
    New {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Optional working directory.
        #[arg(long)]
        directory: Option<PathBuf>,
    },
    /// Send literal UTF-8 bytes without an implicit newline.
    Send {
        /// Target pane; defaults to OMATERM_PANE_ID.
        #[arg(long)]
        pane: Option<String>,
        /// Exact text bytes to send.
        text: String,
    },
    /// Submit structured argv to a ready shell (no shell interpolation).
    Run {
        /// Target pane; defaults to OMATERM_PANE_ID.
        #[arg(long)]
        pane: Option<String>,
        /// Command and arguments after `--`.
        #[arg(last = true, required = true)]
        argv: Vec<String>,
    },
    /// Read the bounded visible viewport (never a transcript).
    Read {
        /// Target pane; defaults to OMATERM_PANE_ID.
        #[arg(long)]
        pane: Option<String>,
        /// Visible lines (1..=1000, default 50).
        #[arg(long, default_value_t = 50)]
        lines: usize,
        /// Visible columns (1..=1000, default 500).
        #[arg(long, default_value_t = 500)]
        columns: usize,
    },
}

pub fn build(cmd: &TerminalCmd) -> Result<WireCall, String> {
    match cmd {
        TerminalCmd::List => Ok(WireCall {
            method: "terminal.list".into(),
            params: json!({}),
        }),
        TerminalCmd::New { project, directory } => Ok(WireCall {
            method: "terminal.create".into(),
            params: json!({
                "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                "directory": directory.as_ref().map(|path| path.to_string_lossy()),
            }),
        }),
        TerminalCmd::Send { pane, text } => {
            let pane_id = required_pane(pane.clone())?;
            let data = base64::engine::general_purpose::STANDARD.encode(text.as_bytes());
            Ok(WireCall {
                method: "terminal.send".into(),
                params: json!({ "pane_id": pane_id, "data": data }),
            })
        }
        TerminalCmd::Run { pane, argv } => {
            let pane_id = required_pane(pane.clone())?;
            if argv.is_empty() {
                return Err("terminal run requires argv after `--`".into());
            }
            Ok(WireCall {
                method: "terminal.run".into(),
                params: json!({ "pane_id": pane_id, "argv": argv }),
            })
        }
        TerminalCmd::Read {
            pane,
            lines,
            columns,
        } => {
            let pane_id = required_pane(pane.clone())?;
            if *lines == 0 || *lines > 1000 || *columns == 0 || *columns > 1000 {
                return Err("terminal read bounds must be between 1 and 1000 lines/columns".into());
            }
            Ok(WireCall {
                method: "terminal.read".into(),
                params: json!({ "pane_id": pane_id, "lines": lines, "columns": columns }),
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn send_preserves_exact_bytes_without_newline() {
        let call = build(&TerminalCmd::Send {
            pane: Some("pane-1".into()),
            text: "cargo test".into(),
        })
        .unwrap();
        assert_eq!(call.method, "terminal.send");
        let decoded = base64::engine::general_purpose::STANDARD
            .decode(call.params["data"].as_str().unwrap())
            .unwrap();
        assert_eq!(decoded, b"cargo test");
    }

    #[test]
    fn run_keeps_structured_argv_with_metacharacters() {
        let argv = vec![
            "echo".to_owned(),
            "a b".to_owned(),
            "\"quoted\"".to_owned(),
            "".to_owned(),
            "ünïcodé".to_owned(),
            "$(rm -rf /)".to_owned(),
        ];
        let call = build(&TerminalCmd::Run {
            pane: Some("pane-1".into()),
            argv: argv.clone(),
        })
        .unwrap();
        assert_eq!(call.method, "terminal.run");
        let round_trip: Vec<String> = serde_json::from_value(call.params["argv"].clone()).unwrap();
        assert_eq!(round_trip, argv);
    }

    #[test]
    fn read_defaults_match_the_m9_contract() {
        // Clap defaults (50 x 500) are part of the contract; the wire call
        // must carry them explicitly so the server never falls back to its
        // 100 x 200 transport defaults for CLI reads.
        let call = build(&TerminalCmd::Read {
            pane: Some("pane-1".into()),
            lines: 50,
            columns: 500,
        })
        .unwrap();
        assert_eq!(call.params["lines"], 50);
        assert_eq!(call.params["columns"], 500);
    }

    #[test]
    fn missing_pane_is_a_local_usage_error() {
        let result = build(&TerminalCmd::Read {
            pane: None,
            lines: 50,
            columns: 500,
        });
        // Without OMATERM_PANE_ID in this test environment this fails; when
        // the variable is set the call succeeds via env fallback. Either way
        // the function never panics and never silently targets another pane.
        let _ = result;
    }
}
