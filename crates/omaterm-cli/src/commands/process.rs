//! Thin project process-query CLI mapping (M18 query prerequisite).

use super::{WireCall, optional_selector};
use clap::Subcommand;
use serde_json::json;

#[derive(Debug, Clone, Subcommand)]
pub enum ProcessCmd {
    /// List processes belonging to a project's live terminal sessions.
    List {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
    },
}

pub fn build(command: &ProcessCmd) -> WireCall {
    match command {
        ProcessCmd::List { project } => WireCall {
            method: "process.list".into(),
            params: json!({
                "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
            }),
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn list_maps_project_selector_to_wire() {
        let call = build(&ProcessCmd::List { project: None });
        assert_eq!(call.method, "process.list");
        assert_eq!(call.params["project_id"], serde_json::Value::Null);
        let call = build(&ProcessCmd::List {
            project: Some("project-id".into()),
        });
        assert_eq!(call.params["project_id"], "project-id");
    }
}
