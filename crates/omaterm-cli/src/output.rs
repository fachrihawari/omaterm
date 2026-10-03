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
        "project.root" => render_root(&result),
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
        "file.list" | "file.search" => render_files(&result),
        "file.open" => "Submitted open to shell.".into(),
        "git.status" => render_git_status(&result),
        "git.stage" => "Staged paths.".into(),
        "git.stage-hunk" => "Staged hunk.".into(),
        "git.unstage" => "Unstaged paths.".into(),
        "git.discard" => "Discarded paths.".into(),
        "git.commit" => format!("Committed {}.", string(&result, "oid")),
        "diff.show" => render_diff(&result),
        "diff.list-files" => render_diff_files(&result),
        "process.list" => render_process_list(&result),
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

fn render_root(result: &Value) -> String {
    let source = result.get("source").and_then(Value::as_str);
    match (result.get("root").and_then(Value::as_str), source) {
        (Some(root), Some(source)) => format!("Project root: {root} (source: {source})"),
        (_, Some(source)) => format!("No project root (source: {source})"),
        _ => "No project root.".into(),
    }
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

/// Compact split path for `pane list` rows: root-to-leaf summaries as
/// `shortid(axis,fraction)`, so a `pane.resize --split` target is discoverable
/// without a second query. The last entry resizes the row's pane.
fn format_splits(splits: Option<&Value>) -> String {
    let Some(items) = splits.and_then(Value::as_array) else {
        return "-".into();
    };
    if items.is_empty() {
        return "-".into();
    }
    items
        .iter()
        .map(|split| {
            let id = split.get("id").and_then(Value::as_str).unwrap_or("?");
            let short: String = id.chars().take(8).collect();
            let axis = match split.get("axis").and_then(Value::as_str) {
                Some("vertical") => "v",
                _ => "h",
            };
            let fraction = split.get("fraction").and_then(Value::as_f64).unwrap_or(0.5);
            format!("{short}({axis},{fraction:.2})")
        })
        .collect::<Vec<_>>()
        .join(",")
}

fn render_panes(result: &Value) -> String {
    let Some(items) = result.get("panes").and_then(Value::as_array) else {
        return "No panes.".into();
    };
    if items.is_empty() {
        return "No panes.".into();
    }
    let mut out = String::from("ID\tSESSION\tFOCUSED\tSPLITS\n");
    for item in items {
        out.push_str(&format!(
            "{}\t{}\t{}\t{}\n",
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
            format_splits(item.get("splits")),
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

fn render_files(result: &Value) -> String {
    let Some(items) = result.get("entries").and_then(Value::as_array) else {
        return "No files.".into();
    };
    if items.is_empty() {
        return "No files.".into();
    }
    let mut out = String::from("KIND\tPATH\n");
    for item in items {
        out.push_str(&format!(
            "{}\t{}\n",
            item.get("kind").and_then(Value::as_str).unwrap_or("-"),
            item.get("path").and_then(Value::as_str).unwrap_or("-"),
        ));
    }
    if result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        out.push_str("(truncated: bounded file listing)\n");
    }
    out.trim_end().to_owned()
}

/// Grouped git status: branch header plus staged/unstaged/untracked
/// sections with counts, mirroring the desktop Source Control panel.
fn render_git_status(result: &Value) -> String {
    let branch = result.get("branch").and_then(Value::as_str);
    let upstream = result.get("upstream").and_then(Value::as_str);
    let ahead = result.get("ahead").and_then(Value::as_u64).unwrap_or(0);
    let behind = result.get("behind").and_then(Value::as_u64).unwrap_or(0);
    let mut out = match (branch, upstream) {
        (Some(branch), Some(upstream)) => {
            format!("Branch: {branch} ({upstream}, ahead {ahead}, behind {behind})\n")
        }
        (Some(branch), None) => format!("Branch: {branch} (ahead {ahead}, behind {behind})\n"),
        (None, _) => "Not a git repository.\n".to_owned(),
    };
    let mut any = false;
    for (key, title) in [
        ("staged", "Staged"),
        ("unstaged", "Unstaged"),
        ("untracked", "Untracked"),
    ] {
        let items = result
            .get(key)
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default();
        if items.is_empty() {
            continue;
        }
        any = true;
        out.push_str(&format!("\n{title} ({})\n", items.len()));
        for item in &items {
            let path = item.get("path").and_then(Value::as_str).unwrap_or("-");
            let renamed = item.get("renamed_from").and_then(Value::as_str);
            match renamed {
                Some(from) => out.push_str(&format!("  {from} -> {path}\n")),
                None => out.push_str(&format!("  {path}\n")),
            }
        }
    }
    if !any {
        out.push_str("\nWorking tree clean.\n");
    }
    if result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        out.push_str("(truncated: bounded git status)\n");
    }
    out.trim_end().to_owned()
}

/// Unified diff: one section per file with `±` hunk bodies in plain
/// monospace (mirrors the desktop Diff tab; no highlighting in v0.2).
/// Binary files render the "binary, not shown" marker.
fn render_diff(result: &Value) -> String {
    let staged = result
        .get("staged")
        .and_then(Value::as_bool)
        .unwrap_or(false);
    let files = result
        .get("files")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    if files.is_empty() {
        return if staged {
            "No staged changes.".into()
        } else {
            "No changes.".into()
        };
    }
    let mut out = String::new();
    for file in &files {
        let path = file.get("path").and_then(Value::as_str).unwrap_or("-");
        let status = file.get("status").and_then(Value::as_str).unwrap_or("-");
        let binary = file.get("binary").and_then(Value::as_bool).unwrap_or(false);
        match file.get("old_path").and_then(Value::as_str) {
            Some(from) => out.push_str(&format!("diff --git {from} -> {path} ({status})\n")),
            None => out.push_str(&format!("diff --git {path} ({status})\n")),
        }
        if binary {
            out.push_str("binary, not shown\n");
            continue;
        }
        let hunks = file
            .get("hunks")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default();
        for hunk in &hunks {
            let (old_start, old_lines) = (
                hunk.get("old_start").and_then(Value::as_u64).unwrap_or(0),
                hunk.get("old_lines").and_then(Value::as_u64).unwrap_or(0),
            );
            let (new_start, new_lines) = (
                hunk.get("new_start").and_then(Value::as_u64).unwrap_or(0),
                hunk.get("new_lines").and_then(Value::as_u64).unwrap_or(0),
            );
            out.push_str(&format!(
                "@@ -{old_start},{old_lines} +{new_start},{new_lines} @@\n"
            ));
            let lines = hunk
                .get("lines")
                .and_then(Value::as_array)
                .cloned()
                .unwrap_or_default();
            for line in &lines {
                let marker = match line.get("kind").and_then(Value::as_str) {
                    Some("addition") => "+",
                    Some("deletion") => "-",
                    _ => " ",
                };
                out.push_str(&format!(
                    "{marker}{}\n",
                    line.get("text").and_then(Value::as_str).unwrap_or("")
                ));
            }
            if hunk
                .get("truncated")
                .and_then(Value::as_bool)
                .unwrap_or(false)
            {
                out.push_str("… (hunk truncated)\n");
            }
        }
        if file
            .get("truncated")
            .and_then(Value::as_bool)
            .unwrap_or(false)
        {
            out.push_str("… (file truncated)\n");
        }
    }
    if result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        out.push_str("(truncated: bounded diff)\n");
    }
    out.trim_end().to_owned()
}

/// Changed-file list with per-file status (the fast header-only
/// surface).
fn render_diff_files(result: &Value) -> String {
    let files = result
        .get("files")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    if files.is_empty() {
        return "No changed files.".into();
    }
    let mut out = String::new();
    for file in &files {
        let path = file.get("path").and_then(Value::as_str).unwrap_or("-");
        let status = file
            .get("status")
            .and_then(Value::as_str)
            .and_then(|status| status.chars().next())
            .unwrap_or('?');
        let hunks = file.get("hunk_count").and_then(Value::as_u64).unwrap_or(0);
        out.push_str(&format!("{status} {path} ({hunks} hunks)\n"));
    }
    if result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        out.push_str("(truncated: bounded diff)\n");
    }
    out.trim_end().to_owned()
}

fn render_process_list(result: &Value) -> String {
    let entries = result
        .get("entries")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    if entries.is_empty() {
        return "No live terminal processes.".into();
    }
    let mut out = String::new();
    for entry in &entries {
        let pid = entry.get("pid").and_then(Value::as_u64).unwrap_or(0);
        let name = entry.get("name").and_then(Value::as_str).unwrap_or("?");
        let pane = entry.get("pane_id").and_then(Value::as_str).unwrap_or("?");
        let ports = entry
            .get("ports")
            .and_then(Value::as_array)
            .map(|ports| {
                ports
                    .iter()
                    .filter_map(Value::as_u64)
                    .map(|port| port.to_string())
                    .collect::<Vec<_>>()
                    .join(",")
            })
            .unwrap_or_default();
        out.push_str(&format!("{pid}\t{name}\tpane={pane}\tports={ports}\n"));
    }
    if result
        .get("truncated")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        out.push_str("(truncated: bounded process list)\n");
    }
    out.trim_end().to_owned()
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
    fn human_project_root_renders_source_and_empty_state() {
        let rooted = human_success(
            "project.root",
            &ok(serde_json::json!({"root": "/repo", "source": "git"})),
        );
        assert!(rooted.contains("/repo"));
        assert!(rooted.contains("git"));
        let empty = human_success(
            "project.root",
            &ok(serde_json::json!({"root": null, "source": "none"})),
        );
        assert!(empty.contains("No project root"));
        assert!(empty.contains("none"));
    }

    #[test]
    fn human_process_list_renders_identity_and_truncation() {
        let text = human_success(
            "process.list",
            &ok(serde_json::json!({
                "entries": [{"pid":42,"name":"sleep","pane_id":"pane-a","ports":[8080]}],
                "truncated": true,
            })),
        );
        assert!(text.contains("42\tsleep\tpane=pane-a\tports=8080"));
        assert!(text.contains("truncated"));
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
                "panes": [{
                    "id": "p1", "session_id": "s1", "focused": true,
                    "splits": [{"id": "split-abcdef", "axis": "horizontal", "fraction": 0.45}],
                }],
                "truncated": true,
            })),
        );
        assert!(text.contains("p1"));
        assert!(text.contains("truncated"));
        assert!(text.contains("split-ab(h,0.45)"));
        let bare = human_success(
            "pane.list",
            &ok(serde_json::json!({
                "panes": [{"id": "p1", "session_id": "s1", "focused": false}],
                "truncated": false,
            })),
        );
        assert!(bare.contains("SPLITS"));

        // Unsplit root panes and pre-11F servers (no splits key) render "-".
        for value in [serde_json::json!({"splits": []}), serde_json::json!({})] {
            let text = human_success(
                "pane.list",
                &ok(
                    serde_json::json!({"panes": [{"id": "p", "splits": value["splits"]}], "truncated": false}),
                ),
            );
            assert!(text.contains("SPLITS"));
        }
        assert_eq!(format_splits(None), "-");
        assert_eq!(
            format_splits(Some(&serde_json::json!([
                {"id": "outer-id", "axis": "vertical", "fraction": 0.6},
                {"id": "inner-id", "axis": "horizontal", "fraction": 0.5},
            ]))),
            "outer-id(v,0.60),inner-id(h,0.50)"
        );
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
    fn human_file_outputs_render() {
        let list = human_success(
            "file.list",
            &ok(serde_json::json!({
                "entries": [
                    {"path": "src", "kind": "directory"},
                    {"path": "src/main.rs", "kind": "file"},
                ],
                "truncated": true,
            })),
        );
        assert!(list.contains("src/main.rs"));
        assert!(list.contains("directory"));
        assert!(list.contains("truncated"));
        let empty = human_success("file.search", &ok(serde_json::json!({"entries": []})));
        assert!(empty.contains("No files."));
        let opened = human_success("file.open", &ok(serde_json::json!({"submitted": true})));
        assert!(opened.contains("Submitted"));
    }

    #[test]
    fn human_git_outputs_render() {
        let status = human_success(
            "git.status",
            &ok(serde_json::json!({
                "branch": "main", "upstream": "origin/main",
                "ahead": 1, "behind": 0,
                "staged": [{"path": "a.txt", "x": "M", "y": "."}],
                "unstaged": [],
                "untracked": [{"path": "new.txt"}],
                "truncated": false,
            })),
        );
        assert!(status.contains("main"));
        assert!(status.contains("Staged (1)"));
        assert!(status.contains("a.txt"));
        assert!(status.contains("Untracked (1)"));
        let renamed = human_success(
            "git.status",
            &ok(serde_json::json!({
                "branch": "main", "ahead": 0, "behind": 0,
                "staged": [{"path": "after.txt", "renamed_from": "before.txt"}],
                "unstaged": [], "untracked": [], "truncated": true,
            })),
        );
        assert!(renamed.contains("before.txt -> after.txt"));
        assert!(renamed.contains("truncated"));
        let clean = human_success(
            "git.status",
            &ok(serde_json::json!({
                "branch": "main", "ahead": 0, "behind": 0,
                "staged": [], "unstaged": [], "untracked": [],
                "truncated": false,
            })),
        );
        assert!(clean.contains("clean"));
        let non_repo = human_success(
            "git.status",
            &ok(serde_json::json!({
                "branch": null, "ahead": 0, "behind": 0,
                "staged": [], "unstaged": [], "untracked": [],
                "truncated": false,
            })),
        );
        assert!(non_repo.contains("Not a git repository"));
        assert_eq!(
            human_success("git.stage", &ok(serde_json::json!({}))),
            "Staged paths."
        );
        assert_eq!(
            human_success("git.commit", &ok(serde_json::json!({"oid": "abc123"}))),
            "Committed abc123."
        );
    }

    #[test]
    fn human_diff_outputs_render() {
        let show = human_success(
            "diff.show",
            &ok(serde_json::json!({
                "files": [{
                    "path": "src/main.rs", "old_path": null,
                    "status": "modified", "binary": false,
                    "hunks": [{
                        "old_start": 1, "old_lines": 2,
                        "new_start": 1, "new_lines": 2,
                        "lines": [
                            {"kind": "context", "text": "ctx"},
                            {"kind": "deletion", "text": "old"},
                            {"kind": "addition", "text": "new"},
                        ],
                        "truncated": false,
                    }],
                    "hunk_count": 1, "truncated": false,
                }],
                "truncated": false, "staged": false,
            })),
        );
        assert!(show.contains("diff --git src/main.rs (modified)"));
        assert!(show.contains("@@ -1,2 +1,2 @@"));
        assert!(show.contains(" ctx"));
        assert!(show.contains("-old"));
        assert!(show.contains("+new"));
        let binary = human_success(
            "diff.show",
            &ok(serde_json::json!({
                "files": [{
                    "path": "logo.png", "old_path": null,
                    "status": "added", "binary": true,
                    "hunks": [], "hunk_count": 0, "truncated": false,
                }],
                "truncated": false, "staged": false,
            })),
        );
        assert!(binary.contains("binary, not shown"));
        let empty = human_success(
            "diff.show",
            &ok(serde_json::json!({"files": [], "truncated": false, "staged": true})),
        );
        assert!(empty.contains("No staged changes"));
        let files = human_success(
            "diff.list-files",
            &ok(serde_json::json!({
                "files": [
                    {"path": "a.txt", "status": "modified", "hunk_count": 2},
                    {"path": "b.txt", "status": "added", "hunk_count": 1},
                ],
                "truncated": true, "staged": false,
            })),
        );
        assert!(files.contains("m a.txt (2 hunks)"));
        assert!(files.contains("a b.txt (1 hunks)"));
        assert!(files.contains("truncated"));
    }

    #[test]
    fn local_error_envelope_is_stable() {
        let envelope =
            serde_json::json!({"ok": false, "error": {"code": "usage_error", "message": "bad"}});
        assert_eq!(envelope["ok"], false);
        assert_eq!(envelope["error"]["code"], "usage_error");
    }
}
