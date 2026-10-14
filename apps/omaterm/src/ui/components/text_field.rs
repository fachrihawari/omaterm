//! Reusable single-line text field: model + key routing + render.
//!
//! One implementation now backs every single-line input in the app (Git
//! commit/stash/repo-filter, Settings inline edit, palette query, cheatsheet
//! filter, branch picker, terminal find). Previously each surface hand-rolled
//! its own buffer, caret, selection, and clipboard handling, so behaviour
//! drifted: some supported `Ctrl+A`, some did not, and none supported word
//! motion or undo.
//!
//! Rendering follows the editor's caret discipline (`main.rs` editor rows): the
//! whole string is shaped once and the caret is an absolutely-positioned
//! overlay measured from the shaped prefix. The previous Git-field renderer
//! split one string into two independently-shaped runs with a fixed-width
//! caret box wedged between them. In the proportional UI font that re-shapes
//! glyphs at the caret boundary and drops the kerning across it, which is why
//! the text appeared to shift and leave a gap as the caret moved. A single
//! shaped line cannot drift.

use gpui::prelude::*;
use gpui::{
    App, Bounds, ClipboardItem, Div, FontWeight, HighlightStyle, Hsla, KeyDownEvent, Pixels,
    SharedString, StyledText, TextRun, Window, canvas, div, fill, font, hsla, point, px, rgb, size,
};
use unicode_segmentation::UnicodeSegmentation;

use crate::editor::{EditorCaret, next_grapheme, previous_grapheme};
use crate::ui::metrics;
use crate::ui::theme;

/// Default byte ceiling for a field. Git commit drafts keep their own
/// `MAX_MESSAGE_BYTES`; this bounds the generic fields (palette query, branch
/// name, terminal find).
pub const DEFAULT_MAX_BYTES: usize = 4096;

/// What a routed keystroke did, so the owning surface can react.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FieldAction {
    /// The buffer or caret changed; the owner should repaint.
    Edited,
    /// The field released focus (`Esc`/`Tab`); the owner decides where focus goes.
    Release,
    /// `Enter`/`Return` with no modifiers.
    Submit,
    /// The key was not ours; leave it for the owner/global routing.
    Ignored,
}

/// A single-line text buffer with grapheme-aware caret, selection, word motion,
/// clipboard, and undo/redo. Text is always single-line: newlines and tabs fold
/// to spaces and other control characters are dropped on insert.
#[derive(Debug, Clone)]
pub struct TextField {
    text: String,
    caret: EditorCaret,
    /// Undo stack of prior `(text, caret)` snapshots (oldest first).
    undo: Vec<(String, EditorCaret)>,
    /// Redo stack, cleared on every fresh edit.
    redo: Vec<(String, EditorCaret)>,
    max_bytes: usize,
}

impl Default for TextField {
    fn default() -> Self {
        Self::new()
    }
}

impl TextField {
    pub fn new() -> Self {
        Self {
            text: String::new(),
            caret: EditorCaret::default(),
            undo: Vec::new(),
            redo: Vec::new(),
            max_bytes: DEFAULT_MAX_BYTES,
        }
    }

    /// Bound the buffer in bytes (grapheme-aligned inserts).
    pub fn with_max_bytes(mut self, max_bytes: usize) -> Self {
        self.max_bytes = max_bytes.max(1);
        self
    }

    /// Seed the buffer (used to restore a draft) and place the caret at the end.
    pub fn with_text(mut self, text: impl AsRef<str>) -> Self {
        self.text.clear();
        self.insert(text.as_ref());
        self.caret.collapse_to(self.text.len());
        self.undo.clear();
        self.redo.clear();
        self
    }

    pub fn text(&self) -> &str {
        &self.text
    }

    pub fn into_text(self) -> String {
        self.text
    }

    pub fn is_empty(&self) -> bool {
        self.text.is_empty()
    }

    pub fn cursor(&self) -> usize {
        self.caret.cursor
    }

    pub fn selection(&self) -> Option<(usize, usize)> {
        self.caret.selection_range()
    }

    pub fn selected_text(&self) -> Option<&str> {
        self.caret
            .selection_range()
            .map(|(start, end)| &self.text[start..end])
    }

    /// Select the entire buffer (caret lands at the end).
    pub fn select_all(&mut self) {
        self.caret.anchor = Some(0);
        self.caret.cursor = self.text.len();
    }

    /// Snapshot the current state for undo before a mutating operation.
    fn push_undo(&mut self) {
        self.undo.push((self.text.clone(), self.caret));
        self.redo.clear();
        const MAX_UNDO: usize = 256;
        if self.undo.len() > MAX_UNDO {
            self.undo.remove(0);
        }
    }

    /// Insert text at the caret, replacing any selection. Returns whether the
    /// buffer changed.
    pub fn insert(&mut self, text: &str) -> bool {
        let clean: String = text
            .chars()
            .filter_map(|ch| match ch {
                '\n' | '\r' | '\t' => Some(' '),
                ch if ch.is_control() => None,
                ch => Some(ch),
            })
            .collect();
        if clean.is_empty() {
            return false;
        }
        let (start, end) = self
            .caret
            .selection_range()
            .unwrap_or((self.caret.cursor, self.caret.cursor));
        let available = self
            .max_bytes
            .saturating_sub(self.text.len() - (end - start));
        let accepted = clean
            .grapheme_indices(true)
            .take_while(|(offset, cluster)| offset + cluster.len() <= available)
            .last()
            .map_or(0, |(offset, cluster)| offset + cluster.len());
        if accepted == 0 {
            return false;
        }
        self.push_undo();
        self.text.replace_range(start..end, &clean[..accepted]);
        self.caret.collapse_to(start + accepted);
        self.caret.clamp(&self.text);
        true
    }

    pub fn move_to(&mut self, offset: usize, extend: bool) {
        if extend {
            self.caret.anchor.get_or_insert(self.caret.cursor);
            self.caret.cursor = offset;
        } else {
            self.caret.collapse_to(offset);
        }
        self.caret.clamp(&self.text);
    }

    /// Move one grapheme left/right. A collapsed motion at a selection edge
    /// first collapses the selection, matching platform text fields.
    pub fn move_horizontal(&mut self, forward: bool, extend: bool) {
        let offset = if !extend && let Some((start, end)) = self.caret.selection_range() {
            if forward { end } else { start }
        } else if forward {
            next_grapheme(&self.text, self.caret.cursor)
        } else {
            previous_grapheme(&self.text, self.caret.cursor)
        };
        self.move_to(offset, extend);
    }

    /// Move one word left/right. Word boundaries are runs of non-space
    /// characters, so `foo_bar.baz` moves over punctuation in one step
    /// (matching most editors' coarse `Ctrl+arrow`).
    pub fn move_word(&mut self, forward: bool, extend: bool) {
        let offset = if !extend && let Some((start, end)) = self.caret.selection_range() {
            if forward { end } else { start }
        } else if forward {
            next_word_boundary(&self.text, self.caret.cursor)
        } else {
            previous_word_boundary(&self.text, self.caret.cursor)
        };
        self.move_to(offset, extend);
    }

    /// Delete the current selection, or one grapheme forward/backward.
    pub fn delete(&mut self, forward: bool) -> bool {
        let (start, end) = self.caret.selection_range().unwrap_or_else(|| {
            if forward {
                (
                    self.caret.cursor,
                    next_grapheme(&self.text, self.caret.cursor),
                )
            } else {
                (
                    previous_grapheme(&self.text, self.caret.cursor),
                    self.caret.cursor,
                )
            }
        });
        if start == end {
            return false;
        }
        self.push_undo();
        self.text.replace_range(start..end, "");
        self.caret.collapse_to(start);
        self.caret.clamp(&self.text);
        true
    }

    /// Delete one word forward/backward, or the selection if present.
    pub fn delete_word(&mut self, forward: bool) -> bool {
        let (start, end) = self.caret.selection_range().unwrap_or_else(|| {
            if forward {
                (
                    self.caret.cursor,
                    next_word_boundary(&self.text, self.caret.cursor),
                )
            } else {
                (
                    previous_word_boundary(&self.text, self.caret.cursor),
                    self.caret.cursor,
                )
            }
        });
        if start == end {
            return false;
        }
        self.push_undo();
        self.text.replace_range(start..end, "");
        self.caret.collapse_to(start);
        self.caret.clamp(&self.text);
        true
    }

    pub fn undo(&mut self) -> bool {
        let Some((text, caret)) = self.undo.pop() else {
            return false;
        };
        self.redo
            .push((std::mem::replace(&mut self.text, text), caret));
        self.caret = caret;
        self.caret.clamp(&self.text);
        true
    }

    pub fn redo(&mut self) -> bool {
        let Some((text, caret)) = self.redo.pop() else {
            return false;
        };
        self.undo
            .push((std::mem::replace(&mut self.text, text), caret));
        self.caret = caret;
        self.caret.clamp(&self.text);
        true
    }

    /// Select the word under `offset` (double-click). The selection excludes
    /// surrounding whitespace, matching platform text fields.
    pub fn select_word_at(&mut self, offset: usize) {
        let offset = offset.min(self.text.len());
        let start = previous_word_boundary(&self.text, offset);
        let end = word_end_boundary(&self.text, start);
        if start == end {
            self.caret.collapse_to(offset);
        } else {
            self.caret.anchor = Some(start);
            self.caret.cursor = end;
        }
    }

    /// Select the whole line under `offset`. Single-line fields select all.
    pub fn select_line_at(&mut self, _offset: usize) {
        self.select_all();
    }
}

/// Next word boundary after `offset`: consume the current run of non-space
/// characters, then the run of spaces, landing on the start of the next word.
fn next_word_boundary(text: &str, offset: usize) -> usize {
    let offset = offset.min(text.len());
    let mut chars = text[offset..].char_indices().peekable();
    let mut boundary = offset;
    // Consume the current word.
    while let Some((index, ch)) = chars.peek().copied() {
        if !ch.is_whitespace() {
            boundary = offset + index + ch.len_utf8();
            chars.next();
        } else {
            break;
        }
    }
    // Consume the separating spaces so the caret lands on the next word start.
    for (index, ch) in chars {
        if !ch.is_whitespace() {
            return offset + index;
        }
        boundary = offset + index + ch.len_utf8();
    }
    boundary
}

/// End of the run of non-space characters starting at `offset` (used for
/// double-click word selection, which must not include the trailing space).
fn word_end_boundary(text: &str, offset: usize) -> usize {
    let offset = offset.min(text.len());
    let mut boundary = offset;
    for (index, ch) in text[offset..].char_indices() {
        if ch.is_whitespace() {
            break;
        }
        boundary = offset + index + ch.len_utf8();
    }
    boundary
}

/// Previous word boundary before `offset`: skip trailing spaces, then the run
/// of non-spaces before them.
fn previous_word_boundary(text: &str, offset: usize) -> usize {
    let offset = offset.min(text.len());
    let mut boundary = offset;
    let mut chars = text[..offset].char_indices().rev().peekable();
    while let Some((index, ch)) = chars.peek().copied() {
        if ch.is_whitespace() {
            boundary = index;
            chars.next();
        } else {
            break;
        }
    }
    for (index, ch) in chars {
        if ch.is_whitespace() {
            return index + ch.len_utf8();
        }
        boundary = index;
    }
    boundary
}

/// Clipboard access the field needs. A trait so routing is testable without an
/// `App`.
pub trait FieldClipboard {
    fn read(&mut self) -> Option<String>;
    fn write(&mut self, text: &str);
}

impl FieldClipboard for App {
    fn read(&mut self) -> Option<String> {
        self.read_from_clipboard().and_then(|item| item.text())
    }

    fn write(&mut self, text: &str) {
        self.write_to_clipboard(ClipboardItem::new_string(text.to_owned()));
    }
}

impl<T> FieldClipboard for gpui::Context<'_, T> {
    fn read(&mut self) -> Option<String> {
        self.read_from_clipboard().and_then(|item| item.text())
    }

    fn write(&mut self, text: &str) {
        self.write_to_clipboard(ClipboardItem::new_string(text.to_owned()));
    }
}

/// Route one keystroke to the field. Returns what the owner should do.
pub fn on_key(
    field: &mut TextField,
    event: &KeyDownEvent,
    clipboard: &mut dyn FieldClipboard,
) -> FieldAction {
    let key = event.keystroke.key.to_lowercase().replace('_', "");
    let modifiers = event.keystroke.modifiers;

    if modifiers.control && !modifiers.alt {
        match (modifiers.shift, key.as_str()) {
            (false, "a") => {
                field.select_all();
                return FieldAction::Edited;
            }
            (_, "c") => {
                if let Some(text) = field.selected_text() {
                    clipboard.write(text);
                }
                return FieldAction::Edited;
            }
            (_, "x") => {
                if let Some(text) = field.selected_text().map(str::to_owned) {
                    clipboard.write(&text);
                    field.delete(false);
                }
                return FieldAction::Edited;
            }
            (_, "v") => {
                if let Some(text) = clipboard.read() {
                    field.insert(&text);
                }
                return FieldAction::Edited;
            }
            (false, "z") => {
                field.undo();
                return FieldAction::Edited;
            }
            (true, "z") | (false, "y") => {
                field.redo();
                return FieldAction::Edited;
            }
            (_, "left") => {
                field.move_word(false, modifiers.shift);
                return FieldAction::Edited;
            }
            (_, "right") => {
                field.move_word(true, modifiers.shift);
                return FieldAction::Edited;
            }
            (_, "home") => {
                field.move_to(0, modifiers.shift);
                return FieldAction::Edited;
            }
            (_, "end") => {
                field.move_to(field.text().len(), modifiers.shift);
                return FieldAction::Edited;
            }
            (_, "backspace") => {
                field.delete_word(false);
                return FieldAction::Edited;
            }
            (_, "delete") => {
                field.delete_word(true);
                return FieldAction::Edited;
            }
            _ => return FieldAction::Ignored,
        }
    }
    if modifiers.alt || modifiers.platform {
        return FieldAction::Ignored;
    }

    match key.as_str() {
        "escape" | "tab" => FieldAction::Release,
        "enter" | "return" | "kpenter" => FieldAction::Submit,
        "home" => {
            field.move_to(0, modifiers.shift);
            FieldAction::Edited
        }
        "end" => {
            field.move_to(field.text().len(), modifiers.shift);
            FieldAction::Edited
        }
        "left" => {
            field.move_horizontal(false, modifiers.shift);
            FieldAction::Edited
        }
        "right" => {
            field.move_horizontal(true, modifiers.shift);
            FieldAction::Edited
        }
        "backspace" => {
            field.delete(false);
            FieldAction::Edited
        }
        "delete" => {
            field.delete(true);
            FieldAction::Edited
        }
        _ => {
            if let Some(text) = &event.keystroke.key_char
                && field.insert(text)
            {
                return FieldAction::Edited;
            }
            FieldAction::Ignored
        }
    }
}

/// Whether a keystroke is a single-line editing chord the focused field owns.
/// The global handler consults this before falling through to chrome so the set
/// can never diverge from [`on_key`].
pub fn captures(event: &KeyDownEvent) -> bool {
    let modifiers = event.keystroke.modifiers;
    if modifiers.alt || modifiers.platform {
        return false;
    }
    if !modifiers.control {
        return true;
    }
    matches!(
        event.keystroke.key.to_lowercase().as_str(),
        "a" | "c"
            | "x"
            | "v"
            | "z"
            | "y"
            | "home"
            | "end"
            | "left"
            | "right"
            | "backspace"
            | "delete"
    )
}

/// Visual configuration for one field render.
#[derive(Clone)]
pub struct FieldStyle {
    pub font_size: f32,
    pub font_weight: FontWeight,
    pub font_family: SharedString,
    pub caret_height: f32,
    pub text_color: Hsla,
    pub placeholder_color: Hsla,
    pub selection_color: Hsla,
    pub caret_color: Hsla,
}

impl FieldStyle {
    /// Body-role defaults for chrome fields.
    pub fn body() -> Self {
        let colors = theme::colors();
        Self {
            font_size: metrics::BODY_11.size_px,
            font_weight: metrics::BODY_11.weight,
            font_family: SharedString::from(".SystemUIFont"),
            caret_height: metrics::BODY_11.line_height_px,
            text_color: rgb(colors.text).into(),
            placeholder_color: rgb(colors.muted).into(),
            selection_color: selection_color(),
            caret_color: rgb(colors.text).into(),
        }
    }
}

/// Records a field's painted bounds so a click handler can map a pointer
/// position to a caret offset.
///
/// The renderer writes the body rectangle during paint (where the real layout
/// is known); the owner reads it in `on_mouse_down`. `Rc<Cell<..>>` matches the
/// editor's `editor_body_bounds` pattern, which exists for the same reason.
#[derive(Clone, Default)]
pub struct FieldBounds(std::rc::Rc<std::cell::Cell<Option<Bounds<Pixels>>>>);

impl FieldBounds {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn set(&self, bounds: Bounds<Pixels>) {
        self.0.set(Some(bounds));
    }

    /// Caret offset for a click at `position`, using the recorded bounds and
    /// the shaped prefix. `None` when the field has not painted yet.
    pub fn offset_for_click(
        &self,
        field: &TextField,
        position: gpui::Point<Pixels>,
        style: &FieldStyle,
        window: &Window,
    ) -> Option<usize> {
        let bounds = self.0.get()?;
        let x: f32 = (position.x - bounds.origin.x).into();
        if x < 0.0 {
            return Some(0);
        }
        Some(caret_offset_for_click(field, x, style, window))
    }
}

/// Render the field contents (no border/background; the owner draws the box).
///
/// The text is one shaped `StyledText` (selection via highlights). The caret is
/// painted by a transparent `canvas` overlay whose paint callback receives the
/// live `Window`, so the caret's x is measured from the shaped prefix without
/// threading a `Window` through the owner's render builder. Because the caret
/// never participates in layout, moving it cannot re-shape or re-flow the text —
/// the defect the old two-run renderer had.
pub fn render(
    field: Option<&TextField>,
    placeholder: &str,
    focused: bool,
    caret_visible: bool,
    style: &FieldStyle,
) -> Div {
    render_tracked(field, placeholder, focused, caret_visible, style, None)
}

/// [`render`] with an optional bounds recorder. Owners that support
/// click-to-caret pass a [`FieldBounds`]; the recorder is written during paint.
pub fn render_tracked(
    field: Option<&TextField>,
    placeholder: &str,
    focused: bool,
    caret_visible: bool,
    style: &FieldStyle,
    bounds: Option<FieldBounds>,
) -> Div {
    let empty = TextField::new();
    let field = field.unwrap_or(&empty);
    let text = field.text().to_owned();

    let row = div()
        .flex()
        .items_center()
        .min_w(px(0.0))
        .h_full()
        .overflow_hidden()
        .whitespace_nowrap()
        .text_size(px(style.font_size))
        .font_weight(style.font_weight)
        .font_family(style.font_family.clone());

    if text.is_empty() && !focused {
        return row
            .text_color(style.placeholder_color)
            .child(placeholder.to_owned());
    }

    let highlights: Vec<(std::ops::Range<usize>, HighlightStyle)> = field
        .selection()
        .filter(|_| focused)
        .into_iter()
        .map(|(start, end)| {
            (
                start..end,
                HighlightStyle {
                    background_color: Some(style.selection_color),
                    ..Default::default()
                },
            )
        })
        .collect();

    let mut content = div()
        .relative()
        .flex()
        .items_center()
        .min_w(px(0.0))
        .h_full()
        .overflow_hidden()
        .whitespace_nowrap()
        .text_color(style.text_color)
        .child(StyledText::new(text.clone()).with_highlights(highlights));

    if focused {
        let cursor = field.cursor().min(text.len());
        let prefix = text[..cursor].to_owned();
        let caret_style = style.clone();
        let caret_height = caret_style.caret_height;
        let caret_color = caret_style.caret_color;
        let recorder = bounds.clone();
        content = content.child(
            canvas(
                move |bounds, window, _| {
                    // Record where the text actually painted so a click can be
                    // mapped back to a caret offset without a second layout.
                    if let Some(recorder) = &recorder {
                        recorder.set(bounds);
                    }
                    shape_width(&prefix, &caret_style, window)
                },
                move |bounds, x, window, _cx| {
                    if !caret_visible {
                        return;
                    }
                    let top = bounds.origin.y + (bounds.size.height - px(caret_height)) / 2.0;
                    let origin = point(bounds.origin.x + px(x), top);
                    let caret = fill(
                        Bounds::new(origin, size(px(1.5), px(caret_height))),
                        caret_color,
                    );
                    window.paint_quad(caret);
                },
            )
            .absolute()
            .top(px(0.0))
            .left(px(0.0))
            .size_full(),
        );
    } else if let Some(recorder) = &bounds {
        // Unfocused fields still need a recorded rectangle so a click that
        // focuses them lands on the right caret offset immediately.
        let recorder = recorder.clone();
        content = content.child(
            canvas(
                move |bounds, _, _| {
                    recorder.set(bounds);
                    bounds
                },
                |_, _, _, _| {},
            )
            .absolute()
            .top(px(0.0))
            .left(px(0.0))
            .size_full(),
        );
    }
    row.child(content)
}

/// Shape one single-line string and return its pixel width. The caret overlay
/// and the click-to-caret math both use this so they cannot disagree.
fn shape_width(text: &str, style: &FieldStyle, window: &Window) -> f32 {
    if text.is_empty() {
        return 0.0;
    }
    window
        .text_system()
        .shape_line(
            SharedString::from(text.to_owned()),
            px(style.font_size),
            &[TextRun {
                len: text.len(),
                font: font(style.font_family.clone()),
                color: style.text_color,
                background_color: None,
                underline: None,
                strikethrough: None,
            }],
            None,
        )
        .width
        .into()
}

/// Map a click `x` pixels into the field text to the nearest caret byte offset,
/// using the shaped prefix (the same measurement the caret overlay uses).
pub fn caret_offset_for_click(
    field: &TextField,
    click_x: f32,
    style: &FieldStyle,
    window: &Window,
) -> usize {
    let text = field.text();
    if text.is_empty() {
        return 0;
    }
    let mut best = 0usize;
    let mut best_distance = f32::INFINITY;
    for (index, _) in text
        .grapheme_indices(true)
        .chain(std::iter::once((text.len(), "")))
    {
        let width = shape_width(&text[..index], style, window);
        let distance = (width - click_x).abs();
        if distance <= best_distance {
            best_distance = distance;
            best = index;
        }
    }
    best
}

/// Apply a click/double/triple-click to the field's selection.
pub fn on_click(field: &mut TextField, click_count: usize, offset: usize) {
    match click_count {
        0 | 1 => field.move_to(offset, false),
        2 => field.select_word_at(offset),
        _ => field.select_line_at(offset),
    }
}

/// Selection color for a given theme, shared with owners that draw their own
/// selection band.
pub fn selection_color() -> Hsla {
    let (h, s, l, a) = theme::colors().selection_hsla;
    hsla(h, s, l, a)
}

#[cfg(test)]
#[path = "text_field_tests.rs"]
mod tests;
