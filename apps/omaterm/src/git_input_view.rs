//! Keyboard and presentation adapter for the two single-line Git fields.
use crate::git_input::GitInput;
use gpui::{
    App, ClipboardItem, Div, HighlightStyle, KeyDownEvent, StyledText, div, prelude::*, px, rgb,
};

pub enum InputAction {
    Edit,
    Submit,
    Release,
}

pub fn captures(event: &KeyDownEvent) -> bool {
    let modifiers = event.keystroke.modifiers;
    !modifiers.alt
        && !modifiers.platform
        && (!modifiers.control
            || matches!(
                event.keystroke.key.to_lowercase().as_str(),
                "a" | "c" | "x" | "v" | "home" | "end"
            ))
}

pub fn on_key(input: &mut GitInput, event: &KeyDownEvent, cx: &mut App) -> InputAction {
    let key = event.keystroke.key.to_lowercase().replace('_', "");
    let modifiers = event.keystroke.modifiers;
    if modifiers.control {
        match key.as_str() {
            "a" => input.select_all(),
            "c" | "x" => {
                if let Some(text) = input.selected_text() {
                    cx.write_to_clipboard(ClipboardItem::new_string(text.to_owned()));
                    if key == "x" {
                        input.delete(false);
                    }
                }
            }
            "v" => {
                if let Some(text) = cx.read_from_clipboard().and_then(|item| item.text()) {
                    input.insert(&text);
                }
            }
            "home" => input.move_to(0, modifiers.shift),
            "end" => input.move_to(input.text().len(), modifiers.shift),
            _ => {}
        }
    } else {
        match key.as_str() {
            "escape" | "tab" => return InputAction::Release,
            "enter" | "return" | "kpenter" => return InputAction::Submit,
            "home" => input.move_to(0, modifiers.shift),
            "end" => input.move_to(input.text().len(), modifiers.shift),
            "left" => input.move_horizontal(false, modifiers.shift),
            "right" => input.move_horizontal(true, modifiers.shift),
            "backspace" => {
                input.delete(false);
            }
            "delete" => {
                input.delete(true);
            }
            _ => {
                if let Some(text) = &event.keystroke.key_char {
                    input.insert(text);
                }
            }
        }
    }
    InputAction::Edit
}

pub struct FieldView<'a> {
    pub input: Option<&'a GitInput>,
    pub placeholder: &'static str,
    pub focused: bool,
    pub caret_visible: bool,
}

pub fn render(field: FieldView<'_>) -> Div {
    let colors = crate::ui::theme::colors();
    let empty = GitInput::default();
    let input = field.input.unwrap_or(&empty);
    let text = input.text();
    let row = div()
        .flex()
        .items_center()
        .min_w(px(0.0))
        .overflow_hidden()
        .whitespace_nowrap();
    if text.is_empty() && !field.focused {
        return row.text_color(rgb(colors.muted)).child(field.placeholder);
    }
    let cursor = if field.focused {
        input.cursor()
    } else {
        text.len()
    };
    let styled = |start: usize, end: usize| {
        let highlights = input
            .selection()
            .filter(|_| field.focused)
            .and_then(|(a, b)| {
                let a = a.max(start);
                let b = b.min(end);
                (a < b).then(|| {
                    (
                        a - start..b - start,
                        HighlightStyle {
                            background_color: Some({
                                let (h, s, l, a) = colors.selection_hsla;
                                gpui::hsla(h, s, l, a)
                            }),
                            ..Default::default()
                        },
                    )
                })
            });
        StyledText::new(text[start..end].to_owned()).with_highlights(highlights)
    };
    row.child(
        div()
            .min_w(px(0.0))
            .flex()
            .justify_end()
            .overflow_hidden()
            .child(div().flex_shrink_0().child(styled(0, cursor))),
    )
    .child(
        div()
            .w(px(if field.focused { 2.0 } else { 0.0 }))
            .h(px(15.0))
            .flex_shrink_0()
            .when(field.focused && field.caret_visible, |caret| {
                caret.bg(rgb(colors.text))
            }),
    )
    .child(
        div()
            .min_w(px(0.0))
            .overflow_hidden()
            .child(styled(cursor, text.len())),
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
