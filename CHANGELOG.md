# Changelog

The section of a version becomes the release notes of its update, so each
one is written for the user: what changed, and why it matters.

## 0.6.1

### Releases build themselves

A tag now builds every platform and uploads it, signed, to a draft
release: macOS, Windows, Linux and Linux ARM64. CI checks each push and
pull request, so a broken build is caught before it reaches a release.

## 0.6.0

### Updates from inside the app

oterm can now update itself. It checks the signed releases of the project
in the background, once a day, and offers a newer version with its notes:
install it, skip that version, or be reminded later.

**Info → UPDATES** checks on demand and shows the running version, and
**Check for Updates…** in the command palette does the same. Only releases
signed with the project's key install, so a download from anywhere else is
refused.

### Simpler keybindings

The keybindings are now one short rule each way: **Ctrl** does things, and
**Shift** picks the other one of a pair, while **Alt** goes places.

- `Alt+1` … `Alt+9` jump to a tab, editor or diff; `Alt+Shift+1` … `Alt+Shift+9`
  switch project.
- `Ctrl+Shift+E`, `Ctrl+Shift+G` and `Ctrl+Shift+I` open the Files, Git and
  Info panels.
- `Ctrl+=`, `Ctrl+-` and `Ctrl+0` size the terminal and the editor together.
- `Ctrl+Shift+/` opens the searchable Keyboard Shortcuts dialog.

Chords, function keys and the second key per command are gone. Anything
without a key is still in the command palette.
