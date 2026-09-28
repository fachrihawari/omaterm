use super::{WireCall, optional_selector, required_pane};
use clap::Subcommand;
use serde_json::json;

#[derive(Debug, Clone, Subcommand)]
pub enum HistoryCmd {
    /// Opt in to encrypted scrollback and command-journal persistence.
    Enable,
    /// Opt out and delete all persisted history archives and journals.
    Disable,
    /// Show history state: enabled, key availability, archives, warnings.
    Status,
    /// List bounded journal entries for a pane (newest last).
    List {
        /// Target pane; defaults to OMATERM_PANE_ID.
        #[arg(long)]
        pane: Option<String>,
        /// Entries to show (1..=1000, default 50).
        #[arg(long, default_value_t = 50)]
        limit: usize,
    },
    /// Pause capture for a pane (record kept, nothing new recorded).
    Pause {
        /// Target pane; defaults to OMATERM_PANE_ID.
        #[arg(long)]
        pane: Option<String>,
    },
    /// Resume capture for a paused pane.
    Resume {
        /// Target pane; defaults to OMATERM_PANE_ID.
        #[arg(long)]
        pane: Option<String>,
    },
    /// Delete persisted history. Exactly one scope is required.
    Clear {
        /// Delete one pane's archives and journal.
        #[arg(long, conflicts_with_all = ["project", "all"])]
        pane: Option<String>,
        /// Delete a project's archives and journals.
        #[arg(long, conflicts_with_all = ["pane", "all"])]
        project: Option<String>,
        /// Delete everything and rotate the encryption key.
        #[arg(long, conflicts_with_all = ["pane", "project"])]
        all: bool,
    },
}

pub fn build(cmd: &HistoryCmd) -> Result<WireCall, String> {
    match cmd {
        HistoryCmd::Enable => Ok(WireCall {
            method: "history.enable".into(),
            params: json!({}),
        }),
        HistoryCmd::Disable => Ok(WireCall {
            method: "history.disable".into(),
            params: json!({}),
        }),
        HistoryCmd::Status => Ok(WireCall {
            method: "history.status".into(),
            params: json!({}),
        }),
        HistoryCmd::List { pane, limit } => Ok(WireCall {
            method: "history.list".into(),
            params: json!({ "pane_id": required_pane(pane.clone())?, "limit": limit }),
        }),
        HistoryCmd::Pause { pane } => Ok(WireCall {
            method: "history.pause".into(),
            params: json!({ "pane_id": required_pane(pane.clone())? }),
        }),
        HistoryCmd::Resume { pane } => Ok(WireCall {
            method: "history.resume".into(),
            params: json!({ "pane_id": required_pane(pane.clone())? }),
        }),
        HistoryCmd::Clear { pane, project, all } => {
            let scopes = pane.is_some() as u8 + project.is_some() as u8 + (*all as u8);
            if scopes != 1 {
                return Err(
                    "history clear requires exactly one scope: --pane, --project, or --all".into(),
                );
            }
            if let Some(pane) = pane {
                return Ok(WireCall {
                    method: "history.clear-pane".into(),
                    params: json!({ "pane_id": required_pane(Some(pane.clone()))? }),
                });
            }
            if let Some(project) = project {
                let project_id = optional_selector(Some(project.clone()), "OMATERM_PROJECT_ID")
                    .filter(|value| !value.is_empty())
                    .ok_or_else(|| "missing required --project <project-id>".to_owned())?;
                return Ok(WireCall {
                    method: "history.clear-project".into(),
                    params: json!({ "project_id": project_id }),
                });
            }
            Ok(WireCall {
                method: "history.clear-all".into(),
                params: json!({}),
            })
        }
    }
}
