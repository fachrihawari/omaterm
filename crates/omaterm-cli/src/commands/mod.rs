//! Thin CLI-to-wire mapping. No workspace logic lives here: each command
//! builds exactly one `(method, params)` pair for the M8 protocol.

pub mod diff;
pub mod file;
pub mod git;
pub mod history;
pub mod pane;
pub mod process;
pub mod project;
pub mod tab;
pub mod terminal;

use serde_json::Value;

/// A fully built wire call plus the pane ID used for human confirmations.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WireCall {
    pub method: String,
    pub params: Value,
}

/// Read an optional selector: explicit flag first, then `OMATERM_*`
/// environment context, then nothing (the server resolves its selection).
/// Callers that require an ID turn `None` into a local usage error.
pub fn optional_selector(explicit: Option<String>, env_name: &str) -> Option<String> {
    if let Some(value) = explicit {
        let trimmed = value.trim().to_owned();
        return if trimmed.is_empty() {
            None
        } else {
            Some(trimmed)
        };
    }
    std::env::var(env_name)
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
}

/// Required pane selectors also honor `OMATERM_PANE_ID` so in-app callers
/// can run `terminal read` without repeating the originating pane.
pub fn required_pane(explicit: Option<String>) -> Result<String, String> {
    optional_selector(explicit, "OMATERM_PANE_ID")
        .ok_or_else(|| "missing required --pane <pane-id> (or OMATERM_PANE_ID)".to_owned())
}
