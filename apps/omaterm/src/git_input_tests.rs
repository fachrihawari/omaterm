use super::*;

#[test]
fn middle_edit_and_shift_selection_replace_whole_graphemes() {
    let mut input = GitInput::default();
    input.insert("a👨‍👩‍👧‍👦e\u{301}z");
    input.move_horizontal(false, false);
    input.delete(false);
    assert_eq!(input.text(), "a👨‍👩‍👧‍👦z");
    input.move_horizontal(false, true);
    assert_eq!(input.selected_text(), Some("👨‍👩‍👧‍👦"));
    input.insert("界");
    assert_eq!(input.text(), "a界z");
    input.delete(true);
    assert_eq!(input.text(), "a界");
}

#[test]
fn selection_collapses_toward_motion_and_cut_deletes_only_selection() {
    let mut input = GitInput::default();
    input.insert("hello world");
    input.move_to(6, false);
    input.move_to(11, true);
    assert_eq!(input.selected_text(), Some("world"));
    input.move_horizontal(false, false);
    assert_eq!(input.caret.cursor, 6);
    assert_eq!(input.selected_text(), None);
    input.move_to(11, true);
    input.delete(false);
    assert_eq!(input.text(), "hello ");
    input.select_all();
    input.insert("replacement");
    assert_eq!(input.text(), "replacement");
}

#[test]
fn byte_cap_preserves_graphemes_and_replacement_capacity() {
    let mut input = GitInput::default();
    input.insert(&"x".repeat(MAX_MESSAGE_BYTES - 1));
    assert!(!input.insert("界"));
    assert_eq!(input.text().len(), MAX_MESSAGE_BYTES - 1);
    input.select_all();
    input.insert("new\nline\tvalue\0");
    assert_eq!(input.text(), "new line value");
    input.select_all();
    input.insert(&"界".repeat(MAX_MESSAGE_BYTES));
    assert_eq!(input.text().len(), 4095);
}

#[test]
fn empty_deletion_and_inserting_combining_marks_keep_valid_caret() {
    let mut input = GitInput::default();
    assert!(!input.delete(false));
    assert!(!input.delete(true));
    input.insert("a");
    input.insert("\u{301}");
    input.delete(false);
    assert_eq!(input.text(), "");
}
