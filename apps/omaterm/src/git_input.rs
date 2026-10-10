//! Bounded single-line editing shared by Git commit and stash drafts.
use crate::editor::{EditorCaret, next_grapheme, previous_grapheme};
use unicode_segmentation::UnicodeSegmentation;

pub const MAX_MESSAGE_BYTES: usize = 4096;

#[derive(Default, Clone, Debug)]
pub struct GitInput {
    text: String,
    caret: EditorCaret,
}

impl GitInput {
    pub fn text(&self) -> &str {
        &self.text
    }

    pub fn cursor(&self) -> usize {
        self.caret.cursor
    }

    pub fn selection(&self) -> Option<(usize, usize)> {
        self.caret.selection_range()
    }

    pub fn into_text(self) -> String {
        self.text
    }

    pub fn selected_text(&self) -> Option<&str> {
        self.caret
            .selection_range()
            .map(|(start, end)| &self.text[start..end])
    }

    pub fn insert(&mut self, text: &str) -> bool {
        let clean: String = text
            .chars()
            .filter_map(|ch| match ch {
                '\n' | '\r' | '\t' => Some(' '),
                ch if ch.is_control() => None,
                ch => Some(ch),
            })
            .collect();
        if clean.is_empty() {
            return false;
        }
        let (start, end) = self
            .caret
            .selection_range()
            .unwrap_or((self.caret.cursor, self.caret.cursor));
        let available = MAX_MESSAGE_BYTES.saturating_sub(self.text.len() - (end - start));
        let accepted = clean
            .grapheme_indices(true)
            .take_while(|(offset, cluster)| offset + cluster.len() <= available)
            .last()
            .map_or(0, |(offset, cluster)| offset + cluster.len());
        if accepted == 0 {
            return false;
        }
        self.text.replace_range(start..end, &clean[..accepted]);
        self.caret.collapse_to(start + accepted);
        self.caret.clamp(&self.text);
        true
    }

    pub fn select_all(&mut self) {
        self.caret.anchor = Some(0);
        self.caret.cursor = self.text.len();
    }

    pub fn move_to(&mut self, offset: usize, extend: bool) {
        if extend {
            self.caret.anchor.get_or_insert(self.caret.cursor);
            self.caret.cursor = offset;
        } else {
            self.caret.collapse_to(offset);
        }
        self.caret.clamp(&self.text);
    }

    pub fn move_horizontal(&mut self, forward: bool, extend: bool) {
        let offset = if !extend && let Some((start, end)) = self.caret.selection_range() {
            if forward { end } else { start }
        } else if forward {
            next_grapheme(&self.text, self.caret.cursor)
        } else {
            previous_grapheme(&self.text, self.caret.cursor)
        };
        self.move_to(offset, extend);
    }

    pub fn delete(&mut self, forward: bool) -> bool {
        let (start, end) = self.caret.selection_range().unwrap_or_else(|| {
            if forward {
                (
                    self.caret.cursor,
                    next_grapheme(&self.text, self.caret.cursor),
                )
            } else {
                (
                    previous_grapheme(&self.text, self.caret.cursor),
                    self.caret.cursor,
                )
            }
        });
        if start == end {
            return false;
        }
        self.text.replace_range(start..end, "");
        self.caret.collapse_to(start);
        self.caret.clamp(&self.text);
        true
    }
}

#[cfg(test)]
#[path = "git_input_tests.rs"]
mod tests;
