# Milestone 3 — Single Terminal

> Get one real terminal working: PTY, alacritty_terminal, GPUI rendering, keyboard input, resize, scrollback.

## Overview

This is the most technically challenging milestone. It integrates three complex subsystems: PTY management, terminal emulation (via `alacritty_terminal`), and GPU-accelerated terminal rendering in GPUI.

The terminal engine is wrapped behind an OmaTerm abstraction (`TerminalEngine` trait) so the rest of the application never depends directly on `alacritty_terminal`.

## Goals

- [ ] `omaterm-terminal` crate created
- [ ] `TerminalEngine` trait defined (OmaTerm's abstraction)
- [ ] `AlacrittyEngine` implements `TerminalEngine`
- [ ] `PtyProcess` manages real PTY + child shell
- [ ] `TerminalSession` ties PTY + engine together
- [ ] GPUI renders terminal cells (text, colors, cursor)
- [ ] Keyboard input flows to PTY
- [ ] Terminal resizes correctly when window/pane resizes
- [ ] Scrollback works
- [ ] User's shell (`$SHELL`) launches correctly

## Prerequisites

- Milestone 2 complete (pane tree renders placeholder panes)

## Deliverables

### New Crate: `omaterm-terminal`

```
crates/omaterm-terminal/
├── Cargo.toml
└── src/
    ├── lib.rs
    ├── engine.rs       # TerminalEngine trait
    ├── alacritty.rs    # AlacrittyEngine implementation
    ├── pty.rs          # PtyProcess — PTY master + child
    ├── session.rs      # TerminalSession — durable terminal identity
    └── events.rs       # Terminal events
```

### Key Types

```rust
// engine.rs — OmaTerm's terminal abstraction
pub trait TerminalEngine {
    fn resize(&mut self, cols: u16, rows: u16);
    fn write_input(&mut self, bytes: &[u8]);
    fn viewport(&self) -> TerminalViewport;
    fn read_visible_text(&self, max_lines: usize, max_columns: usize) -> String;
    fn scroll(&mut self, command: ScrollCommand);
    fn title(&self) -> Option<&str>;
}

pub struct TerminalViewport {
    pub rows: Vec<TerminalRow>,
    pub cursor: CursorState,
    pub cols: u16,
    pub lines: u16,
}

pub struct TerminalRow {
    pub cells: Vec<TerminalCell>,
}

pub struct TerminalCell {
    pub character: char,
    pub fg: Color,
    pub bg: Color,
    pub flags: CellFlags, // bold, italic, underline, etc.
}

// pty.rs
pub struct PtyProcess {
    master: OwnedFd,
    child_pid: Pid,
    // ...
}

// session.rs
pub struct TerminalSession {
    pub id: SessionId,
    pub working_directory: PathBuf,
    pty: PtyProcess,
    engine: Box<dyn TerminalEngine>,
    // ...
}
```

### Terminal Rendering in GPUI

The terminal view component in `omaterm-ui` reads the `TerminalViewport` and renders:

- Monospace text grid
- Foreground/background colors per cell
- Cursor (block, underline, bar)
- Selection highlighting (later)
- Scrollbar or scroll indicator

```
TerminalSession
  ├── PtyProcess (async read/write)
  ├── TerminalEngine (alacritty_terminal)
  └── rendered by TerminalView (GPUI component)
```

## Architecture Notes

### Terminal Layer Separation

```
PTY byte stream → TerminalEngine (VT parsing) → TerminalViewport → GPUI Renderer
     ↑                                                                    │
     └──────────────── keyboard input ─────────────────────────────────────┘
```

### Async Considerations

- PTY read loop runs off the main thread (never block GPUI rendering)
- Terminal state updates are synchronized to the render thread
- Use channels or GPUI's task/executor patterns

### Shell Launch

```rust
// Resolve shell
let shell = std::env::var("SHELL")
    .unwrap_or_else(|_| "/bin/bash".to_string());

// Environment variables to inject
env::set_var("TERM", "xterm-256color");
env::set_var("TERM_PROGRAM", "OmaTerm");
env::set_var("OMATERM", "1");
```

### Hidden Pane Behavior

Even in this milestone (single terminal), establish the pattern:
- PTY output always drains (even if view isn't visible)
- Rendering only happens for visible terminals

## Implementation Steps

1. **Create `omaterm-terminal` crate**
2. **Implement `PtyProcess`** — fork, exec shell, manage master FD, async read/write
3. **Define `TerminalEngine` trait** — OmaTerm's abstraction over terminal emulators
4. **Implement `AlacrittyEngine`** — wrap `alacritty_terminal::Term` behind the trait
5. **Implement `TerminalSession`** — tie PTY + engine + metadata together
6. **Create PTY read loop** — async task draining PTY output into the engine
7. **Create `TerminalView`** GPUI component — renders `TerminalViewport` as text grid
8. **Wire keyboard input** — key events → bytes → PTY write
9. **Implement resize** — window/pane resize → PTY + engine resize
10. **Implement scrollback** — scroll commands, viewport offset
11. **Test with real shells** — zsh, bash, fish
12. **Test TUI applications** — vim, htop, etc.

## Acceptance Criteria

- [ ] Running `cargo run` opens OmaTerm with a working shell
- [ ] User's `$SHELL` launches (zsh/bash/fish)
- [ ] Typing produces characters in the terminal
- [ ] Shell prompt renders correctly (including starship/powerlevel10k)
- [ ] Colors render (ANSI 256-color minimum)
- [ ] Cursor is visible and positioned correctly
- [ ] Terminal resizes correctly when window is resized
- [ ] Scrollback works (scroll up/down through history)
- [ ] TUI apps work: `vim`, `htop`, `top`, `less`
- [ ] `Ctrl+C`, `Ctrl+D`, `Ctrl+Z` work correctly
- [ ] Shell exits cleanly when `exit` is typed
- [ ] No CPU spin when terminal is idle
- [ ] `omaterm-terminal` does NOT depend on `gpui`

## Non-Goals

- No multiple terminals (just one fullscreen terminal)
- No copy/paste
- No search
- No selection
- No mouse support
- No image protocol
- No IME (can be added later)
- No pane splitting active in this milestone

## References

- Blueprint §9 — Terminal Engine Decision
- Blueprint §10 — Terminal Architecture
- Blueprint §11 — PTY Design
- Blueprint §26 — Async / Concurrency
- Blueprint §27 — Rendering Strategy
- Blueprint §28 — Hidden Pane Behavior
- Blueprint §29 — Terminal Scrollback
- Blueprint §68, Slice 3 — One Terminal
- Zed terminal crate (reference only): https://github.com/zed-industries/zed/tree/main/crates/terminal
- `alacritty_terminal` docs
