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
}
