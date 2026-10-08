/// Mode-aware keyboard encoding: UI key events -> PTY bytes.
///
/// This module is intentionally GPUI-free. The UI translates
/// `gpui::KeyDownEvent` into [`KeyEvent`], then calls [`encode_key`].
/// Terminal modes (`app_cursor`, `app_keypad`) are read from the engine,
/// keeping `omaterm-terminal` independent of `gpui`.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct KeyModifiers {
    pub ctrl: bool,
    pub alt: bool,
    pub shift: bool,
    pub super_key: bool,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Key {
    Char(char),
    Enter,
    Tab,
    Backspace,
    Escape,
    Left,
    Right,
    Up,
    Down,
    Home,
    End,
    PageUp,
    PageDown,
    Insert,
    Delete,
    F(u8),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct KeyEvent {
    pub key: Key,
    pub modifiers: KeyModifiers,
    /// Application-cursor mode (`TermMode::APP_CURSOR`).
    pub app_cursor: bool,
    /// Application-keypad mode (`TermMode::APP_KEYPAD`).
    pub app_keypad: bool,
}

/// Encode a key event into PTY bytes.
pub fn encode_key(event: &KeyEvent) -> Vec<u8> {
    // Alt prefixes ESC, then encodes the remainder without alt.
    if event.modifiers.alt {
        let mut plain = event.clone();
        plain.modifiers.alt = false;
        let mut out = vec![0x1b];
        out.extend_from_slice(&encode_key(&plain));
        return out;
    }
    // Super key is reserved for desktop chrome; never sent to the PTY.
    if event.modifiers.super_key {
        return Vec::new();
    }

    match &event.key {
        Key::Enter => vec![b'\r'],
        Key::Tab => {
            if event.modifiers.shift {
                b"\x1b[Z".to_vec()
            } else {
                vec![b'\t']
            }
        }
        Key::Backspace => vec![0x7f],
        Key::Escape => vec![0x1b],
        Key::Left => encode_arrow('D', event),
        Key::Right => encode_arrow('C', event),
        Key::Up => encode_arrow('A', event),
        Key::Down => encode_arrow('B', event),
        Key::Home => encode_home_end('H', event),
        Key::End => encode_home_end('F', event),
        Key::PageUp => encode_ss3_tilde(b"5", event),
        Key::PageDown => encode_ss3_tilde(b"6", event),
        Key::Insert => encode_ss3_tilde(b"2", event),
        Key::Delete => encode_ss3_tilde(b"3", event),
        Key::F(n) => encode_function_key(*n, event),
        Key::Char(c) => encode_char(*c, event),
    }
}

fn encode_char(c: char, event: &KeyEvent) -> Vec<u8> {
    // Shift+Ctrl+letter still sends the control code; Ctrl with non-letter
    // falls through to plain UTF-8 (safer than dropping).
    if event.modifiers.ctrl
        && let Some(byte) = ctrl_byte(c)
    {
        return vec![byte];
    }
    let mut buf = [0u8; 4];
    c.encode_utf8(&mut buf).as_bytes().to_vec()
}

/// Ctrl+A..Z -> 0x01..0x1A, plus common control codes.
fn ctrl_byte(c: char) -> Option<u8> {
    let lower = c.to_ascii_lowercase();
    if lower.is_ascii_lowercase() {
        return Some((lower as u8) - b'a' + 1);
    }
    match c {
        ' ' => Some(0x00),
        '2' | '@' => Some(0x00),
        '3' | '[' => Some(0x1b),
        '4' | '\\' => Some(0x1c),
        '5' | ']' => Some(0x1d),
        '6' | '^' => Some(0x1e),
        '7' | '_' => Some(0x1f),
        '/' => Some(0x1f),
        _ => None,
    }
}

fn modifier_param(event: &KeyEvent) -> Option<u8> {
    let mut param = 1u8;
    if event.modifiers.shift {
        param += 1;
    }
    if event.modifiers.alt {
        param += 2;
    }
    if event.modifiers.ctrl {
        param += 4;
    }
    if param == 1 { None } else { Some(param) }
}

fn encode_arrow(final_byte: char, event: &KeyEvent) -> Vec<u8> {
    if let Some(param) = modifier_param(event) {
        format!("\x1b[1;{param}{final_byte}").into_bytes()
    } else if event.app_cursor {
        format!("\x1bO{final_byte}").into_bytes()
    } else {
        format!("\x1b[{final_byte}").into_bytes()
    }
}

fn encode_home_end(final_byte: char, event: &KeyEvent) -> Vec<u8> {
    if let Some(param) = modifier_param(event) {
        format!("\x1b[1;{param}{final_byte}").into_bytes()
    } else {
        format!("\x1b[{final_byte}").into_bytes()
    }
}

fn encode_ss3_tilde(num: &[u8], event: &KeyEvent) -> Vec<u8> {
    if let Some(param) = modifier_param(event) {
        let mut out = vec![0x1b, b'['];
        out.extend_from_slice(num);
        out.extend_from_slice(format!(";{param}~").as_bytes());
        out
    } else {
        let mut out = vec![0x1b, b'['];
        out.extend_from_slice(num);
        out.push(b'~');
        out
    }
}

fn encode_function_key(n: u8, event: &KeyEvent) -> Vec<u8> {
    let base: Vec<u8> = match n {
        1 => b"\x1bOP".to_vec(),
        2 => b"\x1bOQ".to_vec(),
        3 => b"\x1bOR".to_vec(),
        4 => b"\x1bOS".to_vec(),
        5 => b"\x1b[15~".to_vec(),
        6 => b"\x1b[17~".to_vec(),
        7 => b"\x1b[18~".to_vec(),
        8 => b"\x1b[19~".to_vec(),
        9 => b"\x1b[20~".to_vec(),
        10 => b"\x1b[21~".to_vec(),
        11 => b"\x1b[23~".to_vec(),
        12 => b"\x1b[24~".to_vec(),
        _ => return Vec::new(),
    };
    if let Some(param) = modifier_param(event) {
        // Simplified modifier form; full kitty protocol is deferred.
        let mut out = b"\x1b[".to_vec();
        out.extend_from_slice(format!("1;{param}").as_bytes());
        out.push(base[base.len() - 1]);
        out
    } else {
        base
    }
}

/// Mouse-wheel buttons in the xterm encoding (button 4 = up, 5 = down).
pub const MOUSE_WHEEL_UP: u8 = 64;
pub const MOUSE_WHEEL_DOWN: u8 = 65;

/// Encode one SGR mouse report (DECSET 1006): `CSI < Cb ; Cx ; Cy M|m`.
/// `button` is the xterm button code (wheel up/down are 64/65). `col` and
/// `row` are zero-based cell coordinates; SGR reports are 1-based. `release`
/// uses the trailing `m` instead of `M`.
pub fn encode_sgr_mouse(button: u8, col: u16, row: u16, release: bool) -> Vec<u8> {
    let final_byte = if release { 'm' } else { 'M' };
    format!(
        "\x1b[<{};{};{}{}",
        button,
        u32::from(col) + 1,
        u32::from(row) + 1,
        final_byte
    )
    .into_bytes()
}

/// Wrap paste content in bracketed-paste markers.
pub fn wrap_bracketed_paste(text: &str) -> Vec<u8> {
    let mut out = b"\x1b[200~".to_vec();
    out.extend_from_slice(text.as_bytes());
    out.extend_from_slice(b"\x1b[201~");
    out
}

/// Prepare clipboard text for PTY delivery: UTF-8 unchanged when the child
/// has not enabled bracketed paste, wrapped exactly once when it has.
/// Multiline and non-ASCII content pass through byte-identical.
pub fn prepare_paste(text: &str, bracketed: bool) -> Vec<u8> {
    if bracketed {
        wrap_bracketed_paste(text)
    } else {
        text.as_bytes().to_vec()
    }
}

/// A paste is risky when it spans lines or carries control characters that
/// could trigger shell behavior beyond plain typing (tab included in the
/// control set would false-positive on indented code, so only line breaks
/// and C0/C1 controls outside common whitespace count). Risky pastes go
/// through the desktop's two-step confirmation; direct pastes send at once.
pub fn needs_paste_confirm(text: &str) -> bool {
    text.chars()
        .any(|char| char == '\n' || char == '\r' || (char.is_control() && !matches!(char, '\t')))
}

/// Escape one filesystem path for POSIX shell input: single-quote with
/// embedded quotes closed, escaped, and reopened. Never appends a newline;
/// the user reviews and submits with Enter.
pub fn escape_shell_path(path: &std::path::Path) -> String {
    let raw = path.to_string_lossy();
    let mut escaped = String::with_capacity(raw.len() + 2);
    escaped.push('\'');
    for chunk in raw.split('\'') {
        // `split` yields empty edge chunks; rejoin with the close-escape-open
        // sequence only between them.
        if escaped.len() > 1 {
            escaped.push_str("'\\''");
        }
        escaped.push_str(chunk);
    }
    escaped.push('\'');
    escaped
}

/// Join dropped filesystem paths for shell input: space-separated escaped
/// paths with a trailing space, no newline. Empty input yields empty output.
pub fn format_dropped_paths(paths: &[std::path::PathBuf]) -> Vec<u8> {
    let mut out = String::new();
    for path in paths {
        out.push_str(&escape_shell_path(path));
        out.push(' ');
    }
    out.into_bytes()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn plain(key: Key) -> KeyEvent {
        KeyEvent {
            key,
            modifiers: KeyModifiers::default(),
            app_cursor: false,
            app_keypad: false,
        }
    }

    #[test]
    fn printable_chars_pass_through() {
        assert_eq!(encode_key(&plain(Key::Char('a'))), b"a");
        assert_eq!(encode_key(&plain(Key::Char('é'))), "é".as_bytes());
    }

    #[test]
    fn ctrl_letters_map_to_control_codes() {
        let mods = KeyModifiers {
            ctrl: true,
            ..Default::default()
        };
        let ev = KeyEvent {
            key: Key::Char('c'),
            modifiers: mods,
            app_cursor: false,
            app_keypad: false,
        };
        assert_eq!(encode_key(&ev), vec![0x03]);
    }

    #[test]
    fn arrows_respect_app_cursor() {
        let normal = plain(Key::Up);
        assert_eq!(encode_key(&normal), b"\x1b[A");
        let app = KeyEvent {
            app_cursor: true,
            ..plain(Key::Up)
        };
        assert_eq!(encode_key(&app), b"\x1bOA");
    }

    #[test]
    fn alt_prefixes_escape() {
        let mods = KeyModifiers {
            alt: true,
            ..Default::default()
        };
        let ev = KeyEvent {
            key: Key::Char('x'),
            modifiers: mods,
            app_cursor: false,
            app_keypad: false,
        };
        assert_eq!(encode_key(&ev), b"\x1bx");
    }

    #[test]
    fn special_keys_encode() {
        assert_eq!(encode_key(&plain(Key::Enter)), b"\r");
        assert_eq!(encode_key(&plain(Key::Backspace)), vec![0x7f]);
        assert_eq!(encode_key(&plain(Key::Tab)), b"\t");
        let shift = KeyModifiers {
            shift: true,
            ..Default::default()
        };
        let ev = KeyEvent {
            key: Key::Tab,
            modifiers: shift,
            app_cursor: false,
            app_keypad: false,
        };
        assert_eq!(encode_key(&ev), b"\x1b[Z");
    }

    #[test]
    fn sgr_mouse_encodes_one_based_wheel_reports() {
        // SGR is 1-based; wheel up is button 64, down is 65, `M` press.
        assert_eq!(
            encode_sgr_mouse(MOUSE_WHEEL_UP, 0, 0, false),
            b"\x1b[<64;1;1M"
        );
        assert_eq!(
            encode_sgr_mouse(MOUSE_WHEEL_DOWN, 9, 4, false),
            b"\x1b[<65;10;5M"
        );
        // Release uses the `m` terminator.
        assert_eq!(encode_sgr_mouse(0, 2, 3, true), b"\x1b[<0;3;4m");
    }

    #[test]
    fn bracketed_paste_wraps() {
        assert_eq!(wrap_bracketed_paste("a\nb"), b"\x1b[200~a\nb\x1b[201~");
    }

    #[test]
    fn ctrl_d_z_map_to_control_codes() {
        let mods = KeyModifiers {
            ctrl: true,
            ..Default::default()
        };
        for (ch, byte) in [('d', 0x04), ('z', 0x1a)] {
            let ev = KeyEvent {
                key: Key::Char(ch),
                modifiers: mods.clone(),
                app_cursor: false,
                app_keypad: false,
            };
            assert_eq!(encode_key(&ev), vec![byte], "ctrl-{ch}");
        }
    }

    #[test]
    fn prepare_paste_plain_and_bracketed() {
        assert_eq!(prepare_paste("plain", false), b"plain");
        assert_eq!(prepare_paste("a\nb", true), b"\x1b[200~a\nb\x1b[201~");
    }

    #[test]
    fn prepare_paste_preserves_unicode_multiline() {
        let text = "héllo 你好\nline2\ttab";
        assert_eq!(prepare_paste(text, false), text.as_bytes());
        let wrapped = prepare_paste(text, true);
        assert!(wrapped.starts_with(b"\x1b[200~"));
        assert!(wrapped.ends_with(b"\x1b[201~"));
        assert_eq!(
            &wrapped[b"\x1b[200~".len()..wrapped.len() - b"\x1b[201~".len()],
            text.as_bytes()
        );
    }

    #[test]
    fn super_key_is_swallowed() {
        let mods = KeyModifiers {
            super_key: true,
            ..Default::default()
        };
        let ev = KeyEvent {
            key: Key::Char('w'),
            modifiers: mods,
            app_cursor: false,
            app_keypad: false,
        };
        assert!(encode_key(&ev).is_empty());
    }

    #[test]
    fn risky_pastes_require_confirmation() {
        for direct in [
            "ls -la",
            "cargo test -- --nocapture",
            "indented\tcode",
            "héllo 你好",
        ] {
            assert!(!needs_paste_confirm(direct), "{direct:?}");
        }
        for risky in [
            "line one\nline two",
            "trailing newline\n",
            "carriage\rreturn",
            "bell\x07here",
            "escape\x1b[2J",
        ] {
            assert!(needs_paste_confirm(risky), "{risky:?}");
        }
    }

    #[test]
    fn shell_paths_escape_safely() {
        use std::path::{Path, PathBuf};
        assert_eq!(
            escape_shell_path(Path::new("/tmp/plain dir/f")),
            "'/tmp/plain dir/f'"
        );
        assert_eq!(
            escape_shell_path(Path::new("/tmp/o'brien/x")),
            "'/tmp/o'\\''brien/x'"
        );
        assert_eq!(escape_shell_path(Path::new("")), "''");
        assert_eq!(
            format_dropped_paths(&[PathBuf::from("/a b"), PathBuf::from("/c'd")]),
            b"'/a b' '/c'\\''d' ".as_slice()
        );
        assert!(format_dropped_paths(&[]).is_empty());
        // Unicode survives lossy conversion byte-identical.
        assert_eq!(
            format_dropped_paths(&[PathBuf::from("/tmp/снег")]),
            "'/tmp/снег' ".as_bytes()
        );
    }
}
