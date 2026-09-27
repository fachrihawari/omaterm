//! Strict v1 method parameters. Target resolution and authorization belong to
//! the desktop owner, not the wire or socket transport.
use serde::Deserialize;
use serde_json::Value;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MethodError(pub &'static str);

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Empty {}

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProjectCreate {
    pub directory: Option<String>,
    pub name: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProjectSelector {
    pub project_id: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OptionalProject {
    pub project_id: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TabCreate {
    pub project_id: Option<String>,
    pub name: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TabSelector {
    pub tab_id: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OptionalTab {
    pub tab_id: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PaneSelector {
    pub pane_id: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PaneSplit {
    pub pane_id: Option<String>,
    pub direction: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PaneResize {
    pub split_id: String,
    pub fraction: f32,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TerminalCreate {
    pub project_id: Option<String>,
    pub directory: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TerminalSend {
    pub pane_id: String,
    pub data: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TerminalRun {
    pub pane_id: String,
    pub argv: Vec<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TerminalRead {
    pub pane_id: String,
    pub lines: Option<usize>,
    pub columns: Option<usize>,
}

#[derive(Clone)]
pub enum Method {
    ProjectList(Empty),
    ProjectCreate(ProjectCreate),
    ProjectSelect(ProjectSelector),
    TabList(OptionalProject),
    TabCreate(TabCreate),
    TabClose(TabSelector),
    PaneList(OptionalTab),
    PaneSplit(PaneSplit),
    PaneClose(PaneSelector),
    PaneFocus(PaneSelector),
    PaneResize(PaneResize),
    PaneEqualize(OptionalTab),
    TerminalList(Empty),
    TerminalCreate(TerminalCreate),
    TerminalSend(TerminalSend),
    TerminalRun(TerminalRun),
    TerminalRead(TerminalRead),
}

impl Method {
    pub fn decode(name: &str, params: Value) -> Result<Self, MethodError> {
        let params = if params.is_null() {
            serde_json::json!({})
        } else {
            params
        };
        macro_rules! decode {
            ($ty:ident, $variant:ident) => {
                serde_json::from_value::<$ty>(params)
                    .map(Self::$variant)
                    .map_err(|_| MethodError("invalid or unknown method parameter"))
            };
        }
        match name {
            "project.list" => decode!(Empty, ProjectList),
            "project.create" => decode!(ProjectCreate, ProjectCreate),
            "project.select" => decode!(ProjectSelector, ProjectSelect),
            "tab.list" => decode!(OptionalProject, TabList),
            "tab.create" => decode!(TabCreate, TabCreate),
            "tab.close" => decode!(TabSelector, TabClose),
            "pane.list" => decode!(OptionalTab, PaneList),
            "pane.split" => decode!(PaneSplit, PaneSplit),
            "pane.close" => decode!(PaneSelector, PaneClose),
            "pane.focus" => decode!(PaneSelector, PaneFocus),
            "pane.resize" => decode!(PaneResize, PaneResize),
            "pane.equalize" => decode!(OptionalTab, PaneEqualize),
            "terminal.list" => decode!(Empty, TerminalList),
            "terminal.create" => decode!(TerminalCreate, TerminalCreate),
            "terminal.send" => decode!(TerminalSend, TerminalSend),
            "terminal.run" => decode!(TerminalRun, TerminalRun),
            "terminal.read" => decode!(TerminalRead, TerminalRead),
            _ => Err(MethodError("unknown method")),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn all_seventeen_methods_decode_and_reject_unknown_parameters() {
        let cases = [
            ("project.list", serde_json::json!({})),
            ("project.create", serde_json::json!({"directory":"/tmp"})),
            ("project.select", serde_json::json!({"project_id":"id"})),
            ("tab.list", serde_json::json!({})),
            ("tab.create", serde_json::json!({"name":"test"})),
            ("tab.close", serde_json::json!({"tab_id":"id"})),
            ("pane.list", serde_json::json!({})),
            ("pane.split", serde_json::json!({"direction":"right"})),
            ("pane.close", serde_json::json!({"pane_id":"id"})),
            ("pane.focus", serde_json::json!({"pane_id":"id"})),
            (
                "pane.resize",
                serde_json::json!({"split_id":"id","fraction":0.5}),
            ),
            ("pane.equalize", serde_json::json!({})),
            ("terminal.list", serde_json::json!({})),
            ("terminal.create", serde_json::json!({})),
            (
                "terminal.send",
                serde_json::json!({"pane_id":"id","data":"AQ=="}),
            ),
            (
                "terminal.run",
                serde_json::json!({"pane_id":"id","argv":["true"]}),
            ),
            (
                "terminal.read",
                serde_json::json!({"pane_id":"id","lines":20}),
            ),
        ];
        assert_eq!(cases.len(), 17);
        for (name, params) in cases {
            assert!(Method::decode(name, params.clone()).is_ok(), "{name}");
            let mut unknown = params;
            unknown
                .as_object_mut()
                .unwrap()
                .insert("unknown".into(), serde_json::json!(true));
            assert!(Method::decode(name, unknown).is_err(), "{name}");
        }
        assert!(Method::decode("unknown", serde_json::json!({})).is_err());
    }
}
