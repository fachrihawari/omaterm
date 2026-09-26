use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use gpui::{
    App, Application, AsyncApp, Bounds, Context, FocusHandle, Font, FontFallbacks, Hsla,
    KeyDownEvent, Pixels, ScrollDelta, ScrollWheelEvent, SharedString, TextRun, Timer, WeakEntity,
    Window, WindowBounds, WindowOptions, canvas, div, font, prelude::*, px, rgb, size,
};
use omaterm_terminal::{
    CellWidth, Key, KeyEvent, KeyModifiers, ScrollCommand, TermColor, TerminalSession,
    TerminalViewport, encode_key, poll_fd_readable, wrap_bracketed_paste,
};

/// M3 single-terminal view.
///
/// The `TerminalSession` (PTY + engine) is shared with a background reader
/// thread through a short-lived `Mutex`. The reader pumps PTY output and
/// sends immutable `TerminalViewport` snapshots over a bounded channel; the
/// main thread applies them event-driven (`recv().await`) and repaints.
/// Painting never holds the session lock.
struct TerminalView {
    focus_handle: FocusHandle,
    session: Arc<Mutex<TerminalSession>>,
    snapshot: TerminalViewport,
    rx: async_channel::Receiver<TerminalViewport>,
    font_size: f32,
    fonts: Option<ResolvedFonts>,
    exited: bool,
    /// Deadline until which the scroll thumb stays visible. Set on every
    /// wheel/keyboard scroll; cleared by a one-shot timer task. `None` (and
    /// any expired value) means the thumb is hidden, so an idle terminal
    /// paints no scrollbar and wakes no timers.
    scroll_indicator_until: Option<Instant>,
}

/// How long the scroll thumb lingers after the last scroll input.
const SCROLL_INDICATOR_FADE_MS: u64 = 800;

impl TerminalView {
    fn new(window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus_handle = cx.focus_handle();
        window.focus(&focus_handle);

        let working_directory = std::env::current_dir().unwrap_or_else(|_| std::env::temp_dir());
        let session = TerminalSession::new(working_directory, None, 80, 24)
            .expect("failed to spawn shell for M3 single terminal");
        let snapshot = session.viewport();
        let session = Arc::new(Mutex::new(session));
        // Bounded: snapshots are lossy by design; a full channel means a
        // newer snapshot is already queued, so the reader drops coalescible
        // frames instead of growing memory on output floods.
        let (tx, rx) = async_channel::bounded::<TerminalViewport>(8);

        Self::spawn_reader(Arc::clone(&session), tx);
        Self::spawn_poller(cx);

        Self {
            focus_handle,
            session,
            snapshot,
            rx,
            font_size: 14.0,
            fonts: None,
            exited: false,
            scroll_indicator_until: None,
        }
    }

    /// Background thread: drain PTY output, forward snapshots when changed.
    fn spawn_reader(
        session: Arc<Mutex<TerminalSession>>,
        tx: async_channel::Sender<TerminalViewport>,
    ) {
        std::thread::spawn(move || {
            let master_fd = match session.lock() {
                Ok(session) => session.pty_fd(),
                Err(_) => return,
            };
            let mut last: Option<TerminalViewport> = None;
            loop {
                // Sleep in the kernel until output arrives (or 200ms to
                // re-check child exit). An idle shell wakes this thread a
                // few times per second with no grid clones.
                let readable = poll_fd_readable(master_fd, 200).unwrap_or(true);
                let (snapshot, exited) = {
                    let mut session = match session.lock() {
                        Ok(guard) => guard,
                        Err(_) => break,
                    };
                    if readable {
                        match session.pump() {
                            Ok((out, bytes_read)) => {
                                let exited = session.exited().is_some();
                                let viewport = if bytes_read > 0 || exited {
                                    Some(session.viewport())
                                } else {
                                    None
                                };
                                let _ = out;
                                (viewport, exited)
                            }
                            Err(_) => {
                                let exited = session.poll_child();
                                let viewport = if exited {
                                    Some(session.viewport())
                                } else {
                                    None
                                };
                                (viewport, exited)
                            }
                        }
                    } else {
                        // Timeout: only notice child exit, no output drain.
                        let exited = session.poll_child();
                        let viewport = if exited {
                            Some(session.viewport())
                        } else {
                            None
                        };
                        (viewport, exited)
                    }
                };
                if let Some(viewport) = snapshot {
                    let changed = last.as_ref() != Some(&viewport);
                    if changed {
                        // Bounded channel: on flood, drop this frame; a newer
                        // snapshot follows. try_send never blocks the reader.
                        match tx.try_send(viewport.clone()) {
                            Ok(()) => last = Some(viewport),
                            Err(async_channel::TrySendError::Full(_)) => {}
                            Err(async_channel::TrySendError::Closed(_)) => break,
                        }
                    }
                }
                if exited {
                    break;
                }
            }
        });
    }

    /// Main-thread receiver: apply snapshots as they arrive. Event-driven
    /// (`recv().await` parks the task), so an idle terminal costs zero
    /// main-thread wakeups — no polling timer.
    fn spawn_poller(cx: &mut Context<Self>) {
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            loop {
                let viewport = {
                    let rx = match weak.update(cx, |view, _| view.rx.clone()) {
                        Ok(rx) => rx,
                        Err(_) => break, // view released
                    };
                    rx.recv().await
                };
                let Ok(viewport) = viewport else {
                    // Sender dropped (reader exited): final exit check.
                    let _ = weak.update(cx, |view, cx| {
                        if view
                            .session
                            .lock()
                            .map(|session| session.exited().is_some())
                            .unwrap_or(false)
                        {
                            view.exited = true;
                            cx.notify();
                        }
                    });
                    break;
                };
                let released = weak
                    .update(cx, |view, cx| {
                        view.snapshot = viewport;
                        if view
                            .session
                            .lock()
                            .map(|session| session.exited().is_some())
                            .unwrap_or(false)
                        {
                            view.exited = true;
                        }
                        cx.notify();
                    })
                    .is_err();
                if released {
                    break;
                }
            }
        })
        .detach();
    }

    /// Resolve (once per font size) and cache the terminal font set.
    fn fonts(&mut self, cx: &App) -> ResolvedFonts {
        let font_size = px(self.font_size);
        if self
            .fonts
            .as_ref()
            .is_none_or(|cached| cached.font_size != font_size)
        {
            self.fonts = Some(resolve_terminal_fonts(cx, font_size));
        }
        self.fonts.clone().expect("fonts just resolved")
    }

    fn resize_to_window(&mut self, window: &Window, fonts: &ResolvedFonts) {
        let viewport = window.viewport_size();
        let cols = ((viewport.width / fonts.cell_width).floor() as u16).clamp(2, 500);
        let rows = ((viewport.height / fonts.line_height).floor() as u16).clamp(1, 500);
        if let Ok(mut session) = self.session.lock() {
            let current = session.viewport();
            if current.cols != cols || current.lines != rows {
                session.resize(cols, rows);
            }
        }
    }

    fn on_key_down(&mut self, event: &KeyDownEvent, _: &mut Window, cx: &mut Context<Self>) {
        // libxkbcommon names (e.g. "Page_Up") are normalized for matching.
        let key_name = event.keystroke.key.to_lowercase().replace('_', "");

        // Clipboard paste: Ctrl+Shift+V (reads Wayland clipboard, honors
        // bracketed-paste mode). Selection copy arrives in Phase C.
        if event.keystroke.modifiers.control && event.keystroke.modifiers.shift && key_name == "v" {
            self.paste(cx);
            return;
        }

        // Scrollback when not in the alternate screen: Shift+PageUp/PageDown
        // scrolls history instead of reaching the application.
        if event.keystroke.modifiers.shift
            && (key_name == "pageup" || key_name == "pagedown")
            && !event.keystroke.modifiers.control
            && !event.keystroke.modifiers.alt
        {
            let in_alt_screen = self
                .session
                .lock()
                .map(|s| s.viewport().is_alt_screen)
                .unwrap_or(false);
            if !in_alt_screen {
                let command = if key_name == "pageup" {
                    ScrollCommand::PageUp
                } else {
                    ScrollCommand::PageDown
                };
                if let Ok(mut session) = self.session.lock() {
                    session.scroll(command);
                    self.snapshot = session.viewport();
                }
                self.flash_scroll_indicator(cx);
                return;
            }
        }

        let (app_cursor, app_keypad) = self
            .session
            .lock()
            .map(|s| (s.engine().app_cursor(), s.engine().app_keypad()))
            .unwrap_or((false, false));
        let Some(key_event) = translate_key(event, app_cursor, app_keypad) else {
            return;
        };
        let bytes = encode_key(&key_event);
        if bytes.is_empty() {
            return;
        }
        if let Ok(mut session) = self.session.lock() {
            let _ = session.write_input(&bytes);
        }
    }

    fn paste(&mut self, cx: &mut Context<Self>) {
        let text = cx
            .read_from_clipboard()
            .and_then(|item| item.text().map(|s| s.to_string()));
        let Some(text) = text else { return };
        let bytes = {
            let bracketed = self
                .session
                .lock()
                .map(|s| s.engine().bracketed_paste())
                .unwrap_or(false);
            if bracketed {
                wrap_bracketed_paste(&text)
            } else {
                text.into_bytes()
            }
        };
        if let Ok(mut session) = self.session.lock() {
            let _ = session.write_input(&bytes);
        }
    }

    /// Flash the scroll thumb for [`SCROLL_INDICATOR_FADE_MS`], then hide it.
    /// Each scroll input extends the deadline and spawns one short-lived
    /// task; stale tasks see an unexpired deadline and exit quietly. Nothing
    /// runs at idle.
    fn flash_scroll_indicator(&mut self, cx: &mut Context<Self>) {
        self.scroll_indicator_until =
            Some(Instant::now() + Duration::from_millis(SCROLL_INDICATOR_FADE_MS));
        cx.notify();
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            Timer::after(Duration::from_millis(SCROLL_INDICATOR_FADE_MS + 50)).await;
            let _ = weak.update(cx, |view, cx| {
                if view
                    .scroll_indicator_until
                    .is_some_and(|deadline| Instant::now() >= deadline)
                {
                    view.scroll_indicator_until = None;
                    cx.notify();
                }
            });
        })
        .detach();
    }

    fn on_scroll_wheel(
        &mut self,
        event: &ScrollWheelEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        // GPUI's Wayland backend already normalizes the axis sign (its
        // `vertical_modifier` is -1.0), so positive y means wheel-up, i.e.
        // toward history. Pass it through untouched: the compositor applies
        // the user's natural-scroll setting before we ever see the delta,
        // and negating here would fight that setting.
        let dy_lines: f32 = match event.delta {
            ScrollDelta::Pixels(point) => {
                let line_height: f32 = self.fonts(&*cx).line_height.into();
                if line_height <= 0.0 {
                    return;
                }
                f32::from(point.y) / line_height
            }
            ScrollDelta::Lines(point) => point.y,
        };
        // Positive steps scroll toward history, matching `Scroll::Delta`
        // semantics where the display offset grows upward.
        let mut steps = dy_lines.round() as i32;
        if steps == 0 && dy_lines != 0.0 {
            steps = dy_lines.signum() as i32;
        }
        if steps == 0 {
            return;
        }

        let Ok(session) = self.session.lock() else {
            return;
        };
        if session.viewport().is_alt_screen {
            // Alternate screen has no scrollback: emulate the common
            // alternate-scroll behavior by sending arrow keys.
            let app_cursor = session.engine().app_cursor();
            drop(session);
            let key = if steps > 0 { Key::Up } else { Key::Down };
            let repeats = steps.unsigned_abs().min(3) as usize;
            for _ in 0..repeats {
                let bytes = encode_key(&KeyEvent {
                    key: key.clone(),
                    modifiers: KeyModifiers::default(),
                    app_cursor,
                    app_keypad: false,
                });
                if let Ok(mut session) = self.session.lock() {
                    let _ = session.write_input(&bytes);
                }
            }
            return;
        }
        drop(session);
        if let Ok(mut session) = self.session.lock() {
            session.scroll(ScrollCommand::Lines(steps));
            self.snapshot = session.viewport();
        }
        self.flash_scroll_indicator(cx);
    }
}

/// Translate a GPUI key event into the GPUI-free [`KeyEvent`].
fn translate_key(event: &KeyDownEvent, app_cursor: bool, app_keypad: bool) -> Option<KeyEvent> {
    let modifiers = &event.keystroke.modifiers;
    // Super is reserved for desktop chrome; encode_key swallows it.
    let mods = KeyModifiers {
        ctrl: modifiers.control,
        alt: modifiers.alt,
        shift: modifiers.shift,
        super_key: modifiers.platform,
    };
    let key = match event.keystroke.key.to_lowercase().replace('_', "").as_str() {
        "enter" => Key::Enter,
        "tab" => Key::Tab,
        "backspace" => Key::Backspace,
        "escape" => Key::Escape,
        "left" => Key::Left,
        "right" => Key::Right,
        "up" => Key::Up,
        "down" => Key::Down,
        "home" => Key::Home,
        "end" => Key::End,
        "pageup" => Key::PageUp,
        "pagedown" => Key::PageDown,
        "insert" => Key::Insert,
        "delete" => Key::Delete,
        name if name.starts_with('f') => Key::F(name[1..].parse().ok()?),
        _ => {
            let ch = event
                .keystroke
                .key_char
                .as_ref()
                .and_then(|s| s.chars().next())
                .or_else(|| event.keystroke.key.chars().next())?;
            Key::Char(ch)
        }
    };
    Some(KeyEvent {
        key,
        modifiers: mods,
        app_cursor,
        app_keypad,
    })
}

/// Preferred monospace families, in order. The runtime picks the first one
/// present on the system so prompt icons (Nerd Font glyphs) and CJK/emoji
/// fall back sanely; `"monospace"` is the portable last resort that follows
/// the user's fontconfig configuration.
const MONO_PREFERENCES: &[&str] = &[
    "JetBrainsMono Nerd Font",
    "JetBrainsMono NF",
    "DejaVu Sans Mono",
    "Liberation Mono",
    "Noto Sans Mono",
    "monospace",
];

/// Glyph fallback chain for symbols the primary font lacks (prompt icons,
/// emoji, CJK).
fn symbol_fallbacks() -> FontFallbacks {
    FontFallbacks::from_fonts(
        [
            "JetBrainsMono Nerd Font",
            "JetBrainsMono NF",
            "Noto Color Emoji",
            "DejaVu Sans Mono",
        ]
        .iter()
        .map(ToString::to_string)
        .collect(),
    )
}

/// Pick the first preferred family installed on this machine.
fn pick_mono_family(cx: &App) -> String {
    let installed = cx.text_system().all_font_names();
    for preferred in MONO_PREFERENCES {
        if installed.iter().any(|name| name == preferred) {
            return (*preferred).to_string();
        }
    }
    "monospace".to_string()
}

/// Terminal font set: base + bold/italic variants, ligatures disabled
/// (ligatures merge columns and break the grid), symbol fallbacks attached.
fn terminal_fonts(cx: &App) -> [Font; 4] {
    let family = pick_mono_family(cx);
    let fallbacks = symbol_fallbacks();
    let base = Font {
        family: family.clone().into(),
        features: gpui::FontFeatures::disable_ligatures(),
        fallbacks: Some(fallbacks.clone()),
        ..font(&family)
    };
    let bold = Font {
        family: family.clone().into(),
        features: gpui::FontFeatures::disable_ligatures(),
        fallbacks: Some(fallbacks.clone()),
        ..font(&family).bold()
    };
    let italic = Font {
        family: family.clone().into(),
        features: gpui::FontFeatures::disable_ligatures(),
        fallbacks: Some(fallbacks.clone()),
        ..font(&family).italic()
    };
    let bold_italic = Font {
        family: family.clone().into(),
        features: gpui::FontFeatures::disable_ligatures(),
        fallbacks: Some(fallbacks),
        ..font(&family).bold().italic()
    };
    [base, bold, italic, bold_italic]
}

fn style_index(bold: bool, italic: bool) -> usize {
    match (bold, italic) {
        (false, false) => 0,
        (true, false) => 1,
        (false, true) => 2,
        (true, true) => 3,
    }
}

/// Resolved font IDs plus grid metrics, cached per font size. Metrics come
/// from the font itself (ascent + |descent| — GPUI reports descent negative
/// on Linux), never from window UI metrics, so rows sit exactly on the grid
/// the PTY was sized for.
#[derive(Debug, Clone)]
struct ResolvedFonts {
    fonts: [Font; 4],
    cell_width: Pixels,
    line_height: Pixels,
    font_size: Pixels,
}

fn resolve_terminal_fonts(cx: &App, font_size: Pixels) -> ResolvedFonts {
    let fonts = terminal_fonts(cx);
    let base_id = cx.text_system().resolve_font(&fonts[0]);
    // Resolve the style variants eagerly so missing bold/italic faces surface
    // at startup instead of mid-frame.
    for variant in &fonts[1..] {
        cx.text_system().resolve_font(variant);
    }
    // Cell width is the `M` advance of the base face. A true monospace face
    // advances every glyph identically, which is what keeps shaped runs
    // column-aligned; probe a spread of glyphs and warn on mismatch.
    let cell_width = cx
        .text_system()
        .advance(base_id, font_size, 'M')
        .map(|size| size.width)
        .unwrap_or(px(8.4));
    #[cfg(debug_assertions)]
    {
        let probes = ['i', 'W', ' ', '0', '-', 'M'];
        for probe in probes {
            if let Ok(advance) = cx.text_system().advance(base_id, font_size, probe) {
                let a: f32 = cell_width.into();
                let b: f32 = advance.width.into();
                if (a - b).abs() > 0.5 {
                    tracing::warn!("terminal font is not monospace: 'M'={a}px vs {probe:?}={b}px");
                    break;
                }
            }
        }
    }
    let ascent = cx.text_system().ascent(base_id, font_size);
    let descent = cx.text_system().descent(base_id, font_size);
    // NOTE: GPUI's Linux backend reports descent as a NEGATIVE below-baseline
    // offset (`descent: -metrics.descent` in platform/linux/text_system.rs),
    // so the content height is ascent + |descent|, not ascent + descent.
    let line_height = {
        let a: f32 = ascent.into();
        let d: f32 = descent.into();
        px(a + d.abs())
    };
    tracing::info!(
        family = %cx.text_system().get_font_for_id(base_id).map(|font| font.family.to_string()).unwrap_or_else(|| "<unknown>".to_string()),
        "terminal font resolved",
    );
    ResolvedFonts {
        fonts,
        cell_width,
        line_height,
        font_size,
    }
}

fn fg_color(cell_fg: TermColor, inverse: bool) -> Hsla {
    if inverse {
        return match cell_fg {
            TermColor::Rgb(r, g, b) => rgb(rgb_hex(r, g, b)).into(),
            TermColor::DefaultFg => rgb(0x18181B).into(),
            TermColor::DefaultBg => rgb(0xE4E4E7).into(),
        };
    }
    match cell_fg {
        TermColor::Rgb(r, g, b) => rgb(rgb_hex(r, g, b)).into(),
        TermColor::DefaultFg => rgb(0xE4E4E7).into(),
        TermColor::DefaultBg => rgb(0x18181B).into(),
    }
}

/// Background paint color. `None` means transparent (root background shows).
fn bg_paint(cell_bg: TermColor, inverse: bool) -> Option<Hsla> {
    if inverse {
        return Some(match cell_bg {
            TermColor::Rgb(r, g, b) => rgb(rgb_hex(r, g, b)).into(),
            TermColor::DefaultFg => rgb(0xE4E4E7).into(),
            TermColor::DefaultBg => rgb(0x18181B).into(),
        });
    }
    match cell_bg {
        TermColor::Rgb(r, g, b) => Some(rgb(rgb_hex(r, g, b)).into()),
        TermColor::DefaultFg => Some(rgb(0xE4E4E7).into()),
        TermColor::DefaultBg => None,
    }
}

fn rgb_hex(r: u8, g: u8, b: u8) -> u32 {
    (u32::from(r) << 16) | (u32::from(g) << 8) | u32::from(b)
}

impl Render for TerminalView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let fonts = self.fonts(cx);
        self.resize_to_window(window, &fonts);

        if self.exited {
            return div()
                .track_focus(&self.focus_handle)
                .size_full()
                .flex()
                .items_center()
                .justify_center()
                .bg(rgb(0x18181B))
                .text_color(rgb(0xA1A1AA))
                .child("Shell exited.")
                .into_any_element();
        }

        let snapshot = self.snapshot.clone();
        let cursor_color: Hsla = rgb(0xE4E4E7).into();
        // Thumb shows only briefly after scroll input, never persistently.
        let show_scrollbar = self
            .scroll_indicator_until
            .is_some_and(|deadline| Instant::now() < deadline);

        div()
            .track_focus(&self.focus_handle)
            .on_key_down(cx.listener(Self::on_key_down))
            .on_scroll_wheel(cx.listener(Self::on_scroll_wheel))
            .size_full()
            .bg(rgb(0x18181B))
            .child(canvas(
                move |bounds, _, _| bounds,
                move |bounds: Bounds<Pixels>,
                      bounds_prepaint: Bounds<Pixels>,
                      window: &mut Window,
                      cx: &mut App| {
                    paint_terminal(
                        bounds,
                        bounds_prepaint,
                        &PaintArgs {
                            snapshot: &snapshot,
                            fonts: &fonts,
                            cursor_color,
                            show_scrollbar,
                        },
                        window,
                        cx,
                    );
                },
            ))
            .into_any_element()
    }
}

/// Paint parameters bundled so `paint_terminal` stays under clippy's
/// `too_many_arguments` threshold.
struct PaintArgs<'a> {
    snapshot: &'a TerminalViewport,
    fonts: &'a ResolvedFonts,
    cursor_color: Hsla,
    show_scrollbar: bool,
}

fn paint_terminal(
    _bounds: Bounds<Pixels>,
    origin_bounds: Bounds<Pixels>,
    args: &PaintArgs,
    window: &mut Window,
    cx: &mut App,
) {
    let snapshot = args.snapshot;
    let fonts = args.fonts;
    let cursor_color = args.cursor_color;
    let show_scrollbar = args.show_scrollbar;
    let origin = origin_bounds.origin;
    let cell_width = fonts.cell_width;
    let line_height = fonts.line_height;
    let font_size = fonts.font_size;

    // Reverse-video block cursor position. Computed once so the row pass can
    // skip the cursor cell (it is painted below in inverted colors).
    let cursor_cell = if snapshot.cursor.visible
        && snapshot.display_offset == 0
        && snapshot.cursor.shape == omaterm_terminal::CursorShape::Block
    {
        Some((snapshot.cursor.row as usize, snapshot.cursor.col as usize))
    } else {
        None
    };

    for (row_idx, row) in snapshot.rows.iter().enumerate() {
        let y = origin.y + line_height * (row_idx as f32);

        // Merged background runs (skip default/transparent cells).
        let mut run_start: Option<(usize, Hsla)> = None;
        let flush_bg = |start: usize, end: usize, color: Hsla, window: &mut Window| {
            let x = origin.x + cell_width * (start as f32);
            let w = cell_width * ((end - start) as f32);
            window.paint_quad(gpui::fill(
                Bounds {
                    origin: gpui::Point { x, y },
                    size: gpui::Size {
                        width: w,
                        height: line_height,
                    },
                },
                color,
            ));
        };
        for (col_idx, cell) in row.cells.iter().enumerate() {
            let inverse = cell.flags.contains(omaterm_terminal::CellFlags::INVERSE);
            let bg = bg_paint(cell.bg, inverse);
            match (run_start, bg) {
                (Some((start, color)), Some(next)) if color == next => {
                    run_start = Some((start, color));
                }
                (Some((start, color)), _) => {
                    flush_bg(start, col_idx, color, window);
                    run_start = bg.map(|color| (col_idx, color));
                }
                (None, Some(color)) => run_start = Some((col_idx, color)),
                (None, None) => {}
            }
        }
        if let Some((start, color)) = run_start {
            flush_bg(start, row.cells.len(), color, window);
        }

        // Text: group consecutive cells with identical styling into runs.
        // Wide-continuation cells are skipped (the lead cell draws the glyph).
        // Runs are painted at the row origin; with a verified monospace face
        // every glyph advances exactly one cell, so columns stay grid-aligned
        // and the cursor (placed at col x cell_width) lands on its glyph.
        let mut text = String::new();
        let mut runs: Vec<TextRun> = Vec::new();
        let mut run_start_len = 0usize;
        let mut current_style: Option<(Hsla, usize, bool, bool)> = None;
        let flush_run = |text: &str,
                         start: usize,
                         style: &Option<(Hsla, usize, bool, bool)>,
                         runs: &mut Vec<TextRun>,
                         fonts: &ResolvedFonts| {
            let Some((color, style_idx, underline, strike)) = style else {
                return;
            };
            let len = text.len() - start;
            if len == 0 {
                return;
            }
            runs.push(TextRun {
                len,
                font: fonts.fonts[*style_idx].clone(),
                color: *color,
                background_color: None,
                underline: underline.then(|| gpui::UnderlineStyle {
                    thickness: px(1.0),
                    color: Some(*color),
                    wavy: false,
                }),
                strikethrough: strike.then(|| gpui::StrikethroughStyle {
                    thickness: px(1.0),
                    color: Some(*color),
                }),
            });
        };

        for (col_idx, cell) in row.cells.iter().enumerate() {
            if cell.width == CellWidth::WideContinuation {
                continue;
            }
            // The reverse-video block cursor cell is painted below.
            if cursor_cell == Some((row_idx, col_idx)) {
                text.push(' ');
                continue;
            }
            let inverse = cell.flags.contains(omaterm_terminal::CellFlags::INVERSE);
            let hidden = cell.flags.contains(omaterm_terminal::CellFlags::HIDDEN);
            if hidden {
                continue;
            }
            let color = fg_color(cell.fg, inverse);
            let bold = cell.flags.contains(omaterm_terminal::CellFlags::BOLD);
            let italic = cell.flags.contains(omaterm_terminal::CellFlags::ITALIC);
            let style = style_index(bold, italic);
            let underline = cell.flags.contains(omaterm_terminal::CellFlags::UNDERLINE);
            let strike = cell
                .flags
                .contains(omaterm_terminal::CellFlags::STRIKETHROUGH);
            let style_key = (color, style, underline, strike);
            match &current_style {
                Some(current) if *current == style_key => {}
                _ => {
                    flush_run(&text, run_start_len, &current_style, &mut runs, fonts);
                    run_start_len = text.len();
                    current_style = Some(style_key);
                }
            }
            text.push_str(&cell.text);
        }
        flush_run(&text, run_start_len, &current_style, &mut runs, fonts);

        if !text.trim().is_empty() {
            let shaped =
                window
                    .text_system()
                    .shape_line(SharedString::from(text), font_size, &runs, None);
            let _ = shaped.paint(
                origin
                    + gpui::Point {
                        x: px(0.0),
                        y: line_height * (row_idx as f32),
                    },
                line_height,
                window,
                cx,
            );
            let _ = y;
        }
    }

    // Cursor. Hidden while viewing scrollback history, matching conventional
    // terminal behavior (the cursor lives at the live edge). A block cursor
    // is reverse video: cell background in the cursor color with the cell
    // glyph drawn in the cell's own background color.
    if snapshot.cursor.visible && snapshot.display_offset == 0 {
        let x = origin.x + cell_width * f32::from(snapshot.cursor.col);
        let y = origin.y + line_height * f32::from(snapshot.cursor.row);
        match snapshot.cursor.shape {
            omaterm_terminal::CursorShape::Block => {
                let row = snapshot.cursor.row as usize;
                let col = snapshot.cursor.col as usize;
                let (cell_text, cell_bg) = snapshot
                    .rows
                    .get(row)
                    .and_then(|row| row.cells.get(col))
                    .map(|cell| {
                        let inverse = cell.flags.contains(omaterm_terminal::CellFlags::INVERSE);
                        (cell.text.clone(), bg_paint(cell.bg, inverse))
                    })
                    .unwrap_or_default();
                window.paint_quad(gpui::fill(
                    Bounds {
                        origin: gpui::Point { x, y },
                        size: gpui::Size {
                            width: cell_width,
                            height: line_height,
                        },
                    },
                    cursor_color,
                ));
                // Glyph in the cell's background color for contrast.
                // NOTE: TextRun::len counts UTF-8 bytes.
                let glyph_color: Hsla = cell_bg.unwrap_or(rgb(0x18181B).into());
                if !cell_text.trim().is_empty() {
                    let run_len = cell_text.len();
                    let shaped = window.text_system().shape_line(
                        SharedString::from(cell_text),
                        font_size,
                        &[TextRun {
                            len: run_len,
                            font: fonts.fonts[0].clone(),
                            color: glyph_color,
                            background_color: None,
                            underline: None,
                            strikethrough: None,
                        }],
                        None,
                    );
                    let _ = shaped.paint(gpui::Point { x, y }, line_height, window, cx);
                }
            }
            omaterm_terminal::CursorShape::Underline => {
                window.paint_quad(gpui::fill(
                    Bounds {
                        origin: gpui::Point {
                            x,
                            y: y + line_height - px(2.0),
                        },
                        size: gpui::Size {
                            width: cell_width,
                            height: px(2.0),
                        },
                    },
                    cursor_color,
                ));
            }
            omaterm_terminal::CursorShape::Bar => {
                window.paint_quad(gpui::fill(
                    Bounds {
                        origin: gpui::Point { x, y },
                        size: gpui::Size {
                            width: px(2.0),
                            height: line_height,
                        },
                    },
                    cursor_color,
                ));
            }
            omaterm_terminal::CursorShape::Hidden => {}
        }
    }

    // Scroll position indicator: a slim thumb on the right edge showing
    // where the viewport sits within total (history + visible) content.
    // Shown only briefly after scroll input, and only when scrollback
    // history exists. `display_offset == 0` is the live bottom edge, so the
    // thumb rests at the track bottom there and rises toward the top as the
    // viewport moves into older history.
    let history = snapshot.history_size;
    if show_scrollbar && history > 0 {
        let track_x = origin.x + origin_bounds.size.width - px(8.0);
        let track_h: f32 = (snapshot.lines as f32) * f32::from(line_height);
        let total = (history + snapshot.lines as usize) as f32;
        let thumb_h = (track_h * snapshot.lines as f32 / total).max(12.0);
        let travel = (track_h - thumb_h).max(0.0);
        let frac = (snapshot.display_offset as f32 / history as f32).clamp(0.0, 1.0);
        let thumb_y = origin.y + px((1.0 - frac) * travel);
        let thumb_color: Hsla = rgb(0x52525B).into();
        window.paint_quad(gpui::fill(
            Bounds {
                origin: gpui::Point {
                    x: track_x,
                    y: thumb_y,
                },
                size: gpui::Size {
                    width: px(4.0),
                    height: px(thumb_h),
                },
            },
            thumb_color,
        ));
    }
}

fn main() {
    Application::new().run(|cx: &mut App| {
        cx.on_window_closed(|cx| {
            if cx.windows().is_empty() {
                cx.quit();
            }
        })
        .detach();

        let bounds = Bounds::centered(None, size(px(960.0), px(640.0)), cx);
        cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                ..Default::default()
            },
            |window, cx| {
                window.set_window_title("OmaTerm");
                cx.new(|cx| TerminalView::new(window, cx))
            },
        )
        .expect("failed to open OmaTerm window");

        cx.activate(true);
    });
}
