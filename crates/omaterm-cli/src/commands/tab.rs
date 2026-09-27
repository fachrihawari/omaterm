use super::{WireCall, optional_selector};
use clap::Subcommand;
use serde_json::json;

#[derive(Debug, Clone, Subcommand)]
pub enum TabCmd {
    /// List tabs in the resolved project.
    List {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
    },
    /// Create a tab in the resolved project.
    New {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Optional tab name.
        #[arg(long)]
        name: Option<String>,
    },
    /// Close a tab.
    Close {
        /// Tab ID to close.
        tab_id: String,
    },
}

pub fn build(cmd: &TabCmd) -> Result<WireCall, String> {
    match cmd {
        TabCmd::List { project } => Ok(WireCall {
            method: "tab.list".into(),
            params: match optional_selector(project.clone(), "OMATERM_PROJECT_ID") {
                Some(id) => json!({ "project_id": id }),
                None => json!({}),
            },
        }),
        TabCmd::New { project, name } => {
            if let Some(name) = name
                && (name.trim().is_empty() || name.chars().any(char::is_control))
            {
                return Err("tab --name must be non-empty without control characters".into());
            }
            Ok(WireCall {
                method: "tab.create".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "name": name,
                }),
            })
        }
        TabCmd::Close { tab_id } => {
            if tab_id.trim().is_empty() {
                return Err("tab close requires a tab ID".into());
            }
            Ok(WireCall {
                method: "tab.close".into(),
                params: json!({ "tab_id": tab_id }),
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn list_new_and_close_map_to_wire() {
        let call = build(&TabCmd::List { project: None }).unwrap();
        assert_eq!(call.method, "tab.list");
        let call = build(&TabCmd::New {
            project: Some("p".into()),
            name: Some("Tests".into()),
        })
        .unwrap();
        assert_eq!(call.method, "tab.create");
        assert_eq!(call.params["project_id"], "p");
        let call = build(&TabCmd::Close { tab_id: "t".into() }).unwrap();
        assert_eq!(call.method, "tab.close");
    }
}
