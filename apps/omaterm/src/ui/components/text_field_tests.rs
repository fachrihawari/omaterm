use super::*;
use gpui::Keystroke;

struct FakeClipboard {
    value: Option<String>,
}

impl FieldClipboard for FakeClipboard {
    fn read(&mut self) -> Option<String> {
        self.value.clone()
    }
    fn write(&mut self, text: &str) {
        self.value = Some(text.to_owned());
    }
}

fn key(chord: &str) -> KeyDownEvent {
    KeyDownEvent {
        keystroke: Keystroke::parse(chord).unwrap(),
        is_held: false,
    }
}

fn route(field: &mut TextField, chord: &str, clipboard: &mut FakeClipboard) -> FieldAction {
    on_key(field, &key(chord), clipboard)
}

#[test]
fn select_all_then_replace_replaces_the_whole_buffer() {
    let mut field = TextField::new().with_text("hello world");
    let mut clip = FakeClipboard { value: None };
    assert_eq!(route(&mut field, "ctrl-a", &mut clip), FieldAction::Edited);
    assert_eq!(field.selected_text(), Some("hello world"));
    field.insert("done");
    assert_eq!(field.text(), "done");
}

#[test]
fn ctrl_a_is_captured_and_owned() {
    // The global handler and the router must agree: a chord `captures()`
    // claims must never fall through to `Ignored`.
    let event = key("ctrl-a");
    assert!(captures(&event));
    let mut field = TextField::new().with_text("abc");
    let mut clip = FakeClipboard { value: None };
    assert_eq!(on_key(&mut field, &event, &mut clip), FieldAction::Edited);
    assert_eq!(field.selected_text(), Some("abc"));
}

#[test]
fn word_motion_and_word_delete_work_on_whitespace_runs() {
    let mut field = TextField::new().with_text("alpha beta gamma");
    let mut clip = FakeClipboard { value: None };
    // From the end, one word left lands at the start of "gamma".
    route(&mut field, "ctrl-left", &mut clip);
    assert_eq!(field.cursor(), "alpha beta ".len());
    // Ctrl+Backspace from a word start removes the previous word and the space
    // before it (standard editor behavior).
    route(&mut field, "ctrl-backspace", &mut clip);
    assert_eq!(field.text(), "alpha gamma");
    // Ctrl+Right from the start lands on the next word start, symmetric with
    // Ctrl+Left.
    field.move_to(0, false);
    route(&mut field, "ctrl-right", &mut clip);
    assert_eq!(field.cursor(), "alpha ".len());
    route(&mut field, "ctrl-right", &mut clip);
    assert_eq!(field.cursor(), "alpha gamma".len());
}

#[test]
fn ctrl_shift_left_extends_selection_then_copy_returns_it() {
    let mut field = TextField::new().with_text("one two");
    let mut clip = FakeClipboard { value: None };
    route(&mut field, "ctrl-shift-left", &mut clip);
    assert_eq!(field.selected_text(), Some("two"));
    route(&mut field, "ctrl-c", &mut clip);
    assert_eq!(clip.value.as_deref(), Some("two"));
    // Cut removes the selection.
    route(&mut field, "ctrl-x", &mut clip);
    assert_eq!(field.text(), "one ");
}

#[test]
fn undo_redo_round_trip_restores_text_and_caret() {
    let mut field = TextField::new().with_text("abc");
    let mut clip = FakeClipboard { value: None };
    route(&mut field, "ctrl-a", &mut clip);
    field.insert("xyz");
    assert_eq!(field.text(), "xyz");
    route(&mut field, "ctrl-z", &mut clip);
    assert_eq!(field.text(), "abc");
    route(&mut field, "ctrl-y", &mut clip);
    assert_eq!(field.text(), "xyz");
    // Redo is consumed: a second redo is a no-op.
    route(&mut field, "ctrl-shift-z", &mut clip);
    assert_eq!(field.text(), "xyz");
    // Undo again reaches the original seed (the seed is not itself undoable).
    route(&mut field, "ctrl-z", &mut clip);
    assert_eq!(field.text(), "abc");
}

#[test]
fn paste_inserts_at_caret_and_strips_control_chars() {
    let mut field = TextField::new().with_text("ac");
    let mut clip = FakeClipboard {
        value: Some("b\n\t".into()),
    };
    field.move_to(1, false);
    route(&mut field, "ctrl-v", &mut clip);
    // Newline and tab fold to spaces (control chars are dropped); the paste is
    // inserted verbatim, so the buffer keeps both spaces.
    assert_eq!(field.text(), "ab  c");
}

#[test]
fn double_click_selects_the_word_and_escape_releases() {
    let mut field = TextField::new().with_text("foo bar baz");
    on_click(&mut field, 2, "foo ".len() + 1);
    assert_eq!(field.selected_text(), Some("bar"));
    let mut clip = FakeClipboard { value: None };
    assert_eq!(route(&mut field, "escape", &mut clip), FieldAction::Release);
    assert_eq!(route(&mut field, "enter", &mut clip), FieldAction::Submit);
}

#[test]
fn byte_cap_preserves_graphemes() {
    let mut field = TextField::new().with_max_bytes(8);
    field.insert("abcdef");
    assert!(!field.insert("界"));
    assert_eq!(field.text(), "abcdef");
    field.insert("gh");
    assert_eq!(field.text(), "abcdefgh");
}

#[test]
fn captures_preserves_global_shortcuts() {
    for chord in ["ctrl-a", "ctrl-z", "ctrl-shift-left", "ctrl-backspace"] {
        assert!(captures(&key(chord)), "{chord} must edit the field");
    }
    for chord in [
        "ctrl-p",
        "ctrl-shift-g",
        "alt-1",
        "alt-shift-k",
        "ctrl-shift-p",
    ] {
        assert!(!captures(&key(chord)), "{chord} must reach chrome");
    }
}

#[test]
fn ignored_chords_are_not_swallowed() {
    let mut field = TextField::new().with_text("x");
    let mut clip = FakeClipboard { value: None };
    // A control chord the field does not own stays Ignored so chrome can use it.
    assert_eq!(
        route(&mut field, "ctrl-shift-g", &mut clip),
        FieldAction::Ignored
    );
    assert_eq!(field.text(), "x");
}

#[test]
fn modifiers_are_the_only_difference_from_the_old_git_field() {
    // Regression: the old Git-field router only claimed ctrl a/c/x/v/home/end.
    // Ctrl+Backspace and Ctrl+Shift+Arrow must now be owned too.
    let event = key("ctrl-backspace");
    assert!(captures(&event));
    let mut field = TextField::new().with_text("hello world");
    let mut clip = FakeClipboard { value: None };
    assert_eq!(on_key(&mut field, &event, &mut clip), FieldAction::Edited);
    assert_eq!(field.text(), "hello ");
}

#[test]
fn click_selection_and_word_select_keep_a_valid_caret() {
    let mut field = TextField::new().with_text("alpha beta gamma");
    // A single click places the caret (offset supplied by the owner's
    // shaped-prefix measurement).
    on_click(&mut field, 1, 6);
    assert_eq!(field.cursor(), 6);
    assert_eq!(field.selection(), None);
    // A double click selects the word under the pointer.
    on_click(&mut field, 2, 7);
    assert_eq!(field.selected_text(), Some("beta"));
    // A triple click selects the line (all of it, in a single-line field).
    on_click(&mut field, 3, 7);
    assert_eq!(field.selected_text(), Some("alpha beta gamma"));
    // Selecting all then inserting keeps a valid caret.
    field.select_all();
    field.insert("z");
    assert_eq!(field.text(), "z");
}

#[test]
fn keystroke_modifiers_helper_matches() {
    // Sanity: GPUI parses the chords we rely on with the expected modifiers.
    let parsed = Keystroke::parse("ctrl-shift-left").unwrap();
    assert!(parsed.modifiers.control && parsed.modifiers.shift);
    assert!(parsed.modifiers.control);
}
