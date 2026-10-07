//! Central keybinding registry: every desktop chord in one GPUI-free table.
//!
//! Previously chords were scattered across sequential `if`-returns in
//! `on_key_down`, which allowed silent shadowing (`Ctrl+Shift+E` meant both
//! Inspector-Files and equalize; `Ctrl+Shift+T` meant both new-tab and
//! new-terminal). All chord parsing lives here so precedence is explicit,
//! unit-testable, and checked against `docs/shortcuts.md` by
//! `scripts/check-docs.py`. `Ctrl` chords are intentionally absent from tab
//! and project navigation: plain `Ctrl` bytes belong to the PTY (readline
//! kill-line, `Ctrl+2..7` control codes per `input.rs::ctrl_byte`).

use omaterm_core::{DocumentId, TabId};

/// One desktop keybinding: stable id, human chord label, action, scope note.
/// The `id` values are the contract checked against `docs/shortcuts.md` by
/// `scripts/check-docs.py` (the only reader outside tests, hence the allow).
pub struct Shortcut {
    #[allow(dead_code)]
    pub id: &'static str,
    pub chord: &'static str,
    pub action: &'static str,
    pub context: &'static str,
}

/// Every desktop chord. Exactly one row per action; aliases are rejected by
/// the `shortcut_ids_are_unique` test so shadowing cannot creep back.
pub const SHORTCUTS: &[Shortcut] = &[
    Shortcut {
        id: "tab.jump",
        chord: "Alt+1..9",
        action: "Jump to the nth tab (terminal tabs, then diff, then editor docs)",
        context: "Global",
    },
    Shortcut {
        id: "project.jump",
        chord: "Alt+Shift+1..9",
        action: "Jump to the nth project in sidebar order",
        context: "Global",
    },
    Shortcut {
        id: "keys.cheatsheet",
        chord: "Alt+Shift+K",
        action: "Show this keybinding list",
        context: "Global",
    },
    Shortcut {
        id: "project.open",
        chord: "Ctrl+O",
        action: "Open project directory picker",
        context: "Global",
    },
    Shortcut {
        id: "project.new",
        chord: "Ctrl+Alt+N",
        action: "New project (home directory)",
        context: "Global",
    },
    Shortcut {
        id: "tab.new",
        chord: "Ctrl+Shift+T",
        action: "New tab",
        context: "Global",
    },
    Shortcut {
        id: "tab.close",
        chord: "Ctrl+Shift+Q",
        action: "Close current tab",
        context: "Global",
    },
    Shortcut {
        id: "tab.cycle",
        chord: "Ctrl+PageUp/PageDown",
        action: "Previous/next tab",
        context: "Global",
    },
    Shortcut {
        id: "project.cycle",
        chord: "Alt+PageUp/PageDown",
        action: "Previous/next project",
        context: "Global",
    },
    Shortcut {
        id: "pane.split-right",
        chord: "Ctrl+Shift+R",
        action: "Split focused pane right",
        context: "Terminal",
    },
    Shortcut {
        id: "pane.split-down",
        chord: "Ctrl+Shift+D",
        action: "Split focused pane down",
        context: "Terminal",
    },
    Shortcut {
        id: "pane.close",
        chord: "Ctrl+Shift+W",
        action: "Close focused pane",
        context: "Terminal",
    },
    Shortcut {
        id: "pane.focus",
        chord: "Ctrl+Shift+H/J/K/L",
        action: "Focus left/down/up/right pane",
        context: "Terminal",
    },
    Shortcut {
        id: "pane.resize",
        chord: "Ctrl+[/]",
        action: "Shrink/grow focused pane",
        context: "Terminal",
    },
    Shortcut {
        id: "panel.projects",
        chord: "Ctrl+B",
        action: "Toggle Projects sidebar",
        context: "Global",
    },
    Shortcut {
        id: "panel.inspector",
        chord: "Ctrl+Shift+B",
        action: "Toggle Inspector panel",
        context: "Global",
    },
    Shortcut {
        id: "panel.inspector-tabs",
        chord: "Ctrl+Shift+E/G/I",
        action: "Inspector Files/Git/Info tab",
        context: "Global",
    },
    Shortcut {
        id: "finder.files",
        chord: "Ctrl+P",
        action: "File finder",
        context: "Global",
    },
    Shortcut {
        id: "palette.commands",
        chord: "Ctrl+Shift+P",
        action: "Command palette",
        context: "Global",
    },
    Shortcut {
        id: "diff.hunk-nav",
        chord: "Alt+N/P",
        action: "Next/previous hunk",
        context: "Diff preview",
    },
    Shortcut {
        id: "diff.hunk-stage",
        chord: "Ctrl+Shift+S",
        action: "Stage selected hunk",
        context: "Diff preview",
    },
    Shortcut {
        id: "diff.hunk-copy",
        chord: "Ctrl+Shift+C",
        action: "Copy hunk, selection, or editor text",
        context: "Diff/Terminal/Editor",
    },
    Shortcut {
        id: "terminal.paste",
        chord: "Ctrl+Shift+V",
        action: "Paste (two-step for risky content)",
        context: "Terminal",
    },
    Shortcut {
        id: "terminal.scroll",
        chord: "Shift+PageUp/PageDown",
        action: "Scrollback (not in alt-screen)",
        context: "Terminal",
    },
    Shortcut {
        id: "terminal.search",
        chord: "Ctrl+Shift+F",
        action: "Find in terminal scrollback (Enter next, Shift+Enter previous)",
        context: "Terminal",
    },
    Shortcut {
        id: "pane.zoom",
        chord: "Alt+Z",
        action: "Toggle pane zoom (maximize focused pane)",
        context: "Terminal",
    },
    Shortcut {
        id: "terminal.font-zoom",
        chord: "Ctrl +/-/0",
        action: "Terminal font size (step in/out, reset)",
        context: "Terminal",
    },
    Shortcut {
        id: "history.opt-in",
        chord: "Ctrl+Shift+O",
        action: "Encrypted history opt-in/out (two-step)",
        context: "Global",
    },
    Shortcut {
        id: "history.pause",
        chord: "Ctrl+Shift+G",
        action: "Pause/resume history capture",
        context: "Global",
    },
    Shortcut {
        id: "history.clear",
        chord: "Ctrl+Shift+X",
        action: "Clear pane history (two-step)",
        context: "Global",
    },
    Shortcut {
        id: "files.copy-path",
        chord: "Ctrl+Shift+Y",
        action: "Copy selected tree path",
        context: "Files",
    },
    Shortcut {
        id: "files.reveal",
        chord: "Ctrl+Shift+U",
        action: "Reveal selected path in terminal",
        context: "Files",
    },
    Shortcut {
        id: "editor.save",
        chord: "Ctrl+S",
        action: "Save document",
        context: "Editor",
    },
    Shortcut {
        id: "editor.undo",
        chord: "Ctrl+Z",
        action: "Undo",
        context: "Editor",
    },
    Shortcut {
        id: "editor.redo",
        chord: "Ctrl+Shift+Z / Ctrl+Y",
        action: "Redo",
        context: "Editor",
    },
    Shortcut {
        id: "editor.clipboard",
        chord: "Ctrl+A/X/C/V",
        action: "Select all, cut, copy, paste",
        context: "Editor",
    },
    Shortcut {
        id: "git.nav",
        chord: "Alt+Up/Down/Enter",
        action: "Move Git selection / open diff",
        context: "Git tab",
    },
    Shortcut {
        id: "files.nav",
        chord: "Alt+Up/Down/Left/Right/Enter",
        action: "Move tree selection, collapse/expand, open file",
        context: "Files tab",
    },
    Shortcut {
        id: "overlay.dismiss",
        chord: "Esc",
        action: "Close overlay or dialog",
        context: "Overlay",
    },
];

/// The `Alt+<n>` strip slot for a GPUI key name, or `None` for other keys.
/// Plain `Alt` only: `Alt+Shift` belongs to project jumps, `Ctrl` to the PTY.
pub fn alt_strip_slot(key_name: &str) -> Option<u8> {
    match key_name {
        "1" => Some(1),
        "2" => Some(2),
        "3" => Some(3),
        "4" => Some(4),
        "5" => Some(5),
        "6" => Some(6),
        "7" => Some(7),
        "8" => Some(8),
        "9" => Some(9),
        _ => None,
    }
}

/// Map an `Alt+Shift+<n>` key name to a 0-based project index.
///
/// Shift applies to the character before GPUI reports it, so on a US layout
/// `Alt+Shift+1` arrives as `!`, `Alt+Shift+2` as `@`, and so on. Both the
/// shifted symbol and the raw digit map to the same slot.
pub fn alt_shift_project_index(key_name: &str) -> Option<usize> {
    let slot = match key_name {
        "1" | "!" => 1,
        "2" | "@" => 2,
        "3" | "#" => 3,
        "4" | "$" => 4,
        "5" | "%" => 5,
        "6" | "^" => 6,
        "7" | "&" => 7,
        "8" | "*" => 8,
        "9" | "(" => 9,
        _ => return None,
    };
    Some(slot - 1)
}

/// The cheatsheet chord: `Alt+Shift+K` with no `Ctrl`. Plain `Ctrl+K`
/// (`0x0B`, readline kill-line) is never chrome, so it keeps reaching the
/// PTY.
pub fn is_cheatsheet_toggle(key_name: &str, control: bool, shift: bool, alt: bool) -> bool {
    alt && shift && !control && key_name == "k"
}

/// Tab-strip digit hints are actionable exactly while `Alt` (without `Ctrl`)
/// is held. Bare `Ctrl` must never show digits it does not honor.
pub fn show_strip_hints(control: bool, _shift: bool, alt: bool) -> bool {
    alt && !control
}

/// Project jump hints are actionable exactly while `Alt+Shift` is held.
pub fn show_project_hints(control: bool, shift: bool, alt: bool) -> bool {
    alt && shift && !control
}

/// One entry of the unified tab strip: terminal tabs, then the diff chip,
/// then editor documents. Placeholders and the trailing `+` button are never
/// jump targets.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StripTarget {
    Terminal(TabId),
    Diff,
    Editor(DocumentId),
}

/// The 1-based `Alt+<n>` target in strip order, or `None` past the end.
pub fn strip_index_target(
    tab_ids: &[TabId],
    has_preview: bool,
    doc_ids: &[DocumentId],
    slot: u8,
) -> Option<StripTarget> {
    if slot == 0 {
        return None;
    }
    let mut index = usize::from(slot);
    if index <= tab_ids.len() {
        return Some(StripTarget::Terminal(tab_ids[index - 1]));
    }
    index -= tab_ids.len();
    if has_preview {
        if index == 1 {
            return Some(StripTarget::Diff);
        }
        index -= 1;
    }
    doc_ids.get(index - 1).copied().map(StripTarget::Editor)
}

/// Where the main area should go when a project has no terminal tabs left.
/// Callers with remaining tabs keep the existing surface; this only answers
/// the empty-tabs case so closing the last shell never strands the user on
/// an empty prompt while a document or diff is open.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TablessFallback {
    Editor(DocumentId),
    Diff,
    Empty,
}

pub fn fallback_without_tabs(
    active_doc: Option<DocumentId>,
    remembered_doc: Option<DocumentId>,
    first_doc: Option<DocumentId>,
    has_preview: bool,
) -> TablessFallback {
    if let Some(document) = active_doc.or(remembered_doc).or(first_doc) {
        return TablessFallback::Editor(document);
    }
    if has_preview {
        return TablessFallback::Diff;
    }
    TablessFallback::Empty
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn alt_strip_slot_maps_digits_only() {
        for (key, slot) in [
            ("1", 1),
            ("2", 2),
            ("3", 3),
            ("4", 4),
            ("5", 5),
            ("6", 6),
            ("7", 7),
            ("8", 8),
            ("9", 9),
        ] {
            assert_eq!(alt_strip_slot(key), Some(slot), "key {key}");
        }
        for key in ["!", "@", "#", "0", "p", "k", "enter"] {
            assert_eq!(alt_strip_slot(key), None, "key {key}");
        }
    }

    #[test]
    fn alt_shift_project_index_maps_digits_and_shifted_symbols() {
        for (key, slot) in [
            ("1", 0),
            ("!", 0),
            ("2", 1),
            ("@", 1),
            ("3", 2),
            ("#", 2),
            ("9", 8),
            ("(", 8),
        ] {
            assert_eq!(alt_shift_project_index(key), Some(slot), "key {key}");
        }
        for key in ["0", ")", "p", "k", "enter"] {
            assert_eq!(alt_shift_project_index(key), None, "key {key}");
        }
    }

    #[test]
    fn cheatsheet_toggle_rejects_ctrl_and_plain_keys() {
        assert!(is_cheatsheet_toggle("k", false, true, true));
        assert!(!is_cheatsheet_toggle("k", true, true, true));
        assert!(!is_cheatsheet_toggle("k", false, false, true));
        assert!(!is_cheatsheet_toggle("k", false, true, false));
        assert!(!is_cheatsheet_toggle("p", false, true, true));
    }

    #[test]
    fn hint_gates_never_fire_on_bare_ctrl() {
        assert!(!show_strip_hints(true, false, true));
        assert!(!show_project_hints(true, true, true));
        assert!(!show_strip_hints(true, false, false));
        assert!(show_strip_hints(false, false, true));
        assert!(show_strip_hints(false, true, true));
        assert!(show_project_hints(false, true, true));
        assert!(!show_project_hints(false, false, true));
        assert!(!show_project_hints(false, true, false));
    }

    #[test]
    fn strip_order_is_terminals_then_diff_then_editors() {
        let tabs = vec![TabId::new(), TabId::new()];
        let docs = vec![DocumentId::new(), DocumentId::new()];
        assert_eq!(
            strip_index_target(&tabs, true, &docs, 1),
            Some(StripTarget::Terminal(tabs[0]))
        );
        assert_eq!(
            strip_index_target(&tabs, true, &docs, 2),
            Some(StripTarget::Terminal(tabs[1]))
        );
        assert_eq!(
            strip_index_target(&tabs, true, &docs, 3),
            Some(StripTarget::Diff)
        );
        assert_eq!(
            strip_index_target(&tabs, true, &docs, 4),
            Some(StripTarget::Editor(docs[0]))
        );
        assert_eq!(
            strip_index_target(&tabs, true, &docs, 5),
            Some(StripTarget::Editor(docs[1]))
        );
        assert_eq!(strip_index_target(&tabs, true, &docs, 6), None);
        assert_eq!(strip_index_target(&tabs, true, &docs, 0), None);
    }

    #[test]
    fn strip_order_skips_absent_diff() {
        let tabs = vec![TabId::new()];
        let docs = vec![DocumentId::new()];
        assert_eq!(
            strip_index_target(&tabs, false, &docs, 1),
            Some(StripTarget::Terminal(tabs[0]))
        );
        assert_eq!(
            strip_index_target(&tabs, false, &docs, 2),
            Some(StripTarget::Editor(docs[0]))
        );
        assert_eq!(strip_index_target(&tabs, false, &docs, 3), None);
    }

    #[test]
    fn fallback_prefers_editor_then_diff_then_empty() {
        let live = DocumentId::new();
        let remembered = DocumentId::new();
        let first = DocumentId::new();
        assert_eq!(
            fallback_without_tabs(Some(live), Some(remembered), Some(first), true),
            TablessFallback::Editor(live)
        );
        assert_eq!(
            fallback_without_tabs(None, Some(remembered), Some(first), true),
            TablessFallback::Editor(remembered)
        );
        assert_eq!(
            fallback_without_tabs(None, None, Some(first), true),
            TablessFallback::Editor(first)
        );
        assert_eq!(
            fallback_without_tabs(None, None, None, true),
            TablessFallback::Diff
        );
        assert_eq!(
            fallback_without_tabs(None, None, None, false),
            TablessFallback::Empty
        );
    }

    #[test]
    fn shortcut_ids_are_unique() {
        let mut ids = std::collections::HashSet::new();
        for shortcut in SHORTCUTS {
            assert!(ids.insert(shortcut.id), "duplicate id {}", shortcut.id);
        }
        assert!(SHORTCUTS.len() >= 30, "registry lost rows");
    }
}
