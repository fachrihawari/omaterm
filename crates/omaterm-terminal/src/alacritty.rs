use std::sync::{Arc, Mutex};

use alacritty_terminal::event::{Event, EventListener};
use alacritty_terminal::grid::{Dimensions, Scroll};
use alacritty_terminal::term::{Config, Term, TermMode};
use alacritty_terminal::vte::ansi::{
    Color as AlacColor, CursorShape as AlacCursorShape, NamedColor,
};

use crate::engine::{
    CellFlags, CellWidth, CursorShape, CursorState, EngineOutput, MouseMode, MouseModeKind,
    ScrollCommand, TermColor, TerminalCell, TerminalEngine, TerminalRow, TerminalViewport,
};
use crate::events::TerminalEvent;

/// Listener collecting engine events. `send_event(&self)` requires interior
/// mutability; a cheap `Clone` handle shared with the `Term`.
#[derive(Debug, Default, Clone)]
struct EngineListener {
    queue: Arc<Mutex<Vec<Event>>>,
}

impl EngineListener {
    fn drain(&self) -> Vec<Event> {
        self.queue
            .lock()
            .map(|mut q| std::mem::take(&mut *q))
            .unwrap_or_default()
    }
}

impl EventListener for EngineListener {
    fn send_event(&self, event: Event) {
        if let Ok(mut queue) = self.queue.lock() {
            queue.push(event);
        }
    }
}

/// Minimal `Dimensions` for `Term::new` / `Term::resize`.
#[derive(Debug, Clone, Copy)]
struct TermSize {
    cols: usize,
    lines: usize,
}

impl Dimensions for TermSize {
    fn total_lines(&self) -> usize {
        self.lines
    }
    fn screen_lines(&self) -> usize {
        self.lines
    }
    fn columns(&self) -> usize {
        self.cols
    }
}

/// `TerminalEngine` backed by `alacritty_terminal::Term`.
pub struct AlacrittyEngine {
    term: Term<EngineListener>,
    listener: EngineListener,
    processor: alacritty_terminal::vte::ansi::Processor,
    title: Option<String>,
}

impl AlacrittyEngine {
    /// Create with the given grid size. `scrollback_lines` defaults to 10 000.
    pub fn new(cols: u16, rows: u16) -> Self {
        Self::with_scrollback(cols, rows, 10_000)
    }

    pub fn with_scrollback(cols: u16, rows: u16, scrollback_lines: usize) -> Self {
        let cols = cols.max(2) as usize;
        let rows = rows.max(1) as usize;
        let listener = EngineListener::default();
        let config = Config {
            scrolling_history: scrollback_lines,
            ..Default::default()
        };
        let size = TermSize { cols, lines: rows };
        let mut term = Term::new(config, &size, listener.clone());
        // OmaTerm embeds a focused terminal; alacritty leaves it unfocused by default.
        term.is_focused = true;
        Self {
            term,
            listener,
            processor: alacritty_terminal::vte::ansi::Processor::new(),
            title: None,
        }
    }

    fn translate(&mut self, event: Event, out: &mut EngineOutput) {
        match event {
            Event::Title(title) => {
                self.title = Some(title.clone());
                out.events.push(TerminalEvent::TitleChanged(title));
            }
            Event::ResetTitle => {
                self.title = None;
                out.events.push(TerminalEvent::TitleReset);
            }
            Event::Bell => out.events.push(TerminalEvent::Bell),
            Event::Wakeup => out.events.push(TerminalEvent::Wakeup),
            Event::CursorBlinkingChange => out.events.push(TerminalEvent::CursorBlinkingChanged),
            Event::ChildExit(status) => out.events.push(TerminalEvent::ChildExited(status)),
            Event::Exit => out.events.push(TerminalEvent::ExitRequested),
            Event::PtyWrite(text) => out.reply_bytes.extend_from_slice(text.as_bytes()),
            Event::ColorRequest(index, formatter) => {
                let rgb = resolve_index_color(&self.term, index);
                out.reply_bytes.extend_from_slice(formatter(rgb).as_bytes());
            }
            // M3 non-goals / deferred: no host clipboard read (OSC 52 paste),
            // no text-area size answer. Ignoring is safe (no reply sent).
            Event::ClipboardStore(_, _)
            | Event::ClipboardLoad(_, _)
            | Event::TextAreaSizeRequest(_)
            | Event::MouseCursorDirty => {}
        }
    }
}

impl TerminalEngine for AlacrittyEngine {
    fn app_cursor(&self) -> bool {
        self.term.mode().contains(TermMode::APP_CURSOR)
    }

    fn app_keypad(&self) -> bool {
        self.term.mode().contains(TermMode::APP_KEYPAD)
    }

    fn bracketed_paste(&self) -> bool {
        self.term.mode().contains(TermMode::BRACKETED_PASTE)
    }

    fn mouse_mode(&self) -> MouseMode {
        let mode = self.term.mode();
        let kind = if mode.contains(TermMode::MOUSE_MOTION) {
            MouseModeKind::Motion
        } else if mode.contains(TermMode::MOUSE_DRAG) {
            MouseModeKind::Drag
        } else if mode.contains(TermMode::MOUSE_REPORT_CLICK) {
            MouseModeKind::Click
        } else {
            MouseModeKind::Off
        };
        MouseMode {
            kind,
            sgr: mode.contains(TermMode::SGR_MOUSE),
        }
    }

    fn resize(&mut self, cols: u16, rows: u16) {
        let size = TermSize {
            cols: cols.max(2) as usize,
            lines: rows.max(1) as usize,
        };
        self.term.resize(size);
    }

    fn advance_output(&mut self, bytes: &[u8]) -> EngineOutput {
        self.processor.advance(&mut self.term, bytes);
        let drained = self.listener.drain();
        let mut out = EngineOutput::default();
        for event in drained {
            self.translate(event, &mut out);
        }
        out
    }

    fn viewport(&self) -> TerminalViewport {
        let content = self.term.renderable_content();
        let cols = self.term.columns() as u16;
        let lines = self.term.screen_lines() as u16;
        let offset = content.display_offset as i32;

        // Group display_iter cells by line. Lines are history-relative
        // (visible range is [-offset, -offset + screen_lines)).
        use std::collections::BTreeMap;
        let mut by_line: BTreeMap<i32, Vec<(usize, TerminalCell)>> = BTreeMap::new();
        for indexed in content.display_iter {
            let line = indexed.point.line.0;
            let col = indexed.point.column.0;
            by_line
                .entry(line)
                .or_default()
                .push((col, map_cell(&self.term, indexed.cell)));
        }
        let mut rows = Vec::with_capacity(lines as usize);
        for line in (-offset)..(-offset + lines as i32) {
            let mut cells = vec![
                TerminalCell {
                    text: " ".to_string(),
                    width: CellWidth::Single,
                    fg: TermColor::DefaultFg,
                    bg: TermColor::DefaultBg,
                    flags: CellFlags::default(),
                };
                cols as usize
            ];
            if let Some(entries) = by_line.get(&line) {
                for (col, cell) in entries {
                    if *col < cells.len() {
                        cells[*col] = cell.clone();
                    }
                }
            }
            rows.push(TerminalRow { cells });
        }

        // Cursor: history-relative line -> viewport row.
        let cursor_line = content.cursor.point.line.0;
        let cursor_col = content.cursor.point.column.0;
        let row = (cursor_line + offset).clamp(0, lines as i32 - 1) as u16;
        let col = cursor_col.min(cols.saturating_sub(1) as usize) as u16;
        let (shape, visible) = match content.cursor.shape {
            AlacCursorShape::Block => (CursorShape::Block, true),
            AlacCursorShape::Underline => (CursorShape::Underline, true),
            AlacCursorShape::Beam => (CursorShape::Bar, true),
            AlacCursorShape::HollowBlock => (CursorShape::Block, true),
            AlacCursorShape::Hidden => (CursorShape::Hidden, false),
        };

        TerminalViewport {
            rows,
            cursor: CursorState {
                row,
                col,
                shape,
                visible,
            },
            cols,
            lines,
            cursor_color: {
                let color = resolve_index_color(&self.term, NamedColor::Cursor as usize);
                (color.r, color.g, color.b)
            },
            display_offset: content.display_offset,
            history_size: self.term.history_size(),
            is_alt_screen: self.is_alt_screen(),
        }
    }

    fn read_visible_text(&self, max_lines: usize, max_columns: usize) -> String {
        let viewport = self.viewport();
        let max_lines = max_lines.max(1);
        let max_columns = max_columns.max(1);
        viewport
            .rows
            .iter()
            .take(max_lines)
            .map(|row| {
                let mut text = String::new();
                let mut width = 0;
                for cell in &row.cells {
                    if cell.width == CellWidth::WideContinuation {
                        continue;
                    }
                    if width >= max_columns {
                        break;
                    }
                    text.push_str(&cell.text);
                    width += 1;
                }
                text.trim_end().to_string()
            })
            .collect::<Vec<_>>()
            .join("\n")
    }

    fn scrollback_text(&self, max_lines: usize) -> Vec<String> {
        use alacritty_terminal::index::{Column, Line, Point};
        if self.is_alt_screen() || max_lines == 0 {
            return Vec::new();
        }
        let screen_lines = self.term.screen_lines() as i32;
        let history = self.term.history_size() as i32;
        // Oldest first, ending at the live bottom; the walk is capped so a
        // deep scrollback can never become an unbounded allocation.
        let oldest = (-history).max(screen_lines - max_lines as i32);
        (oldest..screen_lines)
            .map(|line| {
                let mut text = String::new();
                for col in 0..self.term.columns() {
                    let mapped = map_cell(
                        &self.term,
                        &self.term.grid()[Point::new(Line(line), Column(col))],
                    );
                    if mapped.width == CellWidth::WideContinuation {
                        continue;
                    }
                    if mapped.flags.contains(CellFlags::HIDDEN) {
                        continue;
                    }
                    text.push_str(&mapped.text);
                }
                text.trim_end().to_string()
            })
            .collect()
    }

    fn scroll(&mut self, command: ScrollCommand) {
        let scroll = match command {
            ScrollCommand::Lines(n) => Scroll::Delta(n),
            ScrollCommand::PageUp => Scroll::PageUp,
            ScrollCommand::PageDown => Scroll::PageDown,
            ScrollCommand::Top => Scroll::Top,
            ScrollCommand::Bottom => Scroll::Bottom,
        };
        self.term.scroll_display(scroll);
    }

    fn title(&self) -> Option<&str> {
        self.title.as_deref()
    }

    fn is_alt_screen(&self) -> bool {
        self.term.mode().contains(TermMode::ALT_SCREEN)
    }

    fn display_offset(&self) -> usize {
        self.term.grid().display_offset()
    }

    fn viewport_following_row(&self) -> Option<TerminalRow> {
        if self.is_alt_screen() || self.display_offset() == 0 {
            return None;
        }
        let line = self.term.screen_lines() as i32 - self.display_offset() as i32;
        let cells = (0..self.term.columns())
            .map(|col| {
                let point = alacritty_terminal::index::Point::new(
                    alacritty_terminal::index::Line(line),
                    alacritty_terminal::index::Column(col),
                );
                map_cell(&self.term, &self.term.grid()[point])
            })
            .collect();
        Some(TerminalRow { cells })
    }
}

// --- cell / color mapping -------------------------------------------------

fn map_cell(
    term: &Term<EngineListener>,
    cell: &alacritty_terminal::term::cell::Cell,
) -> TerminalCell {
    use alacritty_terminal::term::cell::Flags;

    let mut text = cell.c.to_string();
    if let Some(zero) = cell.zerowidth() {
        for ch in zero {
            text.push(*ch);
        }
    }
    let width = if cell.flags.contains(Flags::WIDE_CHAR) {
        CellWidth::Wide
    } else if cell.flags.contains(Flags::WIDE_CHAR_SPACER)
        || cell.flags.contains(Flags::LEADING_WIDE_CHAR_SPACER)
    {
        CellWidth::WideContinuation
    } else {
        CellWidth::Single
    };

    let mut flags = CellFlags::default();
    if cell.flags.contains(Flags::BOLD) {
        flags |= CellFlags::BOLD;
    }
    if cell.flags.contains(Flags::ITALIC) {
        flags |= CellFlags::ITALIC;
    }
    if cell.flags.intersects(
        Flags::UNDERLINE
            | Flags::DOUBLE_UNDERLINE
            | Flags::UNDERCURL
            | Flags::DOTTED_UNDERLINE
            | Flags::DASHED_UNDERLINE,
    ) {
        flags |= CellFlags::UNDERLINE;
    }
    if cell.flags.contains(Flags::INVERSE) {
        flags |= CellFlags::INVERSE;
    }
    if cell.flags.contains(Flags::DIM) || cell.flags.contains(Flags::DIM_BOLD) {
        flags |= CellFlags::DIM;
    }
    if cell.flags.contains(Flags::HIDDEN) {
        flags |= CellFlags::HIDDEN;
    }
    if cell.flags.contains(Flags::STRIKEOUT) {
        flags |= CellFlags::STRIKETHROUGH;
    }
    if cell.flags.contains(Flags::WRAPLINE) {
        flags |= CellFlags::WRAPPED;
    }

    TerminalCell {
        text,
        width,
        fg: resolve_fg(term, &cell.fg),
        bg: resolve_bg(term, &cell.bg),
        flags,
    }
}

fn resolve_fg(term: &Term<EngineListener>, color: &AlacColor) -> TermColor {
    match color {
        AlacColor::Named(named) => {
            if *named == NamedColor::Foreground {
                if term.colors()[*named].is_some() {
                    let rgb = term.colors()[*named].expect("checked above");
                    TermColor::Rgb(rgb.r, rgb.g, rgb.b)
                } else {
                    TermColor::DefaultFg
                }
            } else if *named == NamedColor::Background {
                // Foreground cell color pointing at background entry: resolve it.
                if let Some(rgb) = term.colors()[*named] {
                    TermColor::Rgb(rgb.r, rgb.g, rgb.b)
                } else {
                    TermColor::DefaultBg
                }
            } else if let Some(rgb) = term.colors()[*named] {
                TermColor::Rgb(rgb.r, rgb.g, rgb.b)
            } else {
                TermColor::Rgb(
                    standard_palette(*named as usize).0,
                    standard_palette(*named as usize).1,
                    standard_palette(*named as usize).2,
                )
            }
        }
        AlacColor::Indexed(index) => {
            if let Some(rgb) = term.colors()[*index as usize] {
                TermColor::Rgb(rgb.r, rgb.g, rgb.b)
            } else {
                let (r, g, b) = xterm_palette(*index);
                TermColor::Rgb(r, g, b)
            }
        }
        AlacColor::Spec(rgb) => TermColor::Rgb(rgb.r, rgb.g, rgb.b),
    }
}

fn resolve_bg(term: &Term<EngineListener>, color: &AlacColor) -> TermColor {
    match color {
        AlacColor::Named(named) => {
            if *named == NamedColor::Background {
                if term.colors()[*named].is_some() {
                    let rgb = term.colors()[*named].expect("checked above");
                    TermColor::Rgb(rgb.r, rgb.g, rgb.b)
                } else {
                    TermColor::DefaultBg
                }
            } else if let Some(rgb) = term.colors()[*named] {
                TermColor::Rgb(rgb.r, rgb.g, rgb.b)
            } else {
                TermColor::Rgb(
                    standard_palette(*named as usize).0,
                    standard_palette(*named as usize).1,
                    standard_palette(*named as usize).2,
                )
            }
        }
        AlacColor::Indexed(index) => {
            if let Some(rgb) = term.colors()[*index as usize] {
                TermColor::Rgb(rgb.r, rgb.g, rgb.b)
            } else {
                let (r, g, b) = xterm_palette(*index);
                TermColor::Rgb(r, g, b)
            }
        }
        AlacColor::Spec(rgb) => TermColor::Rgb(rgb.r, rgb.g, rgb.b),
    }
}

fn resolve_index_color(
    term: &Term<EngineListener>,
    index: usize,
) -> alacritty_terminal::vte::ansi::Rgb {
    use alacritty_terminal::vte::ansi::Rgb;
    // A color explicitly set by the application (OSC 10/11/12/4 set) always
    // wins, including the dynamic foreground/background/cursor slots.
    if index < alacritty_terminal::term::color::COUNT
        && let Some(rgb) = term.colors()[index]
    {
        return rgb;
    }
    if index < 16 {
        let (r, g, b) = standard_palette(index);
        Rgb { r, g, b }
    } else if index < 256 {
        let (r, g, b) = xterm_palette(index as u8);
        Rgb { r, g, b }
    } else {
        // Dynamic slots (Foreground=256, Background=257, Cursor=258, …).
        // Report the colors the renderer actually paints instead of a
        // hardcoded white: an application (e.g. opencode) derives its
        // light/dark theme from the OSC 11 reply, so a false white makes it
        // build a light theme on the dark pane.
        let (r, g, b) = match index {
            i if i == NamedColor::Foreground as usize => {
                crate::color::terminal_palette().foreground
            }
            i if i == NamedColor::Background as usize => {
                crate::color::terminal_palette().background
            }
            i if i == NamedColor::Cursor as usize => crate::color::terminal_palette().cursor,
            _ => crate::color::terminal_palette().foreground,
        };
        Rgb { r, g, b }
    }
}

/// Standard 16 ANSI colors (indices 0–15).
fn standard_palette(index: usize) -> (u8, u8, u8) {
    crate::color::terminal_palette().ansi[index.min(15)]
}

/// xterm 256-color palette for indices 16–255.
fn xterm_palette(index: u8) -> (u8, u8, u8) {
    let i = index as usize;
    if i < 16 {
        return standard_palette(i);
    }
    if i < 232 {
        let n = i - 16;
        let levels = [0u8, 95, 135, 175, 215, 255];
        return (levels[(n / 36) % 6], levels[(n / 6) % 6], levels[n % 6]);
    }
    let gray = 8 + (i as u8 - 232) * 10;
    (gray, gray, gray)
}

#[allow(dead_code)]
fn _assert_send() {
    fn is_send<T: Send>() {}
    is_send::<AlacrittyEngine>();
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn engine_starts_with_requested_dimensions() {
        let engine = AlacrittyEngine::new(80, 24);
        let viewport = engine.viewport();
        assert_eq!(viewport.cols, 80);
        assert_eq!(viewport.lines, 24);
        assert_eq!(viewport.rows.len(), 24);
    }

    #[test]
    fn engine_parses_simple_output() {
        let mut engine = AlacrittyEngine::new(80, 24);
        engine.advance_output(b"hello");
        let text = engine.read_visible_text(24, 80);
        assert!(text.contains("hello"), "viewport text was: {text:?}");
    }

    #[test]
    fn engine_tracks_title_and_bell() {
        let mut engine = AlacrittyEngine::new(80, 24);
        let out = engine.advance_output(b"\x1b]0;mytitle\x07");
        assert_eq!(engine.title(), Some("mytitle"));
        assert!(
            out.events
                .iter()
                .any(|e| matches!(e, TerminalEvent::TitleChanged(_)))
        );

        let out = engine.advance_output(b"\x07");
        assert!(out.events.iter().any(|e| matches!(e, TerminalEvent::Bell)));
    }

    #[test]
    fn engine_reports_device_replies() {
        let mut engine = AlacrittyEngine::new(80, 24);
        // Device Status Report request — engine must answer, not drop it.
        let out = engine.advance_output(b"\x1b[5n");
        assert!(!out.reply_bytes.is_empty(), "expected DSR reply bytes");
    }

    #[test]
    fn engine_handles_alt_screen() {
        let mut engine = AlacrittyEngine::new(80, 24);
        assert!(!engine.is_alt_screen());
        engine.advance_output(b"\x1b[?1049h");
        assert!(engine.is_alt_screen());
        engine.advance_output(b"\x1b[?1049l");
        assert!(!engine.is_alt_screen());
    }

    #[test]
    fn engine_restores_content_after_alt_screen() {
        let mut engine = AlacrittyEngine::new(80, 24);
        engine.advance_output(b"MAIN-CONTENT");
        engine.advance_output(b"\x1b[?1049h");
        assert!(engine.is_alt_screen());
        engine.advance_output(b"\x1b[H");
        engine.advance_output(b"ALT-CONTENT");
        let alt_text = engine.read_visible_text(24, 80);
        assert!(
            alt_text.contains("ALT-CONTENT"),
            "alt text was: {alt_text:?}"
        );
        engine.advance_output(b"\x1b[?1049l");
        assert!(!engine.is_alt_screen());
        let restored = engine.read_visible_text(24, 80);
        assert!(
            restored.contains("MAIN-CONTENT"),
            "main content should be restored, got: {restored:?}"
        );
        assert!(
            !restored.contains("ALT-CONTENT"),
            "alt content must not leak, got: {restored:?}"
        );
    }

    #[test]
    fn engine_tracks_bracketed_paste_mode() {
        let mut engine = AlacrittyEngine::new(80, 24);
        assert!(!engine.bracketed_paste());
        engine.advance_output(b"\x1b[?2004h");
        assert!(engine.bracketed_paste());
        engine.advance_output(b"\x1b[?2004l");
        assert!(!engine.bracketed_paste());
    }

    #[test]
    fn scrollback_history_is_bounded() {
        let mut engine = AlacrittyEngine::with_scrollback(20, 5, 100);
        for i in 0..500 {
            engine.advance_output(format!("line{i}\r\n").as_bytes());
        }
        let viewport = engine.viewport();
        assert!(
            viewport.history_size <= 100,
            "history must stay capped, got {}",
            viewport.history_size
        );
    }

    #[test]
    fn engine_maps_sgr_attributes() {
        let mut engine = AlacrittyEngine::new(80, 24);
        engine.advance_output(b"\x1b[1;3mX");
        let viewport = engine.viewport();
        let cell = viewport.rows[0].cells[0].clone();
        assert_eq!(cell.text, "X");
        assert!(
            cell.flags.contains(CellFlags::BOLD),
            "flags: {:?}",
            cell.flags
        );
        assert!(
            cell.flags.contains(CellFlags::ITALIC),
            "flags: {:?}",
            cell.flags
        );
    }

    #[test]
    fn engine_enforces_read_bounds() {
        let engine = AlacrittyEngine::new(80, 24);
        let text = engine.read_visible_text(2, 5);
        assert!(text.lines().count() <= 2);
    }

    #[test]
    fn scrollback_dump_is_oldest_first_and_bounded() {
        let mut engine = AlacrittyEngine::with_scrollback(20, 5, 100);
        for i in 0..20 {
            engine.advance_output(format!("line{i}\r\n").as_bytes());
        }
        let dump = engine.scrollback_text(1000);
        assert!(dump.len() <= 25, "len {}", dump.len());
        let joined = dump.join("\n");
        assert!(joined.contains("line0"), "{joined}");
        assert!(joined.contains("line19"), "{joined}");
        // Oldest first: line0 precedes line19.
        assert!(joined.find("line0") < joined.find("line19"));
        // The walk is capped, never the whole history.
        assert!(engine.scrollback_text(5).len() <= 5);
        assert!(engine.scrollback_text(0).is_empty());
    }

    #[test]
    fn engine_scrolls_to_top_and_bottom() {
        let mut engine = AlacrittyEngine::with_scrollback(20, 5, 100);
        for i in 0..20 {
            engine.advance_output(format!("line{i}\r\n").as_bytes());
        }
        engine.scroll(ScrollCommand::Top);
        assert!(engine.display_offset() > 0);
        engine.scroll(ScrollCommand::Bottom);
        assert_eq!(engine.display_offset(), 0);
    }

    #[test]
    fn overscan_row_matches_neighbor_without_mutating_grid_or_offset() {
        let mut engine = AlacrittyEngine::with_scrollback(20, 5, 100);
        for i in 0..20 {
            engine.advance_output(format!("line{i}\r\n").as_bytes());
        }
        assert!(engine.viewport_following_row().is_none());
        for offset in [1, 3, engine.viewport().history_size as i32] {
            engine.scroll(ScrollCommand::Bottom);
            engine.scroll(ScrollCommand::Lines(offset));
            let before = engine.viewport();
            let tail = engine.viewport_following_row().unwrap();
            assert_eq!(engine.viewport(), before);
            engine.scroll(ScrollCommand::Lines(-1));
            assert_eq!(engine.viewport().rows.last(), Some(&tail));
        }
        engine.advance_output(b"\x1b[?1049h");
        assert!(engine.viewport_following_row().is_none());
    }

    #[test]
    fn osc_dynamic_color_queries_report_painted_defaults() {
        let mut engine = AlacrittyEngine::new(80, 24);
        // OSC 10/11/12 queries must answer with the colors the renderer
        // actually paints, not a hardcoded white — opencode derives its
        // light/dark theme from the OSC 11 reply.
        let fg = engine.advance_output(b"\x1b]10;?\x07");
        assert!(
            String::from_utf8_lossy(&fg.reply_bytes).contains("rgb:e4e4/e4e4/e7e7"),
            "fg reply: {:?}",
            String::from_utf8_lossy(&fg.reply_bytes)
        );
        let bg = engine.advance_output(b"\x1b]11;?\x07");
        assert!(
            String::from_utf8_lossy(&bg.reply_bytes).contains("rgb:0f0f/1313/1818"),
            "bg reply: {:?}",
            String::from_utf8_lossy(&bg.reply_bytes)
        );
        let cursor = engine.advance_output(b"\x1b]12;?\x07");
        assert!(
            String::from_utf8_lossy(&cursor.reply_bytes).contains("rgb:9595/d7d7/ffff"),
            "cursor reply: {:?}",
            String::from_utf8_lossy(&cursor.reply_bytes)
        );
    }

    #[test]
    fn osc_dynamic_color_set_overrides_query_reply() {
        let mut engine = AlacrittyEngine::new(80, 24);
        // An application-set background must win over the built-in default.
        engine.advance_output(b"\x1b]11;#ffffff\x07");
        let bg = engine.advance_output(b"\x1b]11;?\x07");
        assert!(
            String::from_utf8_lossy(&bg.reply_bytes).contains("rgb:ffff/ffff/ffff"),
            "bg reply: {:?}",
            String::from_utf8_lossy(&bg.reply_bytes)
        );
    }

    #[test]
    fn mouse_mode_tracks_decset_and_sgr() {
        let mut engine = AlacrittyEngine::new(80, 24);
        assert!(!engine.mouse_mode().is_on());
        engine.advance_output(b"\x1b[?1000h");
        assert_eq!(engine.mouse_mode().kind, MouseModeKind::Click);
        assert!(!engine.mouse_mode().sgr);
        engine.advance_output(b"\x1b[?1002h");
        assert_eq!(engine.mouse_mode().kind, MouseModeKind::Drag);
        engine.advance_output(b"\x1b[?1003h");
        assert_eq!(engine.mouse_mode().kind, MouseModeKind::Motion);
        engine.advance_output(b"\x1b[?1006h");
        assert!(engine.mouse_mode().sgr);
        engine.advance_output(b"\x1b[?1003l");
        engine.advance_output(b"\x1b[?1002l");
        engine.advance_output(b"\x1b[?1000l");
        assert!(!engine.mouse_mode().is_on());
    }

    #[test]
    fn engine_handles_wide_chars() {
        let mut engine = AlacrittyEngine::new(20, 5);
        engine.advance_output("你".as_bytes());
        let viewport = engine.viewport();
        let first = &viewport.rows[0].cells[0];
        assert_eq!(first.width, CellWidth::Wide);
        assert_eq!(viewport.rows[0].cells[1].width, CellWidth::WideContinuation);
    }
}
