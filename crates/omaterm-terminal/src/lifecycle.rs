//! Token-authenticated shell lifecycle events (M10 Phase 1).
//!
//! Supported shells report structured lifecycle records through OSC 133
//! sequences on the PTY stream:
//!
//! ```text
//! ESC ] 133 ; A ; <token> ; <exit> BEL   prompt ready, last exit status
//! ESC ] 133 ; A ; <token> BEL             prompt ready (legacy, no status)
//! ESC ] 133 ; B ; <token> ; <b64-cmd> BEL command about to execute
//! ```
//!
//! The sequences are consumed here and never rendered (the Alacritty parser
//! ignores unknown OSC codes). Every record is validated against the
//! session-bound random token: output from untrusted commands cannot forge
//! lifecycle events without knowing 128 bits of per-session secret, and
//! malformed data is dropped silently. Command boundaries and completion
//! come only from these events — never from scraping terminal text.
//!
//! Threat model: the token lives in the shell's memory (private rcfile),
//! never in the child environment. A same-user process that already owns
//! the session can forge events, but so can it type anything; accidental
//! forgery by command output is what authentication defeats.

/// Prompt-ready (`A`) or command-start (`B`) lifecycle record.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LifecycleKind {
    PromptReady,
    CommandStart,
}

/// One validated lifecycle event. `payload` is the decimal exit status
/// (`PromptReady`, possibly empty for the legacy form) or the base64
/// command text (`CommandStart`).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LifecycleEvent {
    pub kind: LifecycleKind,
    pub payload: String,
}

/// Unparsed-tail cap: a valid sequence is at most ~12 KiB (8 KiB commands),
/// so anything larger without a terminator is runaway bytes, not a split
/// sequence. The buffer is dropped past this point (fail closed: a later
/// valid sequence still parses after resync).
pub const MAX_SEQUENCE_BYTES: usize = 128 * 1024;

/// Maximum accepted payload per event. Longer payloads are dropped whole.
pub const MAX_PAYLOAD_BYTES: usize = 64 * 1024;

const ESC: u8 = 0x1b;
const BEL: u8 = 0x07;

/// Streaming, token-authenticated OSC 133 parser over raw PTY bytes.
#[derive(Debug)]
pub struct LifecycleParser {
    token: Vec<u8>,
    buffer: Vec<u8>,
}

impl LifecycleParser {
    #[must_use]
    pub fn new(token: &str) -> Self {
        Self {
            token: token.as_bytes().to_vec(),
            buffer: Vec::new(),
        }
    }

    /// Feed raw PTY output; returns validated events in order. Tolerant of
    /// arbitrary fragmentation; drops anything unauthenticated or malformed.
    pub fn feed(&mut self, bytes: &[u8]) -> Vec<LifecycleEvent> {
        self.buffer.extend_from_slice(bytes);
        if self.buffer.len() > MAX_SEQUENCE_BYTES + MAX_PAYLOAD_BYTES {
            self.buffer.clear();
            return Vec::new();
        }
        let mut events = Vec::new();
        let mut cursor = 0;
        while let Some(parsed) = self.parse_at(cursor) {
            match parsed {
                Parsed::Event(event, next) => {
                    events.push(event);
                    cursor = next;
                }
                Parsed::Incomplete => break,
                Parsed::Resync(next) => cursor = next,
            }
        }
        self.buffer.drain(..cursor);
        if self.buffer.len() > MAX_SEQUENCE_BYTES + MAX_PAYLOAD_BYTES {
            self.buffer.clear();
        }
        events
    }
}

enum Parsed {
    Event(LifecycleEvent, usize),
    /// A valid prefix that may complete with more bytes.
    Incomplete,
    /// Not a lifecycle sequence; resume scanning after `usize`.
    Resync(usize),
}

impl LifecycleParser {
    fn parse_at(&self, start: usize) -> Option<Parsed> {
        let buf = &self.buffer[start..];
        if buf.is_empty() {
            return None;
        }
        let mut offset = 0;
        while offset + 1 < buf.len() {
            if buf[offset] == ESC && buf[offset + 1] == b']' {
                break;
            }
            offset += 1;
        }
        if offset + 1 >= buf.len() {
            // No opening. A trailing lone ESC may still complete one with
            // more input (wait); anything else is plain output (drop).
            if buf.last() == Some(&ESC) {
                return Some(Parsed::Incomplete);
            }
            return Some(Parsed::Resync(start + buf.len()));
        }
        if offset > 0 {
            // Skip non-sequence bytes; re-examine from the opening.
            return Some(Parsed::Resync(start + offset));
        }
        // buf opens with ESC ].
        let mut pos = 2;
        let rest = &buf[pos..];
        if rest.len() < 4 {
            return Some(Parsed::Incomplete);
        }
        if &rest[..4] != b"133;" {
            return Some(Parsed::Resync(start + 2));
        }
        pos += 4;
        let kind = match buf.get(pos) {
            Some(b'A') => LifecycleKind::PromptReady,
            Some(b'B') => LifecycleKind::CommandStart,
            Some(_) => return Some(Parsed::Resync(start + 2)),
            None => return Some(Parsed::Incomplete),
        };
        pos += 1;
        if buf.get(pos) != Some(&b';') {
            // `ESC ] 133 ; X` at the very end may still gain its `;`.
            if pos >= buf.len() {
                return Some(Parsed::Incomplete);
            }
            return Some(Parsed::Resync(start + 2));
        }
        pos += 1;
        // Token: exact match against the session secret.
        if buf.len() < pos + self.token.len() {
            return Some(Parsed::Incomplete);
        }
        if &buf[pos..pos + self.token.len()] != self.token.as_slice() {
            return Some(Parsed::Resync(start + 2));
        }
        pos += self.token.len();
        // Only the prompt kind has a legacy form with no payload section;
        // command starts always carry base64 text after `;`.
        let legacy_prompt = kind == LifecycleKind::PromptReady;
        match buf.get(pos) {
            Some(&BEL) if legacy_prompt => {
                let event = LifecycleEvent {
                    kind,
                    payload: String::new(),
                };
                return Some(Parsed::Event(event, start + pos + 1));
            }
            Some(&ESC) if legacy_prompt => {
                if pos + 1 >= buf.len() {
                    return Some(Parsed::Incomplete);
                }
                if buf[pos + 1] == b'\\' {
                    let event = LifecycleEvent {
                        kind,
                        payload: String::new(),
                    };
                    return Some(Parsed::Event(event, start + pos + 2));
                }
                return Some(Parsed::Resync(start + 2));
            }
            Some(&b';') => pos += 1,
            Some(_) => return Some(Parsed::Resync(start + 2)),
            None => return Some(Parsed::Incomplete),
        }
        // Payload runs to BEL or ST (ESC \). Strict per-kind charset keeps
        // framing unambiguous: neither alphabet contains `;` or ESC.
        let mut end = pos;
        let mut st = false;
        loop {
            match buf.get(end) {
                None => return Some(Parsed::Incomplete),
                Some(&BEL) => break,
                Some(&ESC) if buf.get(end + 1) == Some(&b'\\') => {
                    st = true;
                    break;
                }
                Some(&ESC) => {
                    // A bare ESC ends the candidate (ST needs two bytes,
                    // checked above); a lone trailing ESC may complete.
                    if end + 1 >= buf.len() {
                        return Some(Parsed::Incomplete);
                    }
                    return Some(Parsed::Resync(start + 2));
                }
                Some(_) => end += 1,
            }
            if end - pos > MAX_PAYLOAD_BYTES {
                return Some(Parsed::Resync(start + 2));
            }
        }
        let payload = &buf[pos..end];
        if !valid_payload(kind, payload) {
            return Some(Parsed::Resync(start + 2));
        }
        let event = LifecycleEvent {
            kind,
            payload: String::from_utf8_lossy(payload).into_owned(),
        };
        let next = start + end + if st { 2 } else { 1 };
        Some(Parsed::Event(event, next))
    }
}

fn valid_payload(kind: LifecycleKind, payload: &[u8]) -> bool {
    match kind {
        // Legacy prompt form carries no payload; the status form carries a
        // decimal exit code (0-255, bounded length).
        LifecycleKind::PromptReady => {
            payload.is_empty() || (payload.len() <= 3 && payload.iter().all(|b| b.is_ascii_digit()))
        }
        // Base64 command text: nonempty, bounded, strict alphabet.
        LifecycleKind::CommandStart => {
            !payload.is_empty()
                && payload.len() <= MAX_PAYLOAD_BYTES
                && payload
                    .iter()
                    .all(|b| b.is_ascii_alphanumeric() || *b == b'+' || *b == b'/' || *b == b'=')
        }
    }
}

/// Decode a validated command-start payload to command text. Strict base64
/// and strict UTF-8: anything else is dropped, never guessed.
#[must_use]
pub fn decode_command(payload: &str) -> Option<String> {
    use base64::Engine;
    let bytes = base64::engine::general_purpose::STANDARD
        .decode(payload)
        .ok()?;
    String::from_utf8(bytes).ok()
}

/// Validated exit-status payloads: empty (legacy readiness) or 0-255.
#[must_use]
pub fn decode_exit(payload: &str) -> Option<Option<i32>> {
    if payload.is_empty() {
        return Some(None);
    }
    let status: i32 = payload.parse().ok()?;
    (0..=255).contains(&status).then_some(Some(status))
}

#[cfg(test)]
mod tests {
    use super::*;

    const TOKEN: &str = "test-token-123";

    fn parser() -> LifecycleParser {
        LifecycleParser::new(TOKEN)
    }

    fn prompt(status: &str) -> Vec<u8> {
        format!("\x1b]133;A;{TOKEN};{status}\x07").into_bytes()
    }

    fn legacy_prompt() -> Vec<u8> {
        format!("\x1b]133;A;{TOKEN}\x07").into_bytes()
    }

    fn start(command_b64: &str) -> Vec<u8> {
        format!("\x1b]133;B;{TOKEN};{command_b64}\x07").into_bytes()
    }

    #[test]
    fn prompt_and_command_sequences_parse() {
        let mut parser = parser();
        let events = parser.feed(&prompt("0"));
        assert_eq!(
            events,
            vec![LifecycleEvent {
                kind: LifecycleKind::PromptReady,
                payload: "0".into(),
            }]
        );
        let events = parser.feed(&start("ZWNobyBoZWxsbw=="));
        assert_eq!(
            events,
            vec![LifecycleEvent {
                kind: LifecycleKind::CommandStart,
                payload: "ZWNobyBoZWxsbw==".into(),
            }]
        );
        assert_eq!(
            decode_command("ZWNobyBoZWxsbw==").as_deref(),
            Some("echo hello")
        );
        assert_eq!(decode_exit("1"), Some(Some(1)));
        assert_eq!(decode_exit(""), Some(None));
    }

    #[test]
    fn legacy_prompt_without_status_parses() {
        let mut parser = parser();
        let events = parser.feed(&legacy_prompt());
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].kind, LifecycleKind::PromptReady);
        assert_eq!(events[0].payload, "");
    }

    #[test]
    fn st_terminator_is_accepted() {
        let mut parser = parser();
        let mut bytes = format!("\x1b]133;A;{TOKEN};2").into_bytes();
        bytes.extend_from_slice(b"\x1b\\");
        let events = parser.feed(&bytes);
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].payload, "2");
    }

    #[test]
    fn wrong_token_is_dropped_silently() {
        let mut parser = parser();
        let spoof = b"\x1b]133;B;attacker-token;cmYK\x07prompt $\x1b]133;A;attacker-token;0\x07";
        assert!(parser.feed(spoof).is_empty());
        // The session token still parses afterwards (resync, no poisoning).
        assert_eq!(parser.feed(&prompt("0")).len(), 1);
    }

    #[test]
    fn unknown_kind_and_bad_payloads_are_dropped() {
        let mut parser = parser();
        // Unknown kind.
        assert!(
            parser
                .feed(format!("\x1b]133;X;{TOKEN};0\x07").as_bytes())
                .is_empty()
        );
        // Non-numeric exit payload.
        assert!(
            parser
                .feed(format!("\x1b]133;A;{TOKEN};abc\x07").as_bytes())
                .is_empty()
        );
        // Command payload outside the base64 alphabet (would break framing).
        assert!(
            parser
                .feed(format!("\x1b]133;B;{TOKEN};a;b\x07").as_bytes())
                .is_empty()
        );
        // Empty command payload.
        assert!(
            parser
                .feed(format!("\x1b]133;B;{TOKEN};\x07").as_bytes())
                .is_empty()
        );
    }

    #[test]
    fn fragmented_sequences_reassemble() {
        let full = start("ZWNobyBoZWxsbw==");
        for window in [1, 2, 3, 5, 7] {
            let mut fresh = parser();
            let mut events = Vec::new();
            for chunk in full.chunks(window) {
                events.extend(fresh.feed(chunk));
            }
            assert_eq!(events.len(), 1, "window {window}");
            assert!(fresh.feed(&[]).is_empty());
        }
    }

    #[test]
    fn multiple_sequences_and_noise_in_one_chunk() {
        let mut parser = parser();
        let mut chunk = b"echo output\n".to_vec();
        chunk.extend_from_slice(&start("dHJ1ZQ=="));
        chunk.extend_from_slice(b"\x1b[31mred\x1b[0m");
        chunk.extend_from_slice(&prompt("1"));
        chunk.extend_from_slice(b"\x1b]133;B;wrong;eA==\x07");
        let events = parser.feed(&chunk);
        assert_eq!(events.len(), 2);
        assert_eq!(events[0].kind, LifecycleKind::CommandStart);
        assert_eq!(events[1].kind, LifecycleKind::PromptReady);
    }

    #[test]
    fn oversized_payloads_are_dropped_whole() {
        let mut parser = parser();
        let big = "A".repeat(MAX_PAYLOAD_BYTES + 1);
        let bytes = format!("\x1b]133;B;{TOKEN};{big}\x07").into_bytes();
        assert!(parser.feed(&bytes).is_empty());
        assert_eq!(parser.feed(&prompt("0")).len(), 1);
    }

    #[test]
    fn truncated_candidate_does_not_consume_later_sequences() {
        let mut parser = parser();
        // An ESC ] 133 opener with no terminator yet: buffered, nothing lost.
        assert!(parser.feed(b"\x1b]133;B;test-token-12").is_empty());
        // Completing bytes arrive later and parse.
        let events = parser.feed(b"3;QUJD\x07");
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].payload, "QUJD");
    }

    #[test]
    fn command_decoding_is_strict() {
        assert_eq!(decode_command("YQ==").as_deref(), Some("a"));
        assert_eq!(decode_command("!!!"), None);
        assert_eq!(decode_command("////"), None); // valid b64, invalid UTF-8
        assert_eq!(decode_exit("256"), None);
        assert_eq!(decode_exit("-1"), None);
        assert_eq!(decode_exit("01"), Some(Some(1)));
    }
}
