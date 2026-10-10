use crate::engine::{CellFlags, CellWidth, TerminalViewport};

/// A cell coordinate in the visible viewport (row, col), zero-based.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct CellPoint {
    pub row: usize,
    pub col: usize,
}

impl CellPoint {
    pub fn new(row: usize, col: usize) -> Self {
        Self { row, col }
    }
}

/// A drag selection: where the press started (`anchor`) and where the
/// pointer currently is (`active`). Either order is legal; use
/// [`SelectionRange::normalized`] before consuming.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct SelectionRange {
    pub anchor: CellPoint,
    pub active: CellPoint,
}

impl SelectionRange {
    pub fn new(anchor: CellPoint, active: CellPoint) -> Self {
        Self { anchor, active }
    }

    /// `(start, end)` in document order (top row first, then left column).
    /// Returned points are inclusive cell coordinates.
    pub fn normalized(&self) -> (CellPoint, CellPoint) {
        if (self.active.row, self.active.col) < (self.anchor.row, self.anchor.col) {
            (self.active, self.anchor)
        } else {
            (self.anchor, self.active)
        }
    }

    /// A press without drag selects nothing.
    pub fn is_empty(&self) -> bool {
        self.anchor == self.active
    }
}

/// Extract the selected text from a viewport snapshot.
///
/// Rules (documented, M3 selection semantics):
/// - Coordinates are clamped to the snapshot; stale ranges never panic.
/// - `WideContinuation` cells are skipped (the lead cell carries the glyph);
///   a range edge landing on a continuation backs up to include the lead.
/// - Combining sequences ride along in the lead cell's text.
/// - `HIDDEN` cells (e.g. password input) contribute nothing — selection
///   must never leak concealed text to the clipboard.
/// - Each row is right-trimmed; rows join with `\n`, except a row whose
///   last cell carries `WRAPPED` (soft-wrapped long line), which joins to
///   the next row with no separator.
pub fn extract_text(viewport: &TerminalViewport, range: SelectionRange) -> String {
    let (start, end) = range.normalized();
    if start == end || viewport.rows.is_empty() {
        return String::new();
    }
    let last_row = viewport.rows.len() - 1;
    let start_row = start.row.min(last_row);
    let end_row = end.row.min(last_row);
    let mut out = String::new();
    for r in start_row..=end_row {
        let row = &viewport.rows[r];
        if row.cells.is_empty() {
            if r != end_row {
                out.push('\n');
            }
            continue;
        }
        let last_col = row.cells.len() - 1;
        let mut c0 = if r == start_row {
            start.col.min(last_col)
        } else {
            0
        };
        let c1 = if r == end_row {
            end.col.min(last_col)
        } else {
            last_col
        };
        // A range edge on a wide continuation includes the whole glyph.
        if row.cells[c0].width == CellWidth::WideContinuation && c0 > 0 {
            c0 -= 1;
        }
        if c1 >= c0 {
            let mut text = String::new();
            for cell in &row.cells[c0..=c1] {
                if cell.width == CellWidth::WideContinuation {
                    continue;
                }
                if cell.flags.contains(CellFlags::HIDDEN) {
                    continue;
                }
                text.push_str(&cell.text);
            }
            out.push_str(text.trim_end());
        }
        if r != end_row {
            let wrapped = row.cells[last_col].flags.contains(CellFlags::WRAPPED);
            if !wrapped {
                out.push('\n');
            }
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::engine::{CellFlags, CellWidth, TermColor, TerminalCell, TerminalRow};

    fn cell(text: &str) -> TerminalCell {
        TerminalCell {
            text: text.to_string(),
            width: CellWidth::Single,
            fg: TermColor::DefaultFg,
            bg: TermColor::DefaultBg,
            flags: CellFlags::default(),
        }
    }

    fn wide_lead(text: &str) -> TerminalCell {
        TerminalCell {
            width: CellWidth::Wide,
            ..cell(text)
        }
    }

    fn wide_cont() -> TerminalCell {
        TerminalCell {
            width: CellWidth::WideContinuation,
            ..cell(" ")
        }
    }

    fn flagged(text: &str, flags: CellFlags) -> TerminalCell {
        TerminalCell {
            flags,
            ..cell(text)
        }
    }

    fn viewport(rows: Vec<Vec<TerminalCell>>) -> TerminalViewport {
        use crate::engine::{CursorShape, CursorState};
        TerminalViewport {
            rows: rows
                .into_iter()
                .map(|cells| TerminalRow { cells })
                .collect(),
            cursor: CursorState {
                row: 0,
                col: 0,
                shape: CursorShape::Block,
                visible: true,
            },
            cols: 0,
            cursor_color: crate::color::terminal_palette().cursor,
            lines: 0,
            display_offset: 0,
            history_size: 0,
            is_alt_screen: false,
        }
    }

    fn row_of(text: &str) -> Vec<TerminalCell> {
        text.chars().map(|c| cell(&c.to_string())).collect()
    }

    fn select(sr: usize, sc: usize, er: usize, ec: usize) -> SelectionRange {
        SelectionRange::new(CellPoint::new(sr, sc), CellPoint::new(er, ec))
    }

    #[test]
    fn single_row_slice() {
        let vp = viewport(vec![row_of("hello world")]);
        assert_eq!(extract_text(&vp, select(0, 0, 0, 4)), "hello");
        assert_eq!(extract_text(&vp, select(0, 6, 0, 10)), "world");
    }

    #[test]
    fn reverse_drag_normalizes() {
        let vp = viewport(vec![row_of("hello world")]);
        assert_eq!(extract_text(&vp, select(0, 10, 0, 6)), "world");
    }

    #[test]
    fn empty_selection_yields_nothing() {
        let vp = viewport(vec![row_of("hello")]);
        assert_eq!(extract_text(&vp, select(0, 2, 0, 2)), "");
    }

    #[test]
    fn multi_row_joins_with_newline() {
        let vp = viewport(vec![row_of("foo"), row_of("bar")]);
        assert_eq!(extract_text(&vp, select(0, 0, 1, 2)), "foo\nbar");
    }

    #[test]
    fn trailing_spaces_trimmed_per_row() {
        let vp = viewport(vec![row_of("hi   "), row_of("yo")]);
        assert_eq!(extract_text(&vp, select(0, 0, 1, 1)), "hi\nyo");
    }

    #[test]
    fn wrapped_rows_join_without_separator() {
        let mut first = row_of("abcdefgh");
        let last = first.len() - 1;
        first[last] = flagged("h", CellFlags::WRAPPED);
        let vp = viewport(vec![first, row_of("ijklmnop")]);
        assert_eq!(extract_text(&vp, select(0, 0, 1, 7)), "abcdefghijklmnop");
    }

    #[test]
    fn wide_chars_copy_once() {
        let vp = viewport(vec![vec![
            cell("a"),
            wide_lead("你"),
            wide_cont(),
            cell("b"),
        ]]);
        assert_eq!(extract_text(&vp, select(0, 0, 0, 3)), "a你b");
    }

    #[test]
    fn edge_on_continuation_includes_lead() {
        let vp = viewport(vec![vec![
            cell("a"),
            wide_lead("你"),
            wide_cont(),
            cell("b"),
        ]]);
        // Start exactly on the continuation cell.
        assert_eq!(extract_text(&vp, select(0, 2, 0, 3)), "你b");
    }

    #[test]
    fn combining_sequence_preserved() {
        let mut c = cell("e");
        c.text.push('\u{301}');
        let vp = viewport(vec![vec![c, cell("x")]]);
        assert_eq!(extract_text(&vp, select(0, 0, 0, 1)), "éx");
    }

    #[test]
    fn hidden_cells_never_leak() {
        let vp = viewport(vec![vec![
            cell("p"),
            flagged("w", CellFlags::HIDDEN),
            cell("d"),
        ]]);
        assert_eq!(extract_text(&vp, select(0, 0, 0, 2)), "pd");
    }

    #[test]
    fn out_of_bounds_clamps() {
        let vp = viewport(vec![row_of("hi")]);
        assert_eq!(extract_text(&vp, select(0, 0, 9, 99)), "hi");
        assert_eq!(extract_text(&vp, select(9, 9, 9, 9)), "");
    }
}
