//! M10 scrollback recorder and replay (terminal side).
//!
//! The recorder observes ordered PTY output chunks and resize events and
//! keeps only what is needed to deterministically rebuild the *main-screen*
//! history. Bytes observed while the alternate screen is active are dropped:
//! any chunk where the engine was in alt screen before the advance, or is in
//! alt screen after it, never enters the record. The pre-alt main-screen
//! scrollback is preserved as-is.
//!
//! Recording is a hot-path operation (called from the PTY reader pump): it
//! performs only bounded memory copies and counter updates. Compression,
//! encryption, keyring access, and disk I/O must never happen here; the
//! desktop owner drains snapshots and persists them on a background writer.

use std::collections::VecDeque;

/// One recorded event. Mirrors the persistence framing so the terminal crate
/// stays free of crypto dependencies.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RecordedEvent {
    Output(Vec<u8>),
    Resize { cols: u16, rows: u16 },
}

impl RecordedEvent {
    #[must_use]
    pub fn encoded_len(&self) -> usize {
        match self {
            Self::Output(bytes) => bytes.len(),
            Self::Resize { .. } => 4,
        }
    }
}

/// Recorder bounds. Defaults mirror the M10 persistence ceilings at a scale
/// appropriate for the hot path; the persistence layer re-enforces its own
/// caps before allocation.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct RecorderLimits {
    pub max_frame_bytes: usize,
    pub max_events: usize,
    pub max_bytes: usize,
    pub max_lines: usize,
}

impl Default for RecorderLimits {
    fn default() -> Self {
        Self {
            max_frame_bytes: 64 * 1024,
            max_events: 4096,
            max_bytes: 1024 * 1024,
            max_lines: 10_000,
        }
    }
}

/// Hot-path scrollback recorder.
#[derive(Debug)]
pub struct HistoryRecorder {
    enabled: bool,
    paused: bool,
    events: VecDeque<RecordedEvent>,
    total_bytes: usize,
    total_lines: usize,
    in_alt_screen: bool,
    limits: RecorderLimits,
    /// Bumped on every mutation (record, trim, clear). Lets persistence
    /// writers detect new content without comparing snapshots.
    version: u64,
}

impl HistoryRecorder {
    #[must_use]
    pub fn new(limits: RecorderLimits) -> Self {
        Self {
            enabled: false,
            paused: false,
            events: VecDeque::new(),
            total_bytes: 0,
            total_lines: 0,
            in_alt_screen: false,
            limits,
            version: 0,
        }
    }

    /// Monotonic mutation counter for dirty tracking.
    #[must_use]
    pub fn version(&self) -> u64 {
        self.version
    }

    #[must_use]
    pub fn is_enabled(&self) -> bool {
        self.enabled
    }

    pub fn set_enabled(&mut self, enabled: bool) {
        self.enabled = enabled;
        if !enabled {
            self.clear();
        }
    }

    pub fn set_paused(&mut self, paused: bool) {
        self.paused = paused;
    }

    /// Track alternate-screen state. Called by the session pump around every
    /// engine advance. Entering alt preserves existing main-screen events;
    /// leaving alt resumes capture with the next chunk.
    pub fn set_alt_screen(&mut self, in_alt: bool) {
        self.in_alt_screen = in_alt;
    }

    #[must_use]
    pub fn event_count(&self) -> usize {
        self.events.len()
    }

    #[must_use]
    pub fn total_bytes(&self) -> usize {
        self.total_bytes
    }

    /// Observe one PTY output chunk. `was_alt` is the engine alt state before
    /// the advance, `is_alt` after it. The chunk is dropped when either is
    /// true, so alt-screen bytes (including the enter/exit sequences
    /// themselves) never enter the record.
    pub fn observe_output(&mut self, chunk: &[u8], was_alt: bool, is_alt: bool) {
        if !self.enabled || self.paused || chunk.is_empty() {
            return;
        }
        if was_alt || is_alt || self.in_alt_screen {
            return;
        }
        // Split oversized writes into bounded frames; each frame is a
        // complete record so trimming never leaves partial data.
        for piece in chunk.chunks(self.limits.max_frame_bytes) {
            if piece.is_empty() {
                continue;
            }
            let lines = piece.iter().filter(|b| **b == b'\n').count();
            self.push_event(RecordedEvent::Output(piece.to_vec()), lines);
        }
    }

    /// Observe a grid resize that happened while the main screen was active.
    /// Resizes inside alt-screen periods are skipped by the caller.
    pub fn observe_resize(&mut self, cols: u16, rows: u16) {
        if !self.enabled || self.paused || self.in_alt_screen {
            return;
        }
        let cols = cols.max(2);
        let rows = rows.max(1);
        // Coalesce consecutive duplicate resizes.
        if let Some(RecordedEvent::Resize { cols: c, rows: r }) = self.events.back()
            && *c == cols
            && *r == rows
        {
            return;
        }
        self.push_event(RecordedEvent::Resize { cols, rows }, 0);
    }

    fn push_event(&mut self, event: RecordedEvent, lines: usize) {
        let cost = 5 + event.encoded_len();
        self.events.push_back(event);
        self.total_bytes += cost;
        self.total_lines += lines;
        self.version = self.version.wrapping_add(1);
        self.trim();
    }

    /// Drop oldest complete events first until all ceilings hold.
    fn trim(&mut self) {
        while self.events.len() > self.limits.max_events
            || self.total_bytes > self.limits.max_bytes
            || self.total_lines > self.limits.max_lines
        {
            let Some(oldest) = self.events.pop_front() else {
                break;
            };
            self.total_bytes = self.total_bytes.saturating_sub(5 + oldest.encoded_len());
            if let RecordedEvent::Output(bytes) = &oldest {
                let lines = bytes.iter().filter(|b| **b == b'\n').count();
                self.total_lines = self.total_lines.saturating_sub(lines);
            }
            // If a single huge chunk still exceeds the caps on its own, drop
            // it entirely rather than keeping a partial record.
            if self.events.is_empty() {
                self.total_bytes = 0;
                self.total_lines = 0;
                break;
            }
        }
    }

    /// Snapshot the current record for the background persistence writer.
    #[must_use]
    pub fn snapshot(&self) -> Vec<RecordedEvent> {
        self.events.iter().cloned().collect()
    }

    /// Seed the recorder with restored events (oldest first). Caps are
    /// enforced by dropping the oldest complete events, exactly like live
    /// trimming, so a restored record always fits the writer bounds.
    pub fn seed(&mut self, events: &[RecordedEvent]) {
        for event in events {
            let lines = match event {
                RecordedEvent::Output(bytes) => bytes.iter().filter(|b| **b == b'\n').count(),
                RecordedEvent::Resize { .. } => 0,
            };
            let cost = 5 + event.encoded_len();
            self.events.push_back(event.clone());
            self.total_bytes += cost;
            self.total_lines += lines;
        }
        self.version = self.version.wrapping_add(1);
        self.trim();
    }

    pub fn clear(&mut self) {
        self.events.clear();
        self.total_bytes = 0;
        self.total_lines = 0;
        self.version = self.version.wrapping_add(1);
    }
}

impl Default for HistoryRecorder {
    fn default() -> Self {
        Self::new(RecorderLimits::default())
    }
}

/// Drop the trailing partial line (bytes after the final `\n`) from a
/// restored record. A shell at rest ends its record with the rendered
/// prompt, which carries no trailing newline; replaying it verbatim stacks
/// a stale prompt above every fresh shell, accumulating one duplicate per
/// restart cycle. Torn mid-command tails are dropped with it — they were
/// never completed output, and the archive on disk retains full bytes, so
/// nothing is lost permanently.
///
/// When an idle prompt tail was dropped, exactly one trailing blank line is
/// also dropped if the kept prefix ends with one. Prompts with normal
/// leading-newline padding (the blank separator also seen on plain `foot`
/// terminals) otherwise re-add their padding on every fresh shell, stacking
/// one more empty line per restart cycle. Only one break is removed and
/// only when the bytes between the last two newlines are zero-width
/// (`\r` and terminal escape sequences such as the OSC 133 lifecycle
/// markers, which carry no `\n` and render nothing); real content,
/// including intentional blank output lines, is preserved, so the seam is
/// idempotent across restarts. A record holding nothing visible at all
/// collapses to resizes only: blank-only scrollback carries no information
/// and the fresh shell re-emits its own padding.
///
/// Resize events are always kept (grid fidelity matters and they are tiny).
/// A record with no newline at all restores to just its resizes: the fresh
/// prompt then stands alone instead of doubling. Truncation always lands on
/// a `\n` boundary (ASCII), so wide/combining sequences are never split.
#[must_use]
pub fn strip_trailing_partial_line(events: &[RecordedEvent]) -> Vec<RecordedEvent> {
    // Find the last Output event holding a `\n`; everything output after
    // that newline is the partial tail.
    let mut last_newline: Option<(usize, usize)> = None;
    for (index, event) in events.iter().enumerate() {
        if let RecordedEvent::Output(bytes) = event
            && let Some(pos) = bytes.iter().rposition(|b| *b == b'\n')
        {
            last_newline = Some((index, pos));
        }
    }
    let Some((index, pos)) = last_newline else {
        // No complete line anywhere: keep resizes only.
        return events
            .iter()
            .filter(|event| matches!(event, RecordedEvent::Resize { .. }))
            .cloned()
            .collect();
    };
    // Did an idle prompt (or torn tail) actually exist past the final
    // newline? Padding removal below is only valid with that evidence: a
    // record already ending at a newline has no prompt to attribute a
    // trailing blank line to.
    let mut tail_dropped = false;
    if let RecordedEvent::Output(bytes) = &events[index] {
        tail_dropped = bytes.len() > pos + 1;
    }
    for event in &events[index + 1..] {
        if let RecordedEvent::Output(bytes) = event
            && !bytes.is_empty()
        {
            tail_dropped = true;
            break;
        }
    }
    // Everything up to and including the final newline is complete history.
    // Later output holds no newline by construction, so only later resizes
    // survive.
    let mut stripped: Vec<RecordedEvent> = events[..index].to_vec();
    if let RecordedEvent::Output(bytes) = &events[index] {
        stripped.push(RecordedEvent::Output(bytes[..=pos].to_vec()));
    }
    stripped.extend(
        events[index + 1..]
            .iter()
            .filter(|event| matches!(event, RecordedEvent::Resize { .. }))
            .cloned(),
    );
    if tail_dropped {
        strip_one_prompt_padding_blank(&mut stripped);
        collapse_blank_only_record(&mut stripped);
    }
    stripped
}

/// Flat index of every `Output` byte: `(event_index, byte_offset, byte)`.
/// `Resize` events carry no bytes and keep their order separately.
fn flat_output_bytes(events: &[RecordedEvent]) -> Vec<(usize, usize, u8)> {
    let mut flat = Vec::new();
    for (event_index, event) in events.iter().enumerate() {
        if let RecordedEvent::Output(bytes) = event {
            for (byte_offset, byte) in bytes.iter().enumerate() {
                flat.push((event_index, byte_offset, *byte));
            }
        }
    }
    flat
}

/// Skip one terminal escape sequence starting at `start` (which must hold
/// `ESC`). OSC (`ESC ] … BEL/ST`), CSI (`ESC [ … final`), and short
/// two/three-byte sequences are zero-width for blank-line detection.
/// Returns the first index past the sequence, or `None` when malformed or
/// truncated (conservative: the span is then not blank).
fn skip_escape_sequence(bytes: &[u8], start: usize) -> Option<usize> {
    debug_assert_eq!(bytes.get(start), Some(&0x1b));
    let introduced = *bytes.get(start + 1)?;
    match introduced {
        // OSC: ESC ] … BEL, or ESC ] … ESC \ (ST). Never spans a newline.
        b']' => {
            let mut i = start + 2;
            loop {
                match bytes.get(i) {
                    Some(0x07) => return Some(i + 1),
                    Some(0x1b) if bytes.get(i + 1) == Some(&b'\\') => return Some(i + 2),
                    Some(b'\n') | None => return None,
                    _ => i += 1,
                }
            }
        }
        // CSI: ESC [ params intermediates final.
        b'[' => {
            let mut i = start + 2;
            while matches!(bytes.get(i), Some(0x30..=0x3f)) {
                i += 1;
            }
            while matches!(bytes.get(i), Some(0x20..=0x2f)) {
                i += 1;
            }
            match bytes.get(i) {
                Some(0x40..=0x7e) => Some(i + 1),
                _ => None,
            }
        }
        // Character-set / single-shift introducers take one more byte.
        b'(' | b')' | b'#' | b'%' => {
            if bytes.get(start + 2).is_some() {
                Some(start + 3)
            } else {
                None
            }
        }
        // Any other ESC + single byte (e.g. `M`, `=`, `c`).
        _ => Some(start + 2),
    }
}

/// True when `bytes` renders nothing: only carriage returns and complete
/// terminal escape sequences. Any printable, UTF-8 continuation, tab,
/// lone BEL, or malformed/truncated escape means visible (or unknown)
/// content — never treated as blank.
fn is_zero_width_span(bytes: &[u8]) -> bool {
    let mut i = 0;
    while i < bytes.len() {
        match bytes[i] {
            b'\r' => i += 1,
            0x1b => match skip_escape_sequence(bytes, i) {
                Some(next) => i = next,
                None => return false,
            },
            _ => return false,
        }
    }
    true
}

/// Remove flat `Output`-byte indices `remove` from `events`, dropping
/// `Output` events left empty. `Resize` events are untouched and keep
/// their positions.
fn remove_flat_output_range(events: &mut Vec<RecordedEvent>, remove: &[bool]) {
    let mut flat_cursor = 0;
    let mut kept: Vec<RecordedEvent> = Vec::with_capacity(events.len());
    for event in events.drain(..) {
        match event {
            RecordedEvent::Output(bytes) => {
                let mut remaining = Vec::with_capacity(bytes.len());
                for byte in bytes {
                    if !remove.get(flat_cursor).copied().unwrap_or(false) {
                        remaining.push(byte);
                    }
                    flat_cursor += 1;
                }
                if !remaining.is_empty() {
                    kept.push(RecordedEvent::Output(remaining));
                }
            }
            resize @ RecordedEvent::Resize { .. } => kept.push(resize),
        }
    }
    *events = kept;
}

/// Drop one trailing prompt-padding blank line: when the `Output` byte
/// stream ends with `…\n <zero-width> \n`, the final line break (plus the
/// zero-width span before it) is the idle prompt's leading padding, which
/// the fresh shell re-emits. Removing the whole span keeps the record
/// clean of stale lifecycle markers; the span provably renders nothing.
fn strip_one_prompt_padding_blank(stripped: &mut Vec<RecordedEvent>) {
    let flat = flat_output_bytes(stripped);
    let mut last = None;
    let mut previous = None;
    for (flat_index, (_, _, byte)) in flat.iter().enumerate() {
        if *byte == b'\n' {
            previous = last;
            last = Some(flat_index);
        }
    }
    let (Some(second), Some(first)) = (last, previous) else {
        return;
    };
    let span: Vec<u8> = flat[first + 1..second]
        .iter()
        .map(|(_, _, byte)| *byte)
        .collect();
    if !is_zero_width_span(&span) {
        return;
    }
    let mut remove = vec![false; flat.len()];
    for slot in remove.iter_mut().take(second + 1).skip(first + 1) {
        *slot = true;
    }
    remove_flat_output_range(stripped, &remove);
}

/// Collapse a record whose `Output` bytes render nothing (only line breaks
/// and escape sequences) down to resizes. Blank-only scrollback carries no
/// information and the fresh shell supplies its own padding; without this,
/// an empty-history restore stabilizes with one stray blank line.
fn collapse_blank_only_record(stripped: &mut Vec<RecordedEvent>) {
    let flat = flat_output_bytes(stripped);
    if flat.is_empty() {
        return;
    }
    let all: Vec<u8> = flat.iter().map(|(_, _, byte)| *byte).collect();
    let visible = all.iter().any(|byte| *byte != b'\n' && *byte != b'\r');
    if !visible {
        // Only breaks (no escapes at all): nothing visible, drop outputs.
        stripped.retain(|event| matches!(event, RecordedEvent::Resize { .. }));
        return;
    }
    // Mixed breaks and other bytes: blank only if every non-break run is
    // zero-width escapes. Walk line by line for a precise verdict.
    let mut line_start = 0;
    for (i, byte) in all.iter().enumerate() {
        if *byte == b'\n' {
            if !is_zero_width_span(&all[line_start..i]) {
                return;
            }
            line_start = i + 1;
        }
    }
    if line_start < all.len() && !is_zero_width_span(&all[line_start..]) {
        return;
    }
    stripped.retain(|event| matches!(event, RecordedEvent::Resize { .. }));
}

/// Replay ordered events into a fresh engine. The caller creates the engine
/// with the pane's current grid size, then calls this before attaching the
/// new PTY shell. Output chunks that would enter the alternate screen are
/// skipped defensively (the recorder already excludes them); resize events
/// are applied in order.
///
/// Callers restoring persisted history should pass events through
/// [`strip_trailing_partial_line`] first so a stale idle prompt is not
/// replayed above the fresh shell.
pub fn replay_into<E: crate::TerminalEngine>(engine: &mut E, events: &[RecordedEvent]) {
    for event in events {
        match event {
            RecordedEvent::Output(bytes) => {
                let was_alt = engine.is_alt_screen();
                engine.advance_output(bytes);
                // Defensive: if a recorded chunk somehow enters alt screen,
                // leave it immediately so replay stays on the main screen.
                if !was_alt && engine.is_alt_screen() {
                    engine.advance_output(b"\x1b[?1049l");
                }
            }
            RecordedEvent::Resize { cols, rows } => {
                engine.resize(*cols, *rows);
            }
        }
    }
}

/// Keeps restored scrollback visible across a fresh Windows console.
/// ConPTY erases the display at startup, then the first pane resize homes
/// the cursor and redraws the prompt over that screen. While armed,
/// erase-display is dropped and absolute cursor positions are shifted below
/// the restored rows. The filter disarms after that resize redraw shows the
/// cursor, or when the user types, so a later `clear` still works.
#[derive(Debug, Default)]
pub struct StartupClearFilter {
    active: bool,
    /// 0-based grid row where the fresh shell may start writing.
    origin_row: u16,
    saw_resize_report: bool,
    pending: Vec<u8>,
}

impl StartupClearFilter {
    pub fn arm(&mut self, origin_row: u16) {
        self.active = true;
        self.origin_row = origin_row;
        self.saw_resize_report = false;
        self.pending.clear();
    }

    pub fn disarm(&mut self) {
        self.active = false;
        self.pending.clear();
    }

    #[must_use]
    pub fn apply<'a>(&mut self, input: &'a [u8]) -> std::borrow::Cow<'a, [u8]> {
        if !self.active && self.pending.is_empty() {
            return std::borrow::Cow::Borrowed(input);
        }
        let mut data = std::mem::take(&mut self.pending);
        data.extend_from_slice(input);
        let mut out = Vec::with_capacity(data.len());
        let mut index = 0;
        while index < data.len() {
            if !self.active {
                out.extend_from_slice(&data[index..]);
                break;
            }
            if data[index] != 0x1b {
                out.push(data[index]);
                index += 1;
                continue;
            }
            match self.escape_action(&data[index..]) {
                EscapeAction::NeedMore => {
                    let rest = &data[index..];
                    if rest.len() > 512 {
                        self.disarm();
                        out.extend_from_slice(rest);
                    } else {
                        self.pending = rest.to_vec();
                    }
                    break;
                }
                EscapeAction::Drop(len) => index += len,
                EscapeAction::Keep(len) => {
                    out.extend_from_slice(&data[index..index + len]);
                    index += len;
                }
                EscapeAction::Replace { skip, bytes } => {
                    out.extend_from_slice(&bytes);
                    index += skip;
                }
            }
        }
        std::borrow::Cow::Owned(out)
    }
}

enum EscapeAction {
    NeedMore,
    Drop(usize),
    Keep(usize),
    Replace { skip: usize, bytes: Vec<u8> },
}

impl StartupClearFilter {
    fn escape_action(&mut self, bytes: &[u8]) -> EscapeAction {
        if bytes.len() < 2 {
            return EscapeAction::NeedMore;
        }
        match bytes[1] {
            b'[' => self.csi_action(&bytes[2..]),
            b']' => osc_action(&bytes[2..]),
            _ => EscapeAction::Keep(2),
        }
    }

    fn csi_action(&mut self, body: &[u8]) -> EscapeAction {
        let mut index = 0;
        while index < body.len() {
            let byte = body[index];
            if (0x40..=0x7e).contains(&byte) {
                let params = &body[..index];
                let param_end = params
                    .iter()
                    .position(|value| (0x20..0x30).contains(value))
                    .unwrap_or(params.len());
                let params = &params[..param_end];
                let len = index + 3;
                if byte == b't' && first_csi_param(params) == Some(8) {
                    self.saw_resize_report = true;
                }
                if self.saw_resize_report && params == b"?25" && byte == b'h' {
                    self.disarm();
                }
                if erase_display(params, byte) {
                    return EscapeAction::Drop(len);
                }
                if let Some(bytes) = self.shifted_cursor(params, byte) {
                    return EscapeAction::Replace { skip: len, bytes };
                }
                return EscapeAction::Keep(len);
            }
            if !(0x20..=0x3f).contains(&byte) || index > 64 {
                return EscapeAction::Keep(2);
            }
            index += 1;
        }
        EscapeAction::NeedMore
    }

    fn shifted_cursor(&self, params: &[u8], final_byte: u8) -> Option<Vec<u8>> {
        if !matches!(final_byte, b'H' | b'f')
            || !params
                .iter()
                .all(|byte| byte.is_ascii_digit() || *byte == b';')
        {
            return None;
        }
        let mut parts = params.split(|byte| *byte == b';');
        let row = csi_number(parts.next().unwrap_or(b"")).unwrap_or(1);
        let col = csi_number(parts.next().unwrap_or(b"")).unwrap_or(1);
        let mapped = u32::from(self.origin_row) + u32::from(row);
        let mapped = u16::try_from(mapped).unwrap_or(u16::MAX);
        Some(format!("\u{1b}[{mapped};{col}{final}", final = final_byte as char).into_bytes())
    }
}

fn osc_action(body: &[u8]) -> EscapeAction {
    let mut index = 0;
    while index < body.len() {
        if body[index] == 0x07 {
            return EscapeAction::Keep(index + 3);
        }
        if body[index] == 0x1b {
            return if body.get(index + 1) == Some(&b'\\') {
                EscapeAction::Keep(index + 4)
            } else if index + 1 >= body.len() {
                EscapeAction::NeedMore
            } else {
                EscapeAction::Keep(index + 2)
            };
        }
        index += 1;
    }
    EscapeAction::NeedMore
}

fn erase_display(params: &[u8], final_byte: u8) -> bool {
    final_byte == b'J'
        && params
            .iter()
            .all(|byte| byte.is_ascii_digit() || *byte == b';')
        && first_csi_param(params).is_some_and(|value| value == 2 || value == 3)
}

fn first_csi_param(params: &[u8]) -> Option<u16> {
    csi_number(params.split(|byte| *byte == b';').next()?)
}

fn csi_number(bytes: &[u8]) -> Option<u16> {
    if bytes.is_empty() {
        return None;
    }
    std::str::from_utf8(bytes).ok()?.parse().ok()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{AlacrittyEngine, TerminalEngine};

    fn enabled_recorder() -> HistoryRecorder {
        let mut recorder = HistoryRecorder::new(RecorderLimits::default());
        recorder.set_enabled(true);
        recorder
    }

    #[test]
    fn disabled_recorder_keeps_nothing() {
        let mut recorder = HistoryRecorder::default();
        recorder.observe_output(b"hello", false, false);
        recorder.observe_resize(60, 20);
        assert_eq!(recorder.event_count(), 0);
    }

    #[test]
    fn paused_recorder_keeps_nothing_until_resumed() {
        let mut recorder = enabled_recorder();
        recorder.set_paused(true);
        recorder.observe_output(b"hello", false, false);
        assert_eq!(recorder.event_count(), 0);
        recorder.set_paused(false);
        recorder.observe_output(b"hello", false, false);
        assert_eq!(recorder.event_count(), 1);
    }

    #[test]
    fn alt_screen_chunks_are_dropped_and_main_history_preserved() {
        let mut recorder = enabled_recorder();
        recorder.observe_output(b"MAIN-CONTENT\r\n", false, false);
        // Chunk that enters alt: dropped (is_alt after).
        recorder.observe_output(b"\x1b[?1049h", false, true);
        recorder.set_alt_screen(true);
        // Bytes inside alt: dropped.
        recorder.observe_output(b"ALT-CONTENT\r\n", true, true);
        // Chunk that exits alt: dropped (was_alt before).
        recorder.observe_output(b"\x1b[?1049l", true, false);
        recorder.set_alt_screen(false);
        recorder.observe_output(b"AFTER-ALT\r\n", false, false);

        let snapshot = recorder.snapshot();
        let combined: Vec<u8> = snapshot
            .iter()
            .filter_map(|event| match event {
                RecordedEvent::Output(bytes) => Some(bytes.clone()),
                RecordedEvent::Resize { .. } => None,
            })
            .flatten()
            .collect();
        let text = String::from_utf8_lossy(&combined);
        assert!(
            text.contains("MAIN-CONTENT"),
            "keeps pre-alt history: {text:?}"
        );
        assert!(text.contains("AFTER-ALT"), "resumes after alt: {text:?}");
        assert!(
            !text.contains("ALT-CONTENT"),
            "never records alt bytes: {text:?}"
        );
        assert!(
            !text.contains("1049"),
            "enter/exit sequences dropped: {text:?}"
        );
    }

    #[test]
    fn resize_while_alt_is_skipped_but_main_resizes_kept() {
        let mut recorder = enabled_recorder();
        recorder.observe_resize(80, 24);
        recorder.set_alt_screen(true);
        recorder.observe_resize(100, 40);
        recorder.set_alt_screen(false);
        recorder.observe_resize(60, 20);
        let snapshot = recorder.snapshot();
        assert_eq!(snapshot.len(), 2);
        assert!(snapshot.iter().all(|event| *event
            != RecordedEvent::Resize {
                cols: 100,
                rows: 40
            }));
    }

    #[test]
    fn recorder_trims_oldest_first_under_caps() {
        let limits = RecorderLimits {
            max_frame_bytes: 1024,
            max_events: 4,
            max_bytes: 1024 * 1024,
            max_lines: 2,
        };
        let mut recorder = HistoryRecorder::new(limits);
        recorder.set_enabled(true);
        for index in 0..6 {
            recorder.observe_output(format!("line{index}\n").as_bytes(), false, false);
        }
        assert!(recorder.event_count() <= 4);
        let snapshot = recorder.snapshot();
        let text: Vec<u8> = snapshot
            .iter()
            .filter_map(|event| match event {
                RecordedEvent::Output(bytes) => Some(bytes.clone()),
                RecordedEvent::Resize { .. } => None,
            })
            .flatten()
            .collect();
        let text = String::from_utf8_lossy(&text);
        assert!(!text.contains("line0"), "oldest dropped whole: {text:?}");
        assert!(text.contains("line5"), "newest kept: {text:?}");
    }

    #[test]
    fn oversized_writes_split_into_bounded_frames() {
        let limits = RecorderLimits {
            max_frame_bytes: 8,
            ..RecorderLimits::default()
        };
        let mut recorder = HistoryRecorder::new(limits);
        recorder.set_enabled(true);
        recorder.observe_output(b"0123456789abcdef", false, false);
        assert_eq!(recorder.event_count(), 2);
        for event in recorder.snapshot() {
            assert!(event.encoded_len() <= 8);
        }
    }

    #[test]
    fn replay_reproduces_main_screen_with_resize_colors_and_unicode() {
        let chunks: Vec<Vec<u8>> = vec![
            b"\x1b[1;31mred\x1b[0m hello\r\n".to_vec(),
            "wide \u{4f60}\u{597d} + combining e\u{301}\r\n"
                .as_bytes()
                .to_vec(),
            b"long soft-wrapped line 0123456789 ".repeat(4),
            b"\x1b[38;5;196m256color\x1b[0m\r\n".to_vec(),
        ];
        let mut source = AlacrittyEngine::new(80, 24);
        let mut recorder = enabled_recorder();
        for chunk in &chunks {
            source.advance_output(chunk);
            recorder.observe_output(chunk, false, source.is_alt_screen());
        }
        source.resize(60, 20);
        recorder.observe_resize(60, 20);
        let tail = b"after-resize line\r\n";
        source.advance_output(tail);
        recorder.observe_output(tail, false, source.is_alt_screen());
        // Alt excursion after capture: must not affect the record.
        source.advance_output(b"\x1b[?1049hALT\r\n\x1b[?1049l");
        let before = source.read_visible_text(100, 200);

        let mut restored = AlacrittyEngine::new(80, 24);
        replay_into(&mut restored, &recorder.snapshot());
        let after = restored.read_visible_text(100, 200);
        assert_eq!(before, after, "replay must reproduce the main screen");
    }

    #[test]
    fn seed_loads_restored_prefix_within_caps() {
        let limits = RecorderLimits {
            max_frame_bytes: 1024,
            max_events: 4,
            max_bytes: 1024 * 1024,
            max_lines: 100,
        };
        let mut recorder = HistoryRecorder::new(limits);
        recorder.set_enabled(true);
        let events: Vec<RecordedEvent> = (0..10)
            .map(|index| RecordedEvent::Output(format!("restored-{index}\n").into_bytes()))
            .collect();
        recorder.seed(&events);
        let snapshot = recorder.snapshot();
        assert!(snapshot.len() <= 4, "seed trims oldest first");
        let text: Vec<u8> = snapshot
            .iter()
            .filter_map(|event| match event {
                RecordedEvent::Output(bytes) => Some(bytes.clone()),
                RecordedEvent::Resize { .. } => None,
            })
            .flatten()
            .collect();
        let text = String::from_utf8_lossy(&text);
        assert!(
            text.contains("restored-9"),
            "newest restored kept: {text:?}"
        );
        assert!(
            !text.contains("restored-0"),
            "oldest restored dropped: {text:?}"
        );
    }

    #[test]
    fn replay_flood_beyond_caps_keeps_valid_newest_history() {
        let limits = RecorderLimits {
            max_frame_bytes: 64,
            max_events: 32,
            max_bytes: 2048,
            max_lines: 20,
        };
        let mut recorder = HistoryRecorder::new(limits);
        recorder.set_enabled(true);
        let mut source = AlacrittyEngine::new(40, 10);
        for index in 0..200 {
            let line = format!("flood-line-{index:04}\r\n");
            source.advance_output(line.as_bytes());
            recorder.observe_output(line.as_bytes(), false, source.is_alt_screen());
        }
        assert!(recorder.event_count() <= 32);
        let mut restored = AlacrittyEngine::new(40, 10);
        replay_into(&mut restored, &recorder.snapshot());
        let text = restored.read_visible_text(40, 100);
        assert!(
            text.contains("flood-line-0199"),
            "newest retained: {text:?}"
        );
        assert!(
            !text.contains("flood-line-0000"),
            "oldest dropped: {text:?}"
        );
    }

    fn output(bytes: &[u8]) -> RecordedEvent {
        RecordedEvent::Output(bytes.to_vec())
    }

    fn resize(cols: u16, rows: u16) -> RecordedEvent {
        RecordedEvent::Resize { cols, rows }
    }

    #[test]
    fn strip_drops_trailing_idle_prompt_but_keeps_complete_lines() {
        let events = vec![
            output(b"echo hi\r\n"),
            output(b"hi\r\n"),
            output("❯ ".as_bytes()),
        ];
        let stripped = strip_trailing_partial_line(&events);
        assert_eq!(
            stripped,
            vec![output(b"echo hi\r\n"), output(b"hi\r\n")],
            "stale prompt tail dropped"
        );
    }

    #[test]
    fn strip_keeps_resizes_around_a_dropped_tail() {
        let events = vec![
            output(b"line1\r\n"),
            resize(100, 30),
            output("❯ ".as_bytes()),
            resize(80, 24),
        ];
        let stripped = strip_trailing_partial_line(&events);
        assert_eq!(
            stripped,
            vec![output(b"line1\r\n"), resize(100, 30), resize(80, 24),],
            "resizes survive, prompt tail does not"
        );
    }

    #[test]
    fn strip_truncates_inside_the_last_chunk_after_its_final_newline() {
        let events = vec![output("cmd output\n❯ partial-input".as_bytes())];
        let stripped = strip_trailing_partial_line(&events);
        assert_eq!(stripped, vec![output(b"cmd output\n")]);
    }

    #[test]
    fn strip_leaves_records_ending_at_a_newline_untouched() {
        let events = vec![output(b"line1\r\n"), output(b"line2\r\n")];
        assert_eq!(strip_trailing_partial_line(&events), events);
    }

    #[test]
    fn strip_prompt_only_record_down_to_resizes() {
        let events = vec![output("❯ ".as_bytes()), resize(80, 24)];
        assert_eq!(strip_trailing_partial_line(&events), vec![resize(80, 24)]);
        assert!(strip_trailing_partial_line(&[]).is_empty());
    }

    #[test]
    fn stripped_replay_never_stacks_a_duplicate_prompt() {
        // One full close/reopen cycle: the saved record ends with the idle
        // prompt, the restored view must end at a complete line so the
        // fresh shell's prompt stands alone.
        let saved = vec![
            output(b"echo hi\r\n"),
            output(b"hi\r\n"),
            output("❯ ".as_bytes()),
        ];
        let mut restored = AlacrittyEngine::new(80, 24);
        replay_into(&mut restored, &strip_trailing_partial_line(&saved));
        let text = restored.read_visible_text(24, 80);
        assert!(text.contains("hi"), "complete history replays");
        assert!(
            !text.contains('❯'),
            "no stale prompt above the fresh shell: {text:?}"
        );
    }

    #[test]
    fn strip_drops_one_prompt_padding_blank_after_idle_prompt() {
        // Shells with leading-newline prompt padding (the blank separator
        // also seen on plain foot terminals): the record ends with output,
        // one padding blank, then the idle prompt text. The padding must go
        // with the prompt — the fresh shell re-emits exactly one — or every
        // restart stacks another empty line.
        let events = vec![
            output(b"bash: command not found: ll\r\n"),
            output(b"\r\n"),
            output(b"omaterm main \xe2\x9d\xaf "),
        ];
        assert_eq!(
            strip_trailing_partial_line(&events),
            vec![output(b"bash: command not found: ll\r\n")],
            "prompt padding blank dropped with the idle tail"
        );
    }

    #[test]
    fn strip_drops_padding_blank_with_lifecycle_marker_between() {
        // Our own OSC 133 prompt-ready marker sits between the last output
        // newline and the padding newline; it renders nothing and must not
        // protect the padding blank.
        let marker = b"\x1b]133;A;test-token-12;1\x07";
        let mut padded = b"out\r\n".to_vec();
        padded.extend_from_slice(marker);
        padded.extend_from_slice(b"\r\n");
        let events = vec![output(&padded), output(b"prompt ")];
        assert_eq!(
            strip_trailing_partial_line(&events),
            vec![output(b"out\r\n")],
            "padding break and stale marker span dropped together"
        );
    }

    #[test]
    fn strip_drops_fragmented_padding_blank_across_events() {
        // PTY chunks split anywhere: the padding CRLF may straddle events.
        let events = vec![
            output(b"out\r"),
            output(b"\n"),
            output(b"\r"),
            output(b"\n"),
            output(b"\xe2\x9d\xaf "),
        ];
        assert_eq!(
            strip_trailing_partial_line(&events),
            vec![output(b"out\r"), output(b"\n")],
            "fragmented padding removed, output newline kept"
        );
    }

    #[test]
    fn strip_keeps_intentional_content_blanks() {
        // `printf 'a\n\n'` output plus single padding plus prompt: only the
        // padding goes; the content blank survives.
        let events = vec![
            output(b"a\r\n"),
            output(b"\r\n"),
            output(b"\r\n"),
            output(b"prompt "),
        ];
        assert_eq!(
            strip_trailing_partial_line(&events),
            vec![output(b"a\r\n"), output(b"\r\n")],
            "exactly one trailing blank removed"
        );
    }

    #[test]
    fn strip_keeps_colored_content_before_idle_prompt() {
        // SGR color sequences around real text are visible content: a
        // trailing blank after them is still padding and goes, the colored
        // line itself stays.
        let events = vec![
            output(b"\x1b[31mred\x1b[0m\r\n"),
            output(b"\r\n"),
            output(b"prompt "),
        ];
        assert_eq!(
            strip_trailing_partial_line(&events),
            vec![output(b"\x1b[31mred\x1b[0m\r\n")],
            "colored line kept, padding dropped"
        );
    }

    #[test]
    fn strip_without_padding_blank_is_untouched() {
        // No-leading-newline shells: output newline directly followed by
        // prompt text. There is no padding to drop; the prompt tail still goes.
        let events = vec![output(b"out\r\n"), output(b"prompt ")];
        assert_eq!(
            strip_trailing_partial_line(&events),
            vec![output(b"out\r\n")]
        );
    }

    #[test]
    fn strip_collapses_blank_only_record_to_resizes() {
        // Empty history: nothing but padding plus prompt. Restoring a blank
        // would leave a stray empty row that re-pads every cycle; the fresh
        // shell supplies its own padding on a clean grid.
        let events = vec![
            output(b"\r\n"),
            output(b"\r\n"),
            output(b"prompt "),
            resize(80, 24),
        ];
        assert_eq!(
            strip_trailing_partial_line(&events),
            vec![resize(80, 24)],
            "blank-only record restores to resizes only"
        );
    }

    #[test]
    fn strip_seam_is_idempotent_across_restarts() {
        // Steady state: stripped seed + fresh padding + fresh idle prompt
        // must strip back to the same seed — otherwise blanks accumulate.
        let seed = vec![output(b"bash: command not found: ll\r\n")];
        let mut next_cycle = seed.clone();
        next_cycle.push(output(b"\r\n"));
        next_cycle.push(output(b"omaterm main \xe2\x9d\xaf "));
        assert_eq!(
            strip_trailing_partial_line(&next_cycle),
            seed,
            "second strip returns the seed unchanged"
        );
    }

    #[test]
    fn strip_seam_replay_keeps_exactly_one_blank_separator() {
        // End-to-end seam: replayed seed ends at a complete line, the fresh
        // shell prints its normal padding plus prompt, and the visible grid
        // shows exactly one blank row between output and prompt.
        let saved = vec![
            output(b"bash: command not found: ll\r\n"),
            output(b"\r\n"),
            output(b"omaterm main \xe2\x9d\xaf "),
        ];
        let seed = strip_trailing_partial_line(&saved);
        let mut engine = AlacrittyEngine::new(80, 24);
        replay_into(&mut engine, &seed);
        engine.advance_output(b"\r\nomaterm main \xe2\x9d\xaf ");
        let text = engine.read_visible_text(24, 80);
        let lines: Vec<&str> = text.lines().collect();
        let output_at = lines
            .iter()
            .position(|line| line.contains("command not found"))
            .expect("output replays");
        let prompt_at = lines
            .iter()
            .position(|line| line.contains("omaterm main"))
            .expect("fresh prompt renders");
        assert_eq!(
            prompt_at,
            output_at + 2,
            "exactly one blank row separates output and prompt: {lines:?}"
        );
        // And the newly recorded cycle strips back to the seed.
        let mut recorded = seed.clone();
        recorded.push(output(b"\r\nomaterm main \xe2\x9d\xaf "));
        assert_eq!(strip_trailing_partial_line(&recorded), seed);
    }

    #[test]
    fn startup_clear_keeps_restored_text_and_a_later_clear_still_works() {
        let prefix = b"\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H";
        let mut filter = StartupClearFilter::default();
        filter.arm(1);
        let mut split = Vec::new();
        for byte in prefix {
            split.extend_from_slice(&filter.apply(&[*byte]));
        }
        let whole = {
            let mut once = StartupClearFilter::default();
            once.arm(1);
            once.apply(prefix).into_owned()
        };
        assert_eq!(split, whole);
        assert!(!whole.windows(4).any(|window| window == *b"\x1b[2J"));
        assert!(whole.windows(8).any(|window| window == *b"\x1b[?9001h"));
        assert!(whole.windows(3).any(|window| window == *b"\x1b[m"));

        let mut engine = AlacrittyEngine::new(80, 24);
        replay_into(
            &mut engine,
            &[RecordedEvent::Output(b"restored line\r\n".to_vec())],
        );
        let mut live = StartupClearFilter::default();
        live.arm(1);
        engine.advance_output(&live.apply(b"\x1b[2J\x1b[H"));
        engine.advance_output(&live.apply(b"prompt$ "));
        let text = engine.scrollback_text(40).join("\n");
        assert!(text.contains("restored line"), "{text}");
        assert!(text.contains("prompt$"), "{text}");
        engine.advance_output(&live.apply(b"\x1b[8;30;100t\x1b[Hredraw\x1b[?25h"));
        let settled = engine.scrollback_text(40).join("\n");
        assert!(settled.contains("restored line"), "{settled}");
        let later = live.apply(b"\x1b[2J\x1b[Hafter");
        assert!(
            later.windows(4).any(|window| window == *b"\x1b[2J"),
            "a clear after the prompt must still reach the terminal"
        );
        engine.advance_output(&later);
        let visible = engine.read_visible_text(24, 80);
        assert!(visible.contains("after"), "{visible}");
    }
}
