//! Terminal find: pure substring search over scrollback dumps.
//!
//! The UI owns the query widget and reveal/highlight; this module owns the
//! matching rules so they are unit-testable without a PTY or a window:
//! - case-insensitive substring (Unicode lowercase on both sides),
//! - hits capped at [`MAX_SEARCH_HITS`], queries at [`MAX_SEARCH_QUERY_CHARS`],
//! - `line` is the absolute dump index (oldest first), `col`/`len` are char
//!   offsets in the dump's line text (same builder rule as
//!   `scrollback_text`: continuations and hidden cells skipped, right-trimmed),
//! - [`char_range_to_cells`] maps a hit back onto viewport cells for painting,
//!   counting lead-cell text so combining sequences stay inside one hit.

use crate::engine::{CellWidth, TerminalRow};

/// Longest searchable query, in chars. Longer input is truncated, never rejected.
pub const MAX_SEARCH_QUERY_CHARS: usize = 128;
/// Most hits returned per scan. The UI reports truncation past this bound.
pub const MAX_SEARCH_HITS: usize = 1000;

/// One substring hit: absolute dump line plus char range within that line.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct SearchHit {
    pub line: usize,
    pub col: usize,
    pub len: usize,
}

/// Case-insensitive substring scan of `lines` for `query`.
///
/// Empty/whitespace-only queries match nothing (an empty box is a prompt,
/// not "everything matches"). Returns at most [`MAX_SEARCH_HITS`] hits in
/// document order; callers compare lengths to detect truncation.
pub fn find_hits(lines: &[String], query: &str) -> Vec<SearchHit> {
    let query: String = query.chars().take(MAX_SEARCH_QUERY_CHARS).collect();
    if query.trim().is_empty() {
        return Vec::new();
    }
    let folded = query.to_lowercase();
    let mut hits = Vec::new();
    for (line, text) in lines.iter().enumerate() {
        let haystack = text.to_lowercase();
        let mut from = 0;
        while hits.len() < MAX_SEARCH_HITS {
            let Some(rel) = haystack[from..].find(&folded) else {
                break;
            };
            // Byte match -> char offset in the original line text.
            let byte = from + rel;
            let col = text[..byte.min(text.len())].chars().count();
            let len = folded.chars().count();
            hits.push(SearchHit { line, col, len });
            from = byte + folded.len().max(1);
            if from >= haystack.len() {
                break;
            }
        }
        if hits.len() >= MAX_SEARCH_HITS {
            break;
        }
    }
    hits
}

/// Viewport line text under the same builder rule as `scrollback_text`.
/// The UI paints highlight from dump-coordinate hits, so both sides must
/// agree on char offsets.
pub fn viewport_line_text(row: &TerminalRow) -> String {
    let mut text = String::new();
    for cell in &row.cells {
        if cell.width == CellWidth::WideContinuation {
            continue;
        }
        text.push_str(&cell.text);
    }
    text.trim_end().to_string()
}

/// Map a char range of a viewport row onto inclusive cell columns for
/// painting. Combining sequences stay with their lead cell; a range fully
/// inside a gap or past the end returns `None`.
pub fn char_range_to_cells(row: &TerminalRow, start: usize, len: usize) -> Option<(usize, usize)> {
    if len == 0 || row.cells.is_empty() {
        return None;
    }
    let mut char_at = 0;
    let mut first = None;
    let mut last = None;
    for (col, cell) in row.cells.iter().enumerate() {
        if cell.width == CellWidth::WideContinuation {
            continue;
        }
        let width = cell.text.chars().count().max(1);
        let span = char_at..char_at + width;
        if span.start < start + len && span.end > start {
            if first.is_none() {
                first = Some(col);
            }
            last = Some(col);
        }
        char_at += width;
    }
    match (first, last) {
        (Some(first), Some(last)) => Some((first, last)),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::engine::{CellFlags, TermColor, TerminalCell};

    fn cell(text: &str) -> TerminalCell {
        TerminalCell {
            text: text.into(),
            width: CellWidth::Single,
            fg: TermColor::DefaultFg,
            bg: TermColor::DefaultBg,
            flags: CellFlags::default(),
        }
    }

    #[test]
    fn empty_and_blank_queries_match_nothing() {
        let lines = vec!["foo".to_string()];
        assert!(find_hits(&lines, "").is_empty());
        assert!(find_hits(&lines, "   ").is_empty());
    }

    #[test]
    fn matching_is_case_insensitive_and_ordered() {
        let lines = vec!["Foo bar".to_string(), "baz FOO".to_string()];
        assert_eq!(
            find_hits(&lines, "foo"),
            vec![
                SearchHit {
                    line: 0,
                    col: 0,
                    len: 3
                },
                SearchHit {
                    line: 1,
                    col: 4,
                    len: 3
                },
            ]
        );
    }

    #[test]
    fn overlapping_scan_advances_past_each_hit() {
        assert_eq!(
            find_hits(&["aaa".to_string()], "aa"),
            vec![SearchHit {
                line: 0,
                col: 0,
                len: 2
            }]
        );
    }

    #[test]
    fn hits_are_capped_not_unbounded() {
        let lines = vec!["a".repeat(10); 2000];
        assert_eq!(find_hits(&lines, "a").len(), MAX_SEARCH_HITS);
    }

    #[test]
    fn queries_are_truncated_never_rejected() {
        let lines = vec!["x".to_string()];
        let long = "y".repeat(MAX_SEARCH_QUERY_CHARS + 10);
        assert!(find_hits(&lines, &long).is_empty());
    }

    #[test]
    fn cell_mapping_covers_plain_and_wide_rows() {
        let row = TerminalRow {
            cells: vec![cell("a"), cell("b"), cell("c")],
        };
        assert_eq!(char_range_to_cells(&row, 1, 1), Some((1, 1)));
        assert_eq!(char_range_to_cells(&row, 0, 3), Some((0, 2)));
        assert_eq!(char_range_to_cells(&row, 9, 1), None);
        assert_eq!(char_range_to_cells(&row, 0, 0), None);
    }

    #[test]
    fn combining_sequences_stay_inside_one_hit() {
        let row = TerminalRow {
            cells: vec![cell("e\u{301}"), cell("x")],
        };
        assert_eq!(viewport_line_text(&row), "e\u{301}x");
        assert_eq!(char_range_to_cells(&row, 0, 1), Some((0, 0)));
    }
}
