use std::cell::Cell;
use std::collections::HashMap;
use std::rc::Rc;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use gpui::{
    App, Application, AsyncApp, Bounds, ClipboardItem, Context, FocusHandle, Font, FontFallbacks,
    Hsla, KeyDownEvent, MouseButton, MouseDownEvent, MouseMoveEvent, MouseUpEvent, Pixels,
    ScrollDelta, ScrollWheelEvent, SharedString, TextRun, Timer, WeakEntity, Window, WindowBounds,
    WindowOptions, canvas, div, font, prelude::*, px, relative, rgb, rgba, size,
};
use omaterm_core::{Pane, PaneContent, PaneId, PaneNode, SessionId, SplitAxis, SplitDirection};
use omaterm_terminal::{
    CellPoint, CellWidth, Key, KeyEvent, KeyModifiers, ScrollCommand, SelectionRange, TermColor,
    TerminalSession, TerminalViewport, WorkspaceCoordinator, encode_key, extract_text,
    poll_fd_readable, prepare_paste,
};

/// M4 workspace: a recursive pane tree whose leaves reference
/// registry-owned `TerminalSession`s by `SessionId`.
///
/// Sessions are durable: focus changes, resizes, and sibling split/close
/// never recreate them. Each session has a background reader thread pumping
/// PTY output into engine state and forwarding immutable snapshots over a
/// bounded channel; the main thread applies snapshots event-driven and
/// repaints. Painting never holds a session lock.
struct WorkspaceView {
    focus_handle: FocusHandle,
    coordinator: WorkspaceCoordinator,
    snapshots: HashMap<SessionId, TerminalViewport>,
    receivers: HashMap<SessionId, async_channel::Receiver<TerminalViewport>>,
    selections: HashMap<SessionId, SelectionRange>,
    selecting: Option<PaneId>,
    scroll_indicator_until: HashMap<SessionId, Instant>,
    grid_origins: HashMap<PaneId, Rc<Cell<gpui::Point<Pixels>>>>,
    /// Last grid applied to each session. Compared in `render` so the PTY
    /// follows pane geometry without locking sessions on every frame.
    grid_sizes: HashMap<SessionId, (u16, u16)>,
    fonts: Option<ResolvedFonts>,
    font_size: f32,
}

/// How long the scroll thumb lingers after the last scroll input.
const SCROLL_INDICATOR_FADE_MS: u64 = 800;

impl WorkspaceView {
    fn new(window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus_handle = cx.focus_handle();
        window.focus(&focus_handle);
        let working_directory = std::env::current_dir().unwrap_or_else(|_| std::env::temp_dir());
        let mut view = Self {
            focus_handle,
            coordinator: WorkspaceCoordinator::new(working_directory),
            snapshots: HashMap::new(),
            receivers: HashMap::new(),
            selections: HashMap::new(),
            selecting: None,
            scroll_indicator_until: HashMap::new(),
            grid_origins: HashMap::new(),
            grid_sizes: HashMap::new(),
            fonts: None,
            font_size: 14.0,
        };
        // Initial terminal via the shared coordinator. If spawn fails (no
        // PTY), keep the empty workspace + new-terminal action.
        match view.coordinator.create_initial(80, 24) {
            Ok(session_id) => {
                view.start_runtime(cx, session_id);
            }
            Err(e) => {
                tracing::error!("failed to spawn initial shell: {e}");
            }
        }
        view
    }

    /// Publish a coordinator-owned session's initial snapshot and start its
    /// reader + poller. The session is already registered, so lookup never
    /// observes a half-created terminal.
    fn start_runtime(&mut self, cx: &mut Context<Self>, id: SessionId) {
        let snapshot = self
            .coordinator
            .registry()
            .get(id)
            .and_then(|s| s.lock().ok().map(|s| s.viewport()))
            .expect("coordinator-owned session must be registered");
        self.grid_sizes.insert(id, (snapshot.cols, snapshot.lines));
        self.snapshots.insert(id, snapshot);
        let (tx, rx) = async_channel::bounded::<TerminalViewport>(8);
        self.receivers.insert(id, rx.clone());
        let session = self
            .coordinator
            .registry()
            .get(id)
            .expect("just registered");
        Self::spawn_reader(session, tx);
        Self::spawn_poller(cx, id, rx);
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
                        if tx.is_closed() {
                            return;
                        }
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

    /// Main-thread receiver for one session. Event-driven (`recv().await`
    /// parks the task), so idle terminals cost zero main-thread wakeups.
    /// Exits when the session is detached/closed or the view is released.
    fn spawn_poller(
        cx: &mut Context<Self>,
        session_id: SessionId,
        rx: async_channel::Receiver<TerminalViewport>,
    ) {
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            loop {
                let viewport = rx.recv().await;
                let Ok(viewport) = viewport else {
                    let _ = weak.update(cx, |view, cx| {
                        let exited = view
                            .coordinator
                            .registry()
                            .get(session_id)
                            .map(|s| s.lock().map(|s| s.exited().is_some()).unwrap_or(false))
                            .unwrap_or(false);
                        if exited {
                            view.close_session(session_id, cx);
                            cx.notify();
                        }
                    });
                    break;
                };
                let done = weak
                    .update(cx, |view, cx| {
                        if !view.coordinator.registry().contains(session_id) {
                            return true;
                        }
                        view.snapshots.insert(session_id, viewport);
                        if view
                            .coordinator
                            .registry()
                            .get(session_id)
                            .map(|s| s.lock().map(|s| s.exited().is_some()).unwrap_or(false))
                            .unwrap_or(false)
                        {
                            // A shell exit is a pane-close event regardless
                            // of which pane currently has focus. The session
                            // ID is unique, so a delayed reader cannot close
                            // a newly created/reused pane.
                            view.close_session(session_id, cx);
                            return true;
                        }
                        cx.notify();
                        false
                    })
                    .unwrap_or(true);
                if done {
                    break;
                }
            }
        })
        .detach();
    }

    fn focused_session_id(&self) -> Option<SessionId> {
        self.coordinator.focused_session_id()
    }

    fn session_id_for_pane(&self, pane: PaneId) -> Option<SessionId> {
        self.coordinator.session_id_for_pane(pane)
    }

    /// Drop per-session UI state after its pane is gone. The PTY/session
    /// handle itself is shut down by the caller off the UI thread.
    fn forget_session_state(&mut self, session_id: SessionId, pane: PaneId) {
        self.snapshots.remove(&session_id);
        self.grid_sizes.remove(&session_id);
        if let Some(rx) = self.receivers.remove(&session_id) {
            rx.close();
        }
        self.selections.remove(&session_id);
        self.scroll_indicator_until.remove(&session_id);
        self.grid_origins.remove(&pane);
        if self.selecting == Some(pane) {
            self.selecting = None;
        }
    }

    fn shutdown_session_bg(handle: Arc<Mutex<TerminalSession>>) {
        std::thread::spawn(move || {
            let reaped = handle
                .lock()
                .map(|mut session| session.shutdown())
                .unwrap_or(false);
            if !reaped {
                tracing::warn!("terminal child did not reap within the bounded shutdown");
            }
        });
    }

    fn split_focused(&mut self, direction: SplitDirection, cx: &mut Context<Self>) {
        if self.coordinator.focused().is_none() {
            // Empty workspace: a split key creates the first terminal.
            self.new_terminal_for_empty(cx);
            return;
        }
        match self.coordinator.split_focused(direction, 80, 24) {
            Ok((_pane_id, session_id)) => {
                self.start_runtime(cx, session_id);
                cx.notify();
            }
            Err(e) => {
                tracing::error!("split aborted: {e}");
            }
        }
    }

    fn close_focused(&mut self, cx: &mut Context<Self>) {
        if let Ok(closed) = self.coordinator.close_focused() {
            self.finish_close(closed);
            cx.notify();
        }
    }

    fn close_session(&mut self, session_id: SessionId, cx: &mut Context<Self>) {
        if let Ok(closed) = self.coordinator.close_session(session_id) {
            self.finish_close(closed);
            cx.notify();
        }
    }

    fn finish_close(&mut self, closed: omaterm_terminal::ClosedPane) {
        if let Some(session_id) = closed.session_id {
            self.forget_session_state(session_id, closed.pane_id);
        }
        if let Some(handle) = closed.handle {
            // For a naturally exited shell, shutdown() observes the recorded
            // exit immediately and does not send a second signal.
            Self::shutdown_session_bg(handle);
        }
    }

    fn focus_neighbor(&mut self, direction: SplitDirection, cx: &mut Context<Self>) {
        if self.coordinator.focus_neighbor(direction).is_some() {
            cx.notify();
        }
    }

    fn resize_focused(&mut self, amount: f32, cx: &mut Context<Self>) {
        if self.coordinator.resize_focused(amount).is_ok() {
            cx.notify();
        }
    }

    fn equalize(&mut self, cx: &mut Context<Self>) {
        self.coordinator.equalize();
        cx.notify();
    }

    fn new_terminal_for_empty(&mut self, cx: &mut Context<Self>) {
        if !self.coordinator.is_empty() {
            return;
        }
        match self.coordinator.create_initial(80, 24) {
            Ok(session_id) => {
                self.start_runtime(cx, session_id);
                cx.notify();
            }
            Err(e) => {
                tracing::error!("failed to spawn shell: {e}");
            }
        }
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

    fn on_key_down(&mut self, event: &KeyDownEvent, _: &mut Window, cx: &mut Context<Self>) {
        let key_name = event.keystroke.key.to_lowercase().replace('_', "");

        // Clipboard paste: Ctrl+Shift+V.
        if event.keystroke.modifiers.control && event.keystroke.modifiers.shift && key_name == "v" {
            self.paste(cx);
            return;
        }

        // Explicit clipboard copy of the drag selection: Ctrl+Shift+C.
        if event.keystroke.modifiers.control && event.keystroke.modifiers.shift && key_name == "c" {
            self.copy_selection(cx);
            return;
        }

        // Workspace commands (M2 bindings, preserved for M4). These take
        // precedence over terminal input so layout never depends on the
        // foreground program.
        if event.keystroke.modifiers.control {
            let key = event.keystroke.key.as_str();
            if key == "{" || key == "[" {
                self.resize_focused(-0.05, cx);
                return;
            }
            if key == "}" || key == "]" {
                self.resize_focused(0.05, cx);
                return;
            }
        }
        if event.keystroke.modifiers.control
            && event.keystroke.modifiers.shift
            && !event.keystroke.modifiers.alt
        {
            match key_name.as_str() {
                "r" => {
                    self.split_focused(SplitDirection::Right, cx);
                    return;
                }
                "d" => {
                    self.split_focused(SplitDirection::Down, cx);
                    return;
                }
                "w" => {
                    self.close_focused(cx);
                    return;
                }
                "h" => {
                    self.focus_neighbor(SplitDirection::Left, cx);
                    return;
                }
                "j" => {
                    self.focus_neighbor(SplitDirection::Down, cx);
                    return;
                }
                "k" => {
                    self.focus_neighbor(SplitDirection::Up, cx);
                    return;
                }
                "l" => {
                    self.focus_neighbor(SplitDirection::Right, cx);
                    return;
                }
                "e" => {
                    self.equalize(cx);
                    return;
                }
                "t" => {
                    self.new_terminal_for_empty(cx);
                    return;
                }
                _ => {}
            }
        }

        let Some(session_id) = self.focused_session_id() else {
            // Empty workspace: Enter/T also offers a fresh terminal.
            if key_name == "enter" {
                self.new_terminal_for_empty(cx);
            }
            return;
        };
        let Some(handle) = self.coordinator.registry().get(session_id) else {
            return;
        };

        // Scrollback when not in the alternate screen: Shift+PageUp/PageDown.
        if event.keystroke.modifiers.shift
            && (key_name == "pageup" || key_name == "pagedown")
            && !event.keystroke.modifiers.control
            && !event.keystroke.modifiers.alt
        {
            let in_alt_screen = handle
                .lock()
                .map(|s| s.viewport().is_alt_screen)
                .unwrap_or(false);
            if !in_alt_screen {
                let command = if key_name == "pageup" {
                    ScrollCommand::PageUp
                } else {
                    ScrollCommand::PageDown
                };
                if let Ok(mut session) = handle.lock() {
                    session.scroll(command);
                    self.snapshots.insert(session_id, session.viewport());
                }
                self.flash_scroll_indicator(session_id, cx);
                return;
            }
        }

        let (app_cursor, app_keypad) = handle
            .lock()
            .map(|s| (s.app_cursor(), s.app_keypad()))
            .unwrap_or((false, false));
        let Some(key_event) = translate_key(event, app_cursor, app_keypad) else {
            return;
        };
        let bytes = encode_key(&key_event);
        if bytes.is_empty() {
            return;
        }
        if let Ok(mut session) = handle.lock() {
            let _ = session.write_input(&bytes);
        }
    }

    fn paste(&mut self, cx: &mut Context<Self>) {
        let text = cx
            .read_from_clipboard()
            .and_then(|item| item.text().map(|s| s.to_string()));
        let Some(text) = text else { return };
        let Some(session_id) = self.focused_session_id() else {
            return;
        };
        let Some(handle) = self.coordinator.registry().get(session_id) else {
            return;
        };
        let bytes = {
            let bracketed = handle.lock().map(|s| s.bracketed_paste()).unwrap_or(false);
            prepare_paste(&text, bracketed)
        };
        if let Ok(mut session) = handle.lock() {
            let _ = session.write_input(&bytes);
        }
    }

    /// Map a window-relative pointer position onto a grid cell for one pane.
    /// Returns `None` outside the painted grid (no clamping).
    fn pos_to_cell(
        &mut self,
        pane: PaneId,
        position: gpui::Point<Pixels>,
        cx: &App,
    ) -> Option<CellPoint> {
        let origin = self.grid_origins.get(&pane)?.get();
        let fonts = self.fonts(cx);
        let cell_width: f32 = fonts.cell_width.into();
        let line_height: f32 = fonts.line_height.into();
        if cell_width <= 0.0 || line_height <= 0.0 {
            return None;
        }
        let session_id = self.session_id_for_pane(pane)?;
        let snapshot = self.snapshots.get(&session_id)?;
        let cols = snapshot.cols as usize;
        let lines = snapshot.lines as usize;
        if cols == 0 || lines == 0 {
            return None;
        }
        let rel_x = f32::from(position.x) - f32::from(origin.x);
        let rel_y = f32::from(position.y) - f32::from(origin.y);
        let col = (rel_x / cell_width).floor() as isize;
        let row = (rel_y / line_height).floor() as isize;
        if row < 0 || col < 0 || row >= lines as isize || col >= cols as isize {
            return None;
        }
        Some(CellPoint::new(row as usize, col as usize))
    }

    fn on_mouse_down(
        &mut self,
        pane: PaneId,
        event: &MouseDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.coordinator.tree().find(pane).is_none() {
            return;
        }
        // Click focuses first; selection starts only inside the grid.
        if self.coordinator.focused() != Some(pane) {
            let _ = self.coordinator.focus_pane(pane);
            cx.notify();
        }
        window.focus(&self.focus_handle);
        let Some(cell) = self.pos_to_cell(pane, event.position, cx) else {
            return;
        };
        let Some(session_id) = self.session_id_for_pane(pane) else {
            return;
        };
        self.selecting = Some(pane);
        self.selections
            .insert(session_id, SelectionRange::new(cell, cell));
        cx.notify();
    }

    fn on_mouse_move(
        &mut self,
        pane: PaneId,
        event: &MouseMoveEvent,
        _window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.coordinator.tree().find(pane).is_none() {
            return;
        }
        // Hover is the pane focus model: keyboard input follows the pointer
        // without requiring a click. During a drag, the selection continues
        // to belong to the pane where the drag began.
        if self.coordinator.focused() != Some(pane) && self.coordinator.focus_pane(pane).is_ok() {
            cx.notify();
        }
        if self.selecting != Some(pane) {
            return;
        }
        let Some(cell) = self.pos_to_cell(pane, event.position, cx) else {
            return;
        };
        let Some(session_id) = self.session_id_for_pane(pane) else {
            return;
        };
        if let Some(selection) = self.selections.get_mut(&session_id) {
            selection.active = cell;
            cx.notify();
        }
    }

    fn on_mouse_up(
        &mut self,
        pane: PaneId,
        event: &MouseUpEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.selecting != Some(pane) {
            return;
        }
        self.selecting = None;
        let Some(session_id) = self.session_id_for_pane(pane) else {
            return;
        };
        if let Some(cell) = self.pos_to_cell(pane, event.position, cx)
            && let Some(selection) = self.selections.get_mut(&session_id)
        {
            selection.active = cell;
        }
        match self.selections.get(&session_id).copied() {
            Some(range) if range.is_empty() => {
                self.selections.remove(&session_id);
            }
            Some(range) => {
                if let Some(snapshot) = self.snapshots.get(&session_id) {
                    let text = extract_text(snapshot, range);
                    if !text.is_empty() {
                        cx.write_to_primary(ClipboardItem::new_string(text));
                    }
                }
            }
            None => {}
        }
        cx.notify();
    }

    /// Explicit clipboard copy of the focused pane's selection (Ctrl+Shift+C).
    fn copy_selection(&mut self, cx: &mut Context<Self>) {
        let Some(session_id) = self.focused_session_id() else {
            return;
        };
        let Some(range) = self.selections.get(&session_id).copied() else {
            return;
        };
        if range.is_empty() {
            return;
        };
        let Some(snapshot) = self.snapshots.get(&session_id) else {
            return;
        };
        let text = extract_text(snapshot, range);
        if text.is_empty() {
            return;
        }
        cx.write_to_clipboard(ClipboardItem::new_string(text));
    }

    /// Flash the scroll thumb for [`SCROLL_INDICATOR_FADE_MS`], then hide it.
    fn flash_scroll_indicator(&mut self, session_id: SessionId, cx: &mut Context<Self>) {
        self.scroll_indicator_until.insert(
            session_id,
            Instant::now() + Duration::from_millis(SCROLL_INDICATOR_FADE_MS),
        );
        cx.notify();
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            Timer::after(Duration::from_millis(SCROLL_INDICATOR_FADE_MS + 50)).await;
            let _ = weak.update(cx, |view, cx| {
                if view
                    .scroll_indicator_until
                    .get(&session_id)
                    .is_some_and(|deadline| Instant::now() >= *deadline)
                {
                    view.scroll_indicator_until.remove(&session_id);
                    cx.notify();
                }
            });
        })
        .detach();
    }

    fn on_scroll_wheel(
        &mut self,
        session_id: SessionId,
        event: &ScrollWheelEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(handle) = self.coordinator.registry().get(session_id) else {
            return;
        };
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
        let mut steps = dy_lines.round() as i32;
        if steps == 0 && dy_lines != 0.0 {
            steps = dy_lines.signum() as i32;
        }
        if steps == 0 {
            return;
        }

        let Ok(session) = handle.lock() else {
            return;
        };
        if session.viewport().is_alt_screen {
            let app_cursor = session.app_cursor();
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
                if let Ok(mut session) = handle.lock() {
                    let _ = session.write_input(&bytes);
                }
            }
            return;
        }
        drop(session);
        if let Ok(mut session) = handle.lock() {
            session.scroll(ScrollCommand::Lines(steps));
            self.snapshots.insert(session_id, session.viewport());
        }
        self.flash_scroll_indicator(session_id, cx);
    }

    fn render_node(
        &mut self,
        node: &PaneNode,
        _window: &mut Window,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        match node.clone() {
            PaneNode::Pane(pane) => self.render_leaf(pane, cx),
            PaneNode::Split {
                axis,
                fraction,
                first,
                second,
                ..
            } => {
                let first = self.render_node(&first, _window, cx);
                let second = self.render_node(&second, _window, cx);
                let first = match axis {
                    SplitAxis::Horizontal => {
                        div().w(relative(fraction)).h_full().flex().child(first)
                    }
                    SplitAxis::Vertical => div().h(relative(fraction)).w_full().flex().child(first),
                };
                let second = match axis {
                    SplitAxis::Horizontal => div()
                        .w(relative(1.0 - fraction))
                        .h_full()
                        .flex()
                        .child(second),
                    SplitAxis::Vertical => div()
                        .h(relative(1.0 - fraction))
                        .w_full()
                        .flex()
                        .child(second),
                };
                let container = div().flex().flex_1().size_full();
                match axis {
                    SplitAxis::Horizontal => container.flex_row(),
                    SplitAxis::Vertical => container.flex_col(),
                }
                .child(first)
                .child(second)
                .into_any_element()
            }
        }
    }

    fn render_leaf(&mut self, pane: Pane, cx: &mut Context<Self>) -> gpui::AnyElement {
        let pane_id = pane.id;
        let fonts = self.fonts(cx);
        let origin = self
            .grid_origins
            .entry(pane_id)
            .or_insert_with(|| {
                Rc::new(Cell::new(gpui::Point {
                    x: px(0.0),
                    y: px(0.0),
                }))
            })
            .clone();

        let session_id = match pane.content {
            PaneContent::Terminal(id) => id,
            PaneContent::Empty => {
                return div()
                    .flex()
                    .flex_1()
                    .m_1()
                    .items_center()
                    .justify_center()
                    .bg(rgb(0x18181B))
                    .text_color(rgb(0xA1A1AA))
                    .child("Empty pane")
                    .into_any_element();
            }
        };

        if !self.coordinator.registry().contains(session_id) {
            return div()
                .flex()
                .flex_1()
                .m_1()
                .items_center()
                .justify_center()
                .bg(rgb(0x18181B))
                .text_color(rgb(0xA1A1AA))
                .child("Terminal closed.")
                .into_any_element();
        }

        let Some(snapshot) = self.snapshots.get(&session_id).cloned() else {
            return div()
                .flex()
                .flex_1()
                .m_1()
                .items_center()
                .justify_center()
                .bg(rgb(0x18181B))
                .text_color(rgb(0xA1A1AA))
                .child("Starting shell…")
                .into_any_element();
        };

        let focused = self.coordinator.focused() == Some(pane_id);
        let cursor_color: Hsla = rgb(0xE4E4E7).into();
        let show_scrollbar = self
            .scroll_indicator_until
            .get(&session_id)
            .is_some_and(|deadline| Instant::now() < *deadline);
        let selection = self.selections.get(&session_id).copied().and_then(|range| {
            if range.is_empty() {
                None
            } else {
                Some(range.normalized())
            }
        });

        let border = if focused { 0xA1A1AA } else { 0x27272A };
        div()
            .flex()
            .flex_1()
            .flex_col()
            .size_full()
            .bg(rgb(0x18181B))
            .border_1()
            .border_color(rgb(border))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |view, event: &MouseDownEvent, window, cx| {
                    view.on_mouse_down(pane_id, event, window, cx);
                }),
            )
            .on_mouse_move(
                cx.listener(move |view, event: &MouseMoveEvent, window, cx| {
                    view.on_mouse_move(pane_id, event, window, cx);
                }),
            )
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(move |view, event: &MouseUpEvent, window, cx| {
                    view.on_mouse_up(pane_id, event, window, cx);
                }),
            )
            .on_scroll_wheel(
                cx.listener(move |view, event: &ScrollWheelEvent, window, cx| {
                    view.on_scroll_wheel(session_id, event, window, cx);
                }),
            )
            .child(div().flex_1().size_full().child(canvas(
                move |bounds, _, _| bounds,
                move |bounds: Bounds<Pixels>,
                      bounds_prepaint: Bounds<Pixels>,
                      window: &mut Window,
                      cx: &mut App| {
                    // Grid origin for mouse-to-cell mapping. PTY sizing is
                    // handled in `render` via window geometry x pane
                    // fractions (deterministic); paint never resizes, so a
                    // transient canvas offer can never collapse a live grid.
                    origin.set(bounds_prepaint.origin);
                    paint_terminal(
                        bounds,
                        bounds_prepaint,
                        &PaintArgs {
                            snapshot: &snapshot,
                            fonts: &fonts,
                            cursor_color,
                            show_scrollbar,
                            selection,
                        },
                        window,
                        cx,
                    );
                },
            )))
            .into_any_element()
    }

    /// Match every live PTY grid to its pane's share of the window.
    ///
    /// Geometry comes from the window size times the core normalized pane
    /// rects (the same fractions the layout renders), never from transient
    /// canvas offers — so an unsettled layout pass can never collapse a
    /// live grid. Sessions resize in place: shell, scrollback, PID, and CWD
    /// survive. Offers below 2x2 are layout noise and are skipped (M2
    /// fractions guarantee larger panes at sane window sizes).
    fn resize_panes_to_window(&mut self, window: &Window, cx: &mut App) {
        let fonts = self.fonts(cx);
        let cell_width: f32 = fonts.cell_width.into();
        let line_height: f32 = fonts.line_height.into();
        if cell_width <= 0.0 || line_height <= 0.0 {
            return;
        }
        let viewport = window.viewport_size();
        let window_width: f32 = viewport.width.into();
        let window_height: f32 = viewport.height.into();
        for pane_rect in self.coordinator.tree().pane_rects() {
            let Some(session_id) = self.coordinator.session_id_for_pane(pane_rect.pane) else {
                continue;
            };
            let cols =
                ((window_width * pane_rect.rect.width / cell_width).floor() as u16).clamp(2, 500);
            let rows = ((window_height * pane_rect.rect.height / line_height).floor() as u16)
                .clamp(1, 500);
            if cols < 2 || rows < 2 {
                continue;
            }
            if self.grid_sizes.get(&session_id) == Some(&(cols, rows)) {
                continue;
            }
            if let Some(handle) = self.coordinator.registry().get(session_id)
                && let Ok(mut session) = handle.lock()
            {
                session.resize(cols, rows);
                // The engine grid changes synchronously, while PTY output
                // may be quiet. Publish the new viewport now so rendering
                // never draws an old-sized grid after a pane resize.
                self.snapshots.insert(session_id, session.viewport());
                self.grid_sizes.insert(session_id, (cols, rows));
            }
        }
    }
}

impl Render for WorkspaceView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.resize_panes_to_window(window, cx);
        let content = self
            .coordinator
            .tree()
            .root()
            .cloned()
            .map(|root| self.render_node(&root, window, cx))
            .unwrap_or_else(|| {
                div()
                    .size_full()
                    .flex()
                    .flex_col()
                    .items_center()
                    .justify_center()
                    .gap_2()
                    .bg(rgb(0x18181B))
                    .text_color(rgb(0xA1A1AA))
                    .child("No terminals. Start a fresh shell:")
                    .child(
                        div()
                            .px_3()
                            .py_2()
                            .border_1()
                            .border_color(rgb(0x52525B))
                            .text_color(rgb(0xE4E4E7))
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(|view, _event: &MouseDownEvent, window, cx| {
                                    window.focus(&view.focus_handle);
                                    view.new_terminal_for_empty(cx);
                                }),
                            )
                            .child("New terminal (Ctrl+Shift+T)"),
                    )
                    .into_any_element()
            });
        div()
            .track_focus(&self.focus_handle)
            .on_key_down(cx.listener(Self::on_key_down))
            .size_full()
            .flex()
            .bg(rgb(0x18181B))
            .child(content)
    }
}

/// Translate a GPUI key event into the GPUI-free [`KeyEvent`].
fn translate_key(event: &KeyDownEvent, app_cursor: bool, app_keypad: bool) -> Option<KeyEvent> {
    let modifiers = &event.keystroke.modifiers;
    let mods = KeyModifiers {
        ctrl: modifiers.control,
        alt: modifiers.alt,
        shift: modifiers.shift,
        super_key: modifiers.platform,
    };
    let key = match event.keystroke.key.to_lowercase().replace('_', "").as_str() {
        // "return" is what some Wayland virtual keyboards (e.g. wtype
        // `-k Return`) report for the main Enter key; "kpenter" is the
        // keypad variant. Physical keyboards report "enter".
        "enter" | "return" | "kpenter" => Key::Enter,
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

/// Preferred monospace families, in order.
const MONO_PREFERENCES: &[&str] = &[
    "JetBrainsMono Nerd Font",
    "JetBrainsMono NF",
    "DejaVu Sans Mono",
    "Liberation Mono",
    "Noto Sans Mono",
    "monospace",
];

/// Glyph fallback chain for symbols the primary font lacks.
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

/// Terminal font set: base + bold/italic variants, ligatures disabled.
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

/// Resolved font IDs plus grid metrics, cached per font size.
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
    for variant in &fonts[1..] {
        cx.text_system().resolve_font(variant);
    }
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
            TermColor::DefaultBg => rgb(0xE4E4E7).into(),
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

/// Paint parameters bundled so `paint_terminal` stays under clippy's
/// `too_many_arguments` threshold.
struct PaintArgs<'a> {
    snapshot: &'a TerminalViewport,
    fonts: &'a ResolvedFonts,
    cursor_color: Hsla,
    show_scrollbar: bool,
    selection: Option<(CellPoint, CellPoint)>,
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
    let selection = args.selection;
    let origin = origin_bounds.origin;
    let cell_width = fonts.cell_width;
    let line_height = fonts.line_height;
    let font_size = fonts.font_size;

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

        if let Some((sel_start, sel_end)) = selection
            && row_idx >= sel_start.row
            && row_idx <= sel_end.row
            && !row.cells.is_empty()
        {
            let last = row.cells.len() - 1;
            let c0 = if row_idx == sel_start.row {
                sel_start.col.min(last)
            } else {
                0
            };
            let c1 = if row_idx == sel_end.row {
                sel_end.col.min(last)
            } else {
                last
            };
            if c1 >= c0 {
                let x0 = origin.x + cell_width * (c0 as f32);
                let x1 = origin.x + cell_width * ((c1 + 1) as f32);
                window.paint_quad(gpui::fill(
                    Bounds {
                        origin: gpui::Point { x: x0, y },
                        size: gpui::Size {
                            width: x1 - x0,
                            height: line_height,
                        },
                    },
                    rgba(0x3B82F64D),
                ));
            }
        }

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
                cx.new(|cx| WorkspaceView::new(window, cx))
            },
        )
        .expect("failed to open OmaTerm window");

        cx.activate(true);
    });
}
