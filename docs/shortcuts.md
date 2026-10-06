# OmaTerm keyboard shortcuts

Single source of truth for humans; the machine-readable contract is
`apps/omaterm/src/shortcuts.rs` (`SHORTCUTS`), rendered in-app by the
`Alt+Shift+K` cheatsheet. `scripts/check-docs.py` fails CI when this table
and the registry disagree on ids.

Design rules:

- Plain `Ctrl` bytes belong to the PTY (readline kill-line, `Ctrl+2..7`
  control codes). No `Ctrl+digit` or `Ctrl+letter` chord is chrome, so bare
  `Ctrl` never shows jump digits.
- `Alt+1..9` jumps within the tab strip (terminal tabs, then the diff chip,
  then editor documents, in render order). Past-the-end slots are a truthful
  notice, never a fake tab.
- `Alt+Shift+1..9` jumps between projects in sidebar order.
- Hints are honest: sidebar digits appear only while `Alt+Shift` is held
  (labeled `Alt+Shift+n`), strip digits only while plain `Alt` is held.
- The Omarchy `Super+K` binding list is the model for the cheatsheet, but
  `Super` stays with the compositor, so the in-app chord is `Alt+Shift+K`.
  Plain `Ctrl+K` (`0x0B`) keeps reaching the shell.
- Closing the last terminal tab falls back to a live editor document, then
  the open diff preview, and only then the empty prompt.

| Id | Chord | Action | Context |
|---|---|---|---|
| `tab.jump` | `Alt+1..9` | Jump to the nth tab (terminal tabs, then diff, then editor docs) | Global |
| `project.jump` | `Alt+Shift+1..9` | Jump to the nth project in sidebar order | Global |
| `keys.cheatsheet` | `Alt+Shift+K` | Show this keybinding list | Global |
| `project.open` | `Ctrl+O` | Open project directory picker | Global |
| `project.new` | `Ctrl+Alt+N` | New project (home directory) | Global |
| `tab.new` | `Ctrl+Shift+T` | New tab | Global |
| `tab.close` | `Ctrl+Shift+Q` | Close current tab | Global |
| `tab.cycle` | `Ctrl+PageUp/PageDown` | Previous/next tab | Global |
| `project.cycle` | `Alt+PageUp/PageDown` | Previous/next project | Global |
| `pane.split-right` | `Ctrl+Shift+R` | Split focused pane right | Terminal |
| `pane.split-down` | `Ctrl+Shift+D` | Split focused pane down | Terminal |
| `pane.close` | `Ctrl+Shift+W` | Close focused pane | Terminal |
| `pane.focus` | `Ctrl+Shift+H/J/K/L` | Focus left/down/up/right pane | Terminal |
| `pane.resize` | `Ctrl+[/]` | Shrink/grow focused pane | Terminal |
| `panel.projects` | `Ctrl+B` | Toggle Projects sidebar | Global |
| `panel.inspector` | `Ctrl+Shift+B` | Toggle Inspector panel | Global |
| `panel.inspector-tabs` | `Ctrl+Shift+E/G/I` | Inspector Files/Git/Info tab | Global |
| `finder.files` | `Ctrl+P` | File finder | Global |
| `palette.commands` | `Ctrl+Shift+P` | Command palette | Global |
| `diff.hunk-nav` | `Alt+N/P` | Next/previous hunk | Diff preview |
| `diff.hunk-stage` | `Ctrl+Shift+S` | Stage selected hunk | Diff preview |
| `diff.hunk-copy` | `Ctrl+Shift+C` | Copy hunk, selection, or editor text | Diff/Terminal/Editor |
| `terminal.paste` | `Ctrl+Shift+V` | Paste (two-step for risky content) | Terminal |
| `terminal.scroll` | `Shift+PageUp/PageDown` | Scrollback (not in alt-screen) | Terminal |
| `history.opt-in` | `Ctrl+Shift+O` | Encrypted history opt-in/out (two-step) | Global |
| `history.pause` | `Ctrl+Shift+G` | Pause/resume history capture | Global |
| `history.clear` | `Ctrl+Shift+X` | Clear pane history (two-step) | Global |
| `files.copy-path` | `Ctrl+Shift+Y` | Copy selected tree path | Files |
| `files.reveal` | `Ctrl+Shift+U` | Reveal selected path in terminal | Files |
| `editor.save` | `Ctrl+S` | Save document | Editor |
| `editor.undo` | `Ctrl+Z` | Undo | Editor |
| `editor.redo` | `Ctrl+Shift+Z / Ctrl+Y` | Redo | Editor |
| `editor.clipboard` | `Ctrl+A/X/C/V` | Select all, cut, copy, paste | Editor |
| `git.nav` | `Alt+Up/Down/Enter` | Move Git selection / open diff | Git tab |
| `overlay.dismiss` | `Esc` | Close overlay or dialog | Overlay |

Notes:

- `Ctrl+Shift+E` is the Inspector Files tab (the old equalize arm was dead
  code shadowed by it; equalize remains via the `Equalize Panes` palette
  command, IPC, and CLI).
- `Ctrl+Shift+T` is new-tab (the old empty-workspace terminal arm was dead
  code shadowed by it).
- On machines where `Alt+Shift` switches keyboard layouts, the palette
  `Show Keybindings` command and the status-bar `Alt+Shift+K keys` pill are
  the fallback triggers.
