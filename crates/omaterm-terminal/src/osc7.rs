use std::path::PathBuf;

/// Streaming OSC 7 (`ESC ] 7 ; <uri> <terminator>`) extractor.
///
/// Shells report the current directory as `file://<host><path>`; OmaTerm
/// accepts only absolute *local* paths and rejects everything else without
/// touching session state. The parser observes raw PTY bytes in parallel
/// with the VT emulator: bytes are never consumed or altered here.
///
/// Both OSC terminators are supported: BEL (`\x07`) and ST (`ESC \`).
/// Sequences may span PTY reads; an incomplete tail is buffered until the
/// terminator arrives. The buffer is bounded so a never-terminated sequence
/// cannot grow memory without limit.
pub struct Osc7Parser {
    pending: Vec<u8>,
}

/// Maximum buffered bytes for one incomplete OSC 7 sequence. Longer input
/// is discarded as malformed; a legitimate CWD URI is far shorter.
const MAX_SEQUENCE_BYTES: usize = 4096;

impl Osc7Parser {
    pub fn new() -> Self {
        Self {
            pending: Vec::new(),
        }
    }

    /// Feed raw PTY output bytes. Returns one entry per *accepted* local
    /// directory report, in sequence order. Rejected reports yield nothing.
    pub fn feed(&mut self, bytes: &[u8]) -> Vec<PathBuf> {
        self.pending.extend_from_slice(bytes);
        if self.pending.len() > MAX_SEQUENCE_BYTES * 2 {
            // Pathological input (e.g. a binary dump with no terminator):
            // keep only a tail that could still complete a sequence.
            let excess = self.pending.len() - MAX_SEQUENCE_BYTES;
            self.pending.drain(..excess);
        }
        let mut accepted = Vec::new();
        let mut cursor = 0usize;
        while let Some(found) = find_sequence(&self.pending[cursor..]) {
            let (start, payload_end, terminator_len) = found;
            let abs_start = cursor + start;
            let abs_end = cursor + payload_end;
            let payload = &self.pending[abs_start..abs_end];
            if let Some(path) = parse_osc7_uri(payload) {
                accepted.push(path);
            }
            cursor = abs_end + terminator_len;
        }
        self.pending.drain(..cursor);
        if self.pending.len() > MAX_SEQUENCE_BYTES {
            self.pending.clear();
        }
        accepted
    }
}

impl Default for Osc7Parser {
    fn default() -> Self {
        Self::new()
    }
}

/// Locate the next `ESC ] 7 ; … terminator` in `buf`.
///
/// Returns `(payload_start, payload_end, terminator_len)` relative to
/// `buf`. Returns `None` when no *complete* sequence is present (an opening
/// without a terminator yet means "wait for more bytes", not "reject").
fn find_sequence(buf: &[u8]) -> Option<(usize, usize, usize)> {
    let mut i = 0;
    while i + 4 < buf.len() {
        if buf[i] == 0x1b && buf[i + 1] == b']' && buf[i + 2] == b'7' && buf[i + 3] == b';' {
            let payload_start = i + 4;
            let mut j = payload_start;
            while j < buf.len() {
                if buf[j] == 0x07 {
                    return Some((payload_start, j, 1));
                }
                if buf[j] == 0x1b && j + 1 < buf.len() && buf[j + 1] == b'\\' {
                    return Some((payload_start, j, 2));
                }
                // A bare ESC that does not start ST aborts this candidate:
                // the sequence is malformed, skip past it.
                if buf[j] == 0x1b {
                    break;
                }
                j += 1;
            }
            // No terminator yet (or malformed): if malformed, resume the
            // search after the opening; if merely incomplete, stop.
            if j >= buf.len() {
                return None;
            }
            i = j + 1;
            continue;
        }
        i += 1;
    }
    None
}

/// Validate an OSC 7 payload as a local directory.
///
/// Accepts `file://<path>` (empty host) and `file://localhost<path>`.
/// Rejects remote hosts, non-`file` schemes, relative paths, NUL bytes,
/// and invalid percent escapes. Returns the decoded absolute path.
pub fn parse_osc7_uri(payload: &[u8]) -> Option<PathBuf> {
    let text = std::str::from_utf8(payload).ok()?.trim();
    let rest = text.strip_prefix("file://")?;
    let (host, path) = match rest.find('/') {
        Some(idx) => (&rest[..idx], &rest[idx..]),
        None => (rest, ""),
    };
    if !host.is_empty() && host != "localhost" {
        return None;
    }
    if path.is_empty() {
        return None;
    }
    let decoded = percent_decode(path)?;
    if !decoded.starts_with('/') || decoded.contains('\0') {
        return None;
    }
    Some(PathBuf::from(decoded))
}

/// Strict `%XX` decoder over ASCII bytes. Any malformed escape, bare `%`,
/// or non-ASCII byte rejects the whole input.
fn percent_decode(input: &str) -> Option<String> {
    let bytes = input.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        match bytes[i] {
            b'%' => {
                if i + 2 >= bytes.len() {
                    return None;
                }
                let hi = hex_val(bytes[i + 1])?;
                let lo = hex_val(bytes[i + 2])?;
                out.push(hi << 4 | lo);
                i += 3;
            }
            0x00..=0x1f | 0x7f => return None,
            b => {
                if b >= 0x80 {
                    // Raw UTF-8 bytes pass through; validity is checked below.
                    out.push(b);
                } else {
                    out.push(b);
                }
                i += 1;
            }
        }
    }
    String::from_utf8(out).ok()
}

fn hex_val(b: u8) -> Option<u8> {
    match b {
        b'0'..=b'9' => Some(b - b'0'),
        b'a'..=b'f' => Some(b - b'a' + 10),
        b'A'..=b'F' => Some(b - b'A' + 10),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn feed_all(chunks: &[&[u8]]) -> Vec<PathBuf> {
        let mut parser = Osc7Parser::new();
        let mut out = Vec::new();
        for chunk in chunks {
            out.extend(parser.feed(chunk));
        }
        out
    }

    #[test]
    fn accepts_empty_host_bel_terminated() {
        let got = feed_all(&[b"\x1b]7;file:///home/fachri\x07"]);
        assert_eq!(got, vec![PathBuf::from("/home/fachri")]);
    }

    #[test]
    fn accepts_localhost_st_terminated() {
        let got = feed_all(&[b"\x1b]7;file://localhost/tmp/x\x1b\\"]);
        assert_eq!(got, vec![PathBuf::from("/tmp/x")]);
    }

    #[test]
    fn percent_decodes_spaces() {
        let got = feed_all(&[b"\x1b]7;file:///home/my%20docs\x07"]);
        assert_eq!(got, vec![PathBuf::from("/home/my docs")]);
    }

    #[test]
    fn rejects_remote_host() {
        let got = feed_all(&[b"\x1b]7;file://server/home/u\x07"]);
        assert!(got.is_empty(), "remote host must be rejected: {got:?}");
    }

    #[test]
    fn rejects_non_file_scheme() {
        let got = feed_all(&[b"\x1b]7;https://example.com/x\x07"]);
        assert!(got.is_empty());
    }

    #[test]
    fn rejects_relative_path() {
        let got = feed_all(&[b"\x1b]7;file://home/relative\x07"]);
        assert!(got.is_empty(), "relative path must be rejected: {got:?}");
    }

    #[test]
    fn rejects_bad_percent_escape() {
        for payload in [
            b"file:///home/a%2\x07".as_slice(),
            b"file:///home/a%zz\x07".as_slice(),
        ] {
            let mut seq = b"\x1b]7;".to_vec();
            seq.extend_from_slice(payload);
            let got = feed_all(&[&seq]);
            assert!(got.is_empty(), "bad escape must be rejected: {payload:?}");
        }
    }

    #[test]
    fn reassembles_fragmented_sequence() {
        let got = feed_all(&[b"\x1b]7;file://", b"/home/fac", b"hri\x07"]);
        assert_eq!(got, vec![PathBuf::from("/home/fachri")]);
    }

    #[test]
    fn waits_for_terminator_across_reads() {
        let mut parser = Osc7Parser::new();
        assert!(parser.feed(b"\x1b]7;file:///home/x").is_empty());
        let got = parser.feed(b"\x07");
        assert_eq!(got, vec![PathBuf::from("/home/x")]);
    }

    #[test]
    fn ignores_other_osc_sequences() {
        // OSC 0 title + OSC 7 directory in one stream.
        let got = feed_all(&[b"\x1b]0;mytitle\x07prompt\x1b]7;file:///tmp\x07"]);
        assert_eq!(got, vec![PathBuf::from("/tmp")]);
    }

    #[test]
    fn multiple_sequences_in_one_read() {
        let got = feed_all(&[b"a\x1b]7;file:///one\x07b\x1b]7;file:///two\x07"]);
        assert_eq!(got, vec![PathBuf::from("/one"), PathBuf::from("/two")]);
    }

    #[test]
    fn plain_output_yields_nothing() {
        let got = feed_all(&[b"hello world\r\n$ "]);
        assert!(got.is_empty());
    }
}
