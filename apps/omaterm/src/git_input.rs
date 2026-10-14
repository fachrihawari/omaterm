//! Bounded single-line editing shared by Git commit and stash drafts.
//!
//! Since the shared component layer landed, this is a thin typed wrapper over
//! [`crate::ui::components::text_field::TextField`]: the buffer, caret,
//! selection, word motion, clipboard, and undo/redo all live in the shared
//! field. The wrapper exists so Git-specific naming and the message byte
//! ceiling stay explicit at the call sites.
use crate::ui::components::text_field::TextField;

pub const MAX_MESSAGE_BYTES: usize = 4096;

#[derive(Clone, Debug)]
pub struct GitInput {
    field: TextField,
}

impl Default for GitInput {
    fn default() -> Self {
        Self::new()
    }
}

// The wrapper mirrors the shared field's editing surface so Git call sites
// keep their names. Production code reaches the field directly via `field()`;
// the wrapper accessors below are exercised by `git_input_tests`, which is why
// they are not dead even though `cargo` cannot see the test call graph.
#[cfg_attr(not(test), allow(dead_code))]
impl GitInput {
    /// A Git draft with the commit-message byte ceiling applied.
    pub fn new() -> Self {
        Self {
            field: TextField::new().with_max_bytes(MAX_MESSAGE_BYTES),
        }
    }
    pub fn text(&self) -> &str {
        self.field.text()
    }

    // The wrapper exposes the editing surface of the shared field so Git call
    // sites keep their names. A few accessors are exercised only by the Git
    // tests today (production paths reach the field directly via `field()`),
    // hence the scoped allows.
    pub fn cursor(&self) -> usize {
        self.field.cursor()
    }

    pub fn into_text(self) -> String {
        self.field.into_text()
    }

    pub fn selected_text(&self) -> Option<&str> {
        self.field.selected_text()
    }

    pub fn insert(&mut self, text: &str) -> bool {
        self.field.insert(text)
    }

    pub fn select_all(&mut self) {
        self.field.select_all();
    }

    pub fn move_to(&mut self, offset: usize, extend: bool) {
        self.field.move_to(offset, extend);
    }

    pub fn move_horizontal(&mut self, forward: bool, extend: bool) {
        self.field.move_horizontal(forward, extend);
    }

    pub fn delete(&mut self, forward: bool) -> bool {
        self.field.delete(forward)
    }
    /// Read access to the underlying shared field (routing, render).
    pub fn field(&self) -> &TextField {
        &self.field
    }

    pub fn field_mut(&mut self) -> &mut TextField {
        &mut self.field
    }
}

#[cfg(test)]
#[path = "git_input_tests.rs"]
mod tests;
