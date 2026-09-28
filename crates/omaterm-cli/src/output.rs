//! Human vs JSON output. Machine output goes to stdout; human
//! diagnostics go to stderr. `--json` never emits colors.

use omaterm_protocol::IpcResponse;
use serde_json::{Value, json};

/// Render a successful server response. Always exits 0.
pub fn success(method: &str, response: &IpcResponse, as_json: bool) -> i32 {
    if as_json {
        println!(
            "{}",
            serde_json::to_string(response).unwrap_or_else(|_| "{}".into())
        );
        return 0;
    }
    println!("{}", human_success(method, response));
    0
}

fn human_success(method: &str, response: &IpcResponse) -> String {
    let result = response.result.clone().unwrap_or(Value::Null);
    match method {
        "project.list" => render_projects(&result),
        "project.create" => format!(
            "Opened project {} (tab {}, pane {}, session {})",
            string(&result, "project_id"),
            string(&result, "tab_id"),
            string(&result, "pane_id"),
            string(&result, "session_id"),
        ),
        "project.select" => "Selected project.".into(),
        "tab.list" => render_tabs(&result),
        "tab.create" | "terminal.create" => format!(
            "Created tab {} (pane {}, session {})",
            string(&result, "tab_id"),
            string(&result, "pane_id"),
            string(&result, "session_id"),
        ),
        "tab.close" => "Closed tab.".into(),
        "pane.list" => render_panes(&result),
        "pane.split" => format!(
            "Split pane: new pane {} (session {})",
            string(&result, "new_pane_id"),
            string(&result, "new_session_id"),
        ),
        "pane.focus" => "Focused pane.".into(),
        "pane.close" => "Closed pane.".into(),
        "pane.resize" => "Resized split.".into(),
        "pane.equalize" => "Equalized splits.".into(),
        "terminal.list" => render_terminals(&result),
        "terminal.send" => "Sent bytes to terminal.".into(),
        "terminal.run" => "Submitted command to shell.".into(),
        "terminal.read" => render_read(&result),
        "history.enable" => "History persistence enabled.".into(),
        "history.disable" => render_cleared(&result, "History persistence disabled."),
        "history.status" => render_history_status(&result),
        "history.list" => render_journal(&result),
        "history.pause" => "History capture paused for pane.".into(),
        "history.resume" => "History capture resumed for pane.".into(),
        "history.clear-pane" | "history.clear-project" => {
            render_cleared(&result, "History cleared.")
        }
        "history.clear-all" => {
            render_cleared(&result, "All history cleared; encryption key rotated.")
        }
        _ => result.to_string(),
    }
}

/// Render a server `ok:false` response. Always exits 1.
pub fn server_error(response: &IpcResponse, as_json: bool) -> i32 {
    if as_json {
        println!(
            "{}",
            serde_json::to_string(response).unwrap_or_else(|_| "{}".into())
        );
    }
    let (code, message) = response
        .error
        .as_ref()
        .map(|error| (error.code.as_str(), error.message.as_str()))
        .unwrap_or(("command_error", "command failed"));
    eprintln!("Error [{code}]: {message}");
    1
}

/// Render a local failure as a stable envelope in JSON mode, or as a
/// one-line diagnostic otherwise. Returns the associated exit code.
pub fn local_error(code: &str, message: &str, as_json: bool, exit_code: i32) -> i32 {
    if as_json {
        let envelope = json!({"ok": false, "error": {"code": code, "message": message}});
        println!("{envelope}");
    } else {
        eprintln!("Error [{code}]: {message}");
    }
    exit_code
}

fn string(value: &Value, key: &str) -> String {
    value
        .get(key)
        .and_then(Value::as_str)
        .unwrap_or("-")
        .to_owned()
}

fn render_projects(result: &Value) -> String {
    let Some(items) = result.get("projects").and_then(Value::as_array) else {
        return "No projects.".into();
    };
    if items.is_empty() {
        return "No projects.".into();
    }
    let mut out = String::from("ID\tNAME\tDIRECTORY\tTABS\tSELECTED\n");
    for item in items {
        out.push_str(&format!(
            "{}\t{}\t{}\t{}\t{}\n",
            item.get("id").and_then(Value::as_str).unwrap_or("-"),
            item.get("name").and_then(Value::as_str).unwrap_or("-"),
            item.get("directory").and_then(Value::as_str).unwrap_or("-"),
            item.get("tab_count").and_then(Value::as_u64).unwrap_or(0),
            if item
                .get("selected")
                .and_then(Value::as_bool)
                .unwrap_or(false)
            {
                "*"
            } else {
                ""
            },
        ));
    }
    if result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        out.push_str("(truncated: showing first 128)\n");
    }
    out.trim_end().to_owned()
}

fn render_tabs(result: &Value) -> String {
    let Some(items) = result.get("tabs").and_then(Value::as_array) else {
        return "No tabs.".into();
    };
    if items.is_empty() {
        return "No tabs.".into();
    }
    let mut out = String::from("ID\tNAME\tPANES\tSELECTED\n");
    for item in items {
        out.push_str(&format!(
            "{}\t{}\t{}\t{}\n",
            item.get("id").and_then(Value::as_str).unwrap_or("-"),
            item.get("name").and_then(Value::as_str).unwrap_or("-"),
            item.get("pane_count").and_then(Value::as_u64).unwrap_or(0),
            if item
                .get("selected")
                .and_then(Value::as_bool)
                .unwrap_or(false)
            {
                "*"
            } else {
                ""
            },
        ));
    }
    if result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        out.push_str("(truncated: showing first 128)\n");
    }
    out.trim_end().to_owned()
}

fn render_panes(result: &Value) -> String {
    let Some(items) = result.get("panes").and_then(Value::as_array) else {
        return "No panes.".into();
    };
    if items.is_empty() {
        return "No panes.".into();
    }
    let mut out = String::from("ID\tSESSION\tFOCUSED\n");
    for item in items {
        out.push_str(&format!(
            "{}\t{}\t{}\n",
            item.get("id").and_then(Value::as_str).unwrap_or("-"),
            item.get("session_id")
                .and_then(Value::as_str)
                .unwrap_or("-"),
            if item
                .get("focused")
                .and_then(Value::as_bool)
                .unwrap_or(false)
            {
                "*"
            } else {
                ""
            },
        ));
    }
    if result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        out.push_str("(truncated: showing first 128)\n");
    }
    out.trim_end().to_owned()
}

fn render_terminals(result: &Value) -> String {
    let Some(items) = result.get("terminals").and_then(Value::as_array) else {
        return "No terminals.".into();
    };
    if items.is_empty() {
        return "No terminals.".into();
    }
    let mut out = String::from("ID\tPANE\tCWD\tEXITED\n");
    for item in items {
        out.push_str(&format!(
            "{}\t{}\t{}\t{}\n",
            item.get("id").and_then(Value::as_str).unwrap_or("-"),
            item.get("pane_id").and_then(Value::as_str).unwrap_or("-"),
            item.get("cwd").and_then(Value::as_str).unwrap_or("-"),
            if item.get("exited").and_then(Value::as_bool).unwrap_or(false) {
                "yes"
            } else {
                "no"
            },
        ));
    }
    if result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        out.push_str("(truncated: showing first 128)\n");
    }
    out.trim_end().to_owned()
}

fn render_cleared(result: &Value, message: &str) -> String {
    let removed = result
        .get("removed_files")
        .and_then(Value::as_u64)
        .unwrap_or(0);
    format!("{message} ({removed} files removed)")
}

fn render_history_status(result: &Value) -> String {
    let enabled = result
        .get("enabled")
        .and_then(Value::as_bool)
        .unwrap_or(false);
    let key = result
        .get("key_available")
        .and_then(Value::as_bool)
        .unwrap_or(false);
    let files = result
        .get("archive_files")
        .and_then(Value::as_u64)
        .unwrap_or(0);
    let bytes = result
        .get("archive_bytes")
        .and_then(Value::as_u64)
        .unwrap_or(0);
    let paused = result
        .get("paused_panes")
        .and_then(Value::as_u64)
        .unwrap_or(0);
    let warning = result.get("warning").and_then(Value::as_str).unwrap_or("");
    let state = if enabled { "enabled" } else { "disabled" };
    let mut out = format!(
        "History {state} (key {}, {} archives, {} bytes, {} panes paused)",
        if key { "available" } else { "unavailable" },
        files,
        bytes,
        paused
    );
    if !warning.is_empty() {
        out.push_str(&format!("\nwarning: {warning}"));
    }
    out
}

fn render_journal(result: &Value) -> String {
    let entries = result
        .get("entries")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let truncated = result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false);
    if entries.is_empty() {
        return "No journal entries.".into();
    }
    let mut out = Vec::with_capacity(entries.len());
    for entry in entries.iter().take(128) {
        let command = entry.get("command").and_then(Value::as_str).unwrap_or("");
        let exit = entry
            .get("exit_status")
            .and_then(Value::as_i64)
            .map(|status| status.to_string())
            .unwrap_or_else(|| "-".into());
        let started = entry
            .get("started_unix_secs")
            .and_then(Value::as_u64)
            .unwrap_or(0);
        out.push(format!("[exit {exit}] {command} (@{started})"));
    }
    if truncated || entries.len() > out.len() {
        out.push("[truncated: bounded journal listing]".into());
    }
    out.join("\n")
}

fn render_read(result: &Value) -> String {
    let text = result.get("text").and_then(Value::as_str).unwrap_or("");
    let truncated = result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false);
    let lines = result.get("lines").and_then(Value::as_u64).unwrap_or(0);
    let columns = result.get("columns").and_then(Value::as_u64).unwrap_or(0);
    if truncated {
        format!("{text}\n[truncated: showing {lines} lines x {columns} columns]")
    } else {
        text.to_owned()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ok(result: Value) -> IpcResponse {
        IpcResponse::success("req-1".into(), result)
    }

    #[test]
    fn json_success_envelope_is_valid_json() {
        let response = ok(serde_json::json!({"panes": []}));
        let text = serde_json::to_string(&response).unwrap();
        let parsed: Value = serde_json::from_str(&text).unwrap();
        assert_eq!(parsed["ok"], true);
    }

    #[test]
    fn human_lists_and_truncation_render() {
        let text = human_success(
            "pane.list",
            &ok(serde_json::json!({
                "panes": [{"id": "p1", "session_id": "s1", "focused": true}],
                "truncated": true,
            })),
        );
        assert!(text.contains("p1"));
        assert!(text.contains("truncated"));
        let text = human_success(
            "terminal.read",
            &ok(serde_json::json!({
                "text": "hello", "truncated": true, "lines": 50, "columns": 500,
            })),
        );
        assert!(text.contains("hello"));
        assert!(text.contains("truncated"));
    }

    #[test]
    fn human_history_outputs_render() {
        let status = human_success(
            "history.status",
            &ok(serde_json::json!({
                "enabled": true, "key_available": true,
                "archive_files": 2, "archive_bytes": 100,
                "paused_panes": 1, "warning": "locked",
            })),
        );
        assert!(status.contains("enabled"));
        assert!(status.contains('2'));
        assert!(status.contains("locked"));
        let journal = human_success(
            "history.list",
            &ok(serde_json::json!({
                "entries": [
                    {"command": "cargo test", "exit_status": 0, "started_unix_secs": 7},
                    {"command": "unfinished", "started_unix_secs": 8},
                ],
                "truncated": false,
            })),
        );
        assert!(journal.contains("cargo test"));
        assert!(journal.contains("[exit 0]"));
        assert!(journal.contains("[exit -]"));
        assert!(!journal.contains("No journal entries."));
        let empty = human_success("history.list", &ok(serde_json::json!({"entries": []})));
        assert!(empty.contains("No journal entries."));
        let cleared = human_success(
            "history.clear-all",
            &ok(serde_json::json!({"removed_files": 3})),
        );
        assert!(cleared.contains('3'));
        assert!(cleared.contains("rotated"));
    }

    #[test]
    fn local_error_envelope_is_stable() {
        let envelope =
            serde_json::json!({"ok": false, "error": {"code": "usage_error", "message": "bad"}});
        assert_eq!(envelope["ok"], false);
        assert_eq!(envelope["error"]["code"], "usage_error");
    }
}
