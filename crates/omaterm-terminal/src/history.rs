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
    stripped
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
}
