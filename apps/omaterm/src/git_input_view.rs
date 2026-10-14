//! Keyboard and presentation adapter for the two single-line Git fields.
//!
//! The buffer, caret, selection, word motion, clipboard, and undo/redo now
//! live in [`crate::ui::components::text_field`]. This module only adapts the
//! `GitInput` wrapper to the shared field so Git call sites keep their names,
//! and re-exports the shared render so the Git panel keeps one import.
use gpui::{App, KeyDownEvent};

use crate::git_input::GitInput;
use crate::ui::components::text_field::{self, FieldAction, FieldStyle, TextField};

/// What a routed keystroke did, so the Git owner can react.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum InputAction {
    Edit,
    Submit,
    Release,
}

/// Whether a keystroke is an editing chord the focused field owns.
pub fn captures(event: &KeyDownEvent) -> bool {
    text_field::captures(event)
}

/// Route one keystroke to the Git field.
pub fn on_key(input: &mut GitInput, event: &KeyDownEvent, clipboard: &mut App) -> InputAction {
    match text_field::on_key(input.field_mut(), event, clipboard) {
        FieldAction::Release => InputAction::Release,
        FieldAction::Submit => InputAction::Submit,
        FieldAction::Edited | FieldAction::Ignored => InputAction::Edit,
    }
}

pub struct FieldView<'a> {
    pub input: Option<&'a GitInput>,
    pub placeholder: &'static str,
    pub focused: bool,
    pub caret_visible: bool,
}

/// Render a Git field using the shared single-line renderer.
pub fn render(field: FieldView<'_>) -> gpui::Div {
    render_tracked(field, None)
}

/// [`render`] with an optional bounds recorder for click-to-caret.
pub fn render_tracked(field: FieldView<'_>, bounds: Option<text_field::FieldBounds>) -> gpui::Div {
    let style = FieldStyle::body();
    let empty = TextField::new();
    let text = field.input.map(|input| input.field()).unwrap_or(&empty);
    text_field::render_tracked(
        Some(text),
        field.placeholder,
        field.focused,
        field.caret_visible,
        &style,
        bounds,
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fields_capture_clipboard_editing_but_preserve_global_shortcuts() {
        for chord in [
            "ctrl-a",
            "ctrl-c",
            "ctrl-x",
            "ctrl-v",
            "ctrl-shift-c",
            "ctrl-shift-v",
            "shift-left",
            "delete",
            "home",
            "end",
            // Newly owned by the shared field (regression guard).
            "ctrl-backspace",
            "ctrl-shift-left",
            "ctrl-z",
        ] {
            let event = KeyDownEvent {
                keystroke: gpui::Keystroke::parse(chord).unwrap(),
                is_held: false,
            };
            assert!(captures(&event), "{chord} must edit the field");
        }
        for chord in ["ctrl-p", "ctrl-shift-g", "alt-1", "alt-shift-k"] {
            let event = KeyDownEvent {
                keystroke: gpui::Keystroke::parse(chord).unwrap(),
                is_held: false,
            };
            assert!(!captures(&event), "{chord} must reach chrome");
        }
    }
}
