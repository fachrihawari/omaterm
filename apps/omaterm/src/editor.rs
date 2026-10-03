//! Native document buffers for the built-in editor (M19 Phase C).
//!
//! GPUI-free, like `history.rs` and the panel models: the desktop renderer
//! owns caret/selection/paint state, while this store owns document text,
//! dirty tracking, bounded undo/redo, and on-disk revision generations.
//! Keystroke-level edits apply here directly through [`DocumentStore`]
//! methods — they are UI text-input state, not dispatcher commands. Only
//! open/close/save/revert travel through `EditorCommand` dispatch, where the
//! router revalidates project scope and root identity before touching disk.
//!
//! Invariants:
//! - One buffer per (project, root identity, opened-file identity); reopening
//!   a contained symlink alias returns the live document instead of forking it.
//! - Dirty text is never persisted except through an explicit save.
//! - Undo history is bounded by entry count and retained bytes; the oldest
//!   entries drop first and redo clears on every new edit.
//! - Byte offsets are validated as UTF-8 char boundaries before mutation.
//!
//! The buffer-editing API (`apply_edit`/`undo`/`redo`, direct text access)
//! is exercised by tests here and consumed by the Phase D editing surface;
//! the dispatcher owns lifecycle/save only.
//!
//! Dead-code is allowed module-wide until Phase D wires the editing surface
//! (theme-token precedent): removing it now would only re-add identical API.
#![allow(dead_code)]

use std::collections::{HashMap, VecDeque};
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use unicode_segmentation::UnicodeSegmentation;
use unicode_width::UnicodeWidthChar;

use omaterm_core::{CommandError, DocumentId, EditorDocumentInfo, ErrorCode, ProjectId};

/// Bounded undo history: at most this many edits plus this many retained
/// bytes per document. A 1 MiB buffer with pathological 1-byte edits stays
/// under ~8 MiB of undo state.
const MAX_UNDO_ENTRIES: usize = 100;
const MAX_UNDO_BYTES: usize = 8 * 1024 * 1024;

/// One reversible text replacement: `removed.len()` bytes at `start` were
/// replaced, and `added_len` bytes now occupy that span.
#[derive(Debug, Clone, PartialEq, Eq)]
struct TextEdit {
    start: usize,
    removed: String,
    added_len: usize,
}

impl TextEdit {
    fn retained_bytes(&self) -> usize {
        self.removed.len() + self.added_len
    }
}

/// One open document: live text plus dirty state, on-disk generation, and
/// bounded undo/redo stacks.
#[derive(Debug)]
pub struct Document {
    id: DocumentId,
    project: ProjectId,
    /// Root-relative path as supplied on open (display/identity form).
    path: PathBuf,
    /// Canonical project root at open time, retained for diagnostics and
    /// descriptor capture. Identity, not this pathname, guards later I/O.
    root: PathBuf,
    root_identity: omaterm_context::RootIdentity,
    file_device: u64,
    file_inode: u64,
    text: String,
    saved_text: String,
    history_cursor: Option<usize>,
    dirty: bool,
    revision: omaterm_context::FileRevision,
    language: omaterm_context::EditorLanguage,
    undo: Vec<TextEdit>,
    undo_bytes: usize,
    redo: Vec<TextEdit>,
}

impl Document {
    pub fn info(&self) -> EditorDocumentInfo {
        EditorDocumentInfo {
            document: self.id,
            project: self.project,
            path: self.path.clone(),
            bytes: self.text.len(),
            lines: count_lines(&self.text),
        }
    }
}

fn count_lines(text: &str) -> usize {
    if text.is_empty() {
        0
    } else {
        text.matches('\n').count() + 1
    }
}

#[derive(Debug, Default)]
pub struct DocumentStore {
    docs: HashMap<DocumentId, Document>,
    by_file: HashMap<(ProjectId, omaterm_context::RootIdentity, u64, u64), DocumentId>,
}

impl DocumentStore {
    /// Open a buffer over an already-validated read. Returns the live document
    /// when the same opened file (including a contained symlink alias) is open
    /// beneath the same captured root identity.
    pub fn open(
        &mut self,
        project: ProjectId,
        path: PathBuf,
        root: PathBuf,
        root_identity: omaterm_context::RootIdentity,
        file: omaterm_context::EditorFile,
    ) -> DocumentId {
        let file_key = (
            project,
            root_identity,
            file.revision.device,
            file.revision.inode,
        );
        if let Some(id) = self.by_file.get(&file_key) {
            return *id;
        }
        let id = DocumentId::new();
        let language = file.language;
        let revision = file.revision;
        self.by_file.insert(file_key, id);
        self.docs.insert(
            id,
            Document {
                id,
                project,
                path,
                root,
                root_identity,
                file_device: revision.device,
                file_inode: revision.inode,
                saved_text: file.text.clone(),
                text: file.text,
                history_cursor: None,
                dirty: false,
                revision,
                language,
                undo: Vec::new(),
                undo_bytes: 0,
                redo: Vec::new(),
            },
        );
        id
    }

    pub fn get(&self, document: DocumentId) -> Option<&Document> {
        self.docs.get(&document)
    }

    pub fn project_of(&self, document: DocumentId) -> Option<ProjectId> {
        self.docs.get(&document).map(|doc| doc.project)
    }

    pub fn document_info(&self, document: DocumentId) -> Option<EditorDocumentInfo> {
        self.docs.get(&document).map(Document::info)
    }

    /// Root-relative path as supplied on open (display/identity form).
    pub fn relative_path(&self, document: DocumentId) -> Option<PathBuf> {
        self.docs.get(&document).map(|doc| doc.path.clone())
    }

    /// Canonical project root captured at open time.
    pub fn open_root(&self, document: DocumentId) -> Option<PathBuf> {
        self.docs.get(&document).map(|doc| doc.root.clone())
    }

    /// Device/inode identity of the root captured when the document opened.
    pub fn open_root_identity(
        &self,
        document: DocumentId,
    ) -> Option<omaterm_context::RootIdentity> {
        self.docs.get(&document).map(|doc| doc.root_identity)
    }

    pub fn revision(&self, document: DocumentId) -> Option<omaterm_context::FileRevision> {
        self.docs.get(&document).map(|doc| doc.revision)
    }

    /// Owned copy of the live buffer for saves. Cloned once per save;
    /// bounded by the editor byte cap.
    pub fn buffer_text(&self, document: DocumentId) -> Option<String> {
        self.docs.get(&document).map(|doc| doc.text.clone())
    }

    pub fn text(&self, document: DocumentId) -> Option<&str> {
        self.docs.get(&document).map(|doc| doc.text.as_str())
    }

    pub fn is_dirty(&self, document: DocumentId) -> Option<bool> {
        self.docs.get(&document).map(|doc| doc.dirty)
    }

    pub fn has_dirty_documents(&self) -> bool {
        self.docs.values().any(|doc| doc.dirty)
    }

    pub fn dirty_documents(&self) -> Vec<DocumentId> {
        self.docs
            .iter()
            .filter(|(_, doc)| doc.dirty)
            .map(|(id, _)| *id)
            .collect()
    }

    pub fn history_cursor(&self, document: DocumentId) -> Option<usize> {
        self.docs.get(&document)?.history_cursor
    }

    /// Called only after an explicit discard decision. Does not touch disk.
    pub fn discard_changes(&mut self, document: DocumentId) {
        if let Some(doc) = self.docs.get_mut(&document) {
            doc.text.clone_from(&doc.saved_text);
            doc.dirty = false;
            doc.undo.clear();
            doc.redo.clear();
            doc.undo_bytes = 0;
            doc.history_cursor = None;
        }
    }

    pub fn language(&self, document: DocumentId) -> Option<omaterm_context::EditorLanguage> {
        self.docs.get(&document).map(|doc| doc.language)
    }

    pub fn remove(&mut self, document: DocumentId) -> bool {
        let Some(doc) = self.docs.remove(&document) else {
            return false;
        };
        self.by_file.remove(&(
            doc.project,
            doc.root_identity,
            doc.file_device,
            doc.file_inode,
        ));
        true
    }

    /// Open document IDs owned by `project`, stable-sorted by path for
    /// deterministic tab order.
    pub fn project_documents(&self, project: ProjectId) -> Vec<DocumentId> {
        let mut docs: Vec<(PathBuf, DocumentId)> = self
            .docs
            .iter()
            .filter(|(_, doc)| doc.project == project)
            .map(|(id, doc)| (doc.path.clone(), *id))
            .collect();
        docs.sort_by(|left, right| left.0.cmp(&right.0));
        docs.into_iter().map(|(_, id)| id).collect()
    }

    /// Drop every document owned by `project` (project deletion). Returns
    /// the closed count; dirty buffers are discarded with the project, never
    /// written — deletion is an explicit user action.
    pub fn close_project(&mut self, project: ProjectId) -> usize {
        let ids: Vec<DocumentId> = self
            .docs
            .iter()
            .filter(|(_, doc)| doc.project == project)
            .map(|(id, _)| *id)
            .collect();
        let count = ids.len();
        for id in ids {
            self.remove(id);
        }
        count
    }

    /// Apply one text replacement to a buffer. Byte offsets must be UTF-8
    /// char boundaries inside the current text; the result stays within the
    /// editor byte/line caps. Records the inverse delta, clears redo, and
    /// marks the document dirty.
    pub fn apply_edit(
        &mut self,
        document: DocumentId,
        start: usize,
        remove_len: usize,
        insert: &str,
    ) -> Result<(), CommandError> {
        let invalid = |message: &str| CommandError::new(ErrorCode::InvalidRequest, message);
        let Some(doc) = self.docs.get_mut(&document) else {
            return Err(CommandError::new(
                ErrorCode::DocumentNotOpen,
                "document is not open",
            ));
        };
        let end = start
            .checked_add(remove_len)
            .ok_or_else(|| invalid("edit range overflows"))?;
        if end > doc.text.len()
            || !doc.text.is_char_boundary(start)
            || !doc.text.is_char_boundary(end)
        {
            return Err(invalid("edit range is outside the document text"));
        }
        let next_len = (doc.text.len() - remove_len).checked_add(insert.len());
        if next_len.is_none_or(|len| len > omaterm_core::validation::MAX_EDITOR_BYTES) {
            return Err(CommandError::new(
                ErrorCode::DocumentTooLarge,
                "edit would exceed the 1 MiB document cap",
            ));
        }
        if remove_len == 0 && insert.is_empty() || doc.text[start..end] == *insert {
            return Ok(());
        }
        let mut next = String::with_capacity(next_len.unwrap());
        next.push_str(&doc.text[..start]);
        next.push_str(insert);
        next.push_str(&doc.text[end..]);
        if next.len() > omaterm_core::validation::MAX_EDITOR_BYTES {
            return Err(CommandError::new(
                ErrorCode::DocumentTooLarge,
                "edit would exceed the 1 MiB document cap",
            ));
        }
        if count_lines(&next) > omaterm_core::validation::MAX_EDITOR_LINES {
            return Err(CommandError::new(
                ErrorCode::DocumentTooLarge,
                "edit would exceed the 20000-line document cap",
            ));
        }
        let removed = doc.text[start..end].to_owned();
        let edit = TextEdit {
            start,
            removed,
            added_len: insert.len(),
        };
        doc.undo_bytes += edit.retained_bytes();
        doc.undo.push(edit);
        while doc.undo.len() > MAX_UNDO_ENTRIES || doc.undo_bytes > MAX_UNDO_BYTES {
            if let Some(dropped) = doc.undo.first() {
                doc.undo_bytes = doc.undo_bytes.saturating_sub(dropped.retained_bytes());
            }
            doc.undo.remove(0);
        }
        doc.redo.clear();
        doc.text = next;
        doc.dirty = doc.text != doc.saved_text;
        doc.history_cursor = None;
        Ok(())
    }

    /// Undo the newest edit. Returns whether an edit was undone.
    pub fn undo(&mut self, document: DocumentId) -> Result<bool, CommandError> {
        let Some(doc) = self.docs.get_mut(&document) else {
            return Err(CommandError::new(
                ErrorCode::DocumentNotOpen,
                "document is not open",
            ));
        };
        let Some(edit) = doc.undo.pop() else {
            return Ok(false);
        };
        doc.undo_bytes = doc.undo_bytes.saturating_sub(edit.retained_bytes());
        let end = edit.start + edit.added_len;
        let mut next = String::with_capacity(doc.text.len() - edit.added_len + edit.removed.len());
        next.push_str(&doc.text[..edit.start]);
        next.push_str(&edit.removed);
        next.push_str(&doc.text[end..]);
        let redo = TextEdit {
            start: edit.start,
            removed: doc.text[edit.start..end].to_owned(),
            added_len: edit.removed.len(),
        };
        doc.redo.push(redo);
        doc.text = next;
        doc.history_cursor = Some(edit.start + edit.removed.len());
        doc.dirty = doc.text != doc.saved_text;
        Ok(true)
    }

    /// Redo the newest undone edit. Returns whether an edit was redone.
    pub fn redo(&mut self, document: DocumentId) -> Result<bool, CommandError> {
        let Some(doc) = self.docs.get_mut(&document) else {
            return Err(CommandError::new(
                ErrorCode::DocumentNotOpen,
                "document is not open",
            ));
        };
        let Some(edit) = doc.redo.pop() else {
            return Ok(false);
        };
        let end = edit.start + edit.added_len;
        let mut next = String::with_capacity(doc.text.len() - edit.added_len + edit.removed.len());
        next.push_str(&doc.text[..edit.start]);
        next.push_str(&edit.removed);
        next.push_str(&doc.text[end..]);
        let undo = TextEdit {
            start: edit.start,
            removed: doc.text[edit.start..end].to_owned(),
            added_len: edit.removed.len(),
        };
        doc.undo_bytes += undo.retained_bytes();
        doc.undo.push(undo);
        while doc.undo.len() > MAX_UNDO_ENTRIES || doc.undo_bytes > MAX_UNDO_BYTES {
            if let Some(dropped) = doc.undo.first() {
                doc.undo_bytes = doc.undo_bytes.saturating_sub(dropped.retained_bytes());
            }
            doc.undo.remove(0);
        }
        doc.text = next;
        doc.history_cursor = Some(edit.start + edit.removed.len());
        doc.dirty = doc.text != doc.saved_text;
        Ok(true)
    }

    /// Replace buffer text from a fresh disk read (revert path): clears
    /// undo/redo, clears dirty, adopts the new revision.
    pub fn adopt_disk_text(&mut self, document: DocumentId, file: omaterm_context::EditorFile) {
        let Some((old_key, new_key)) = self.docs.get_mut(&document).map(|doc| {
            let old_key = (
                doc.project,
                doc.root_identity,
                doc.file_device,
                doc.file_inode,
            );
            doc.saved_text.clone_from(&file.text);
            doc.text = file.text;
            doc.history_cursor = None;
            doc.revision = file.revision;
            doc.language = file.language;
            doc.file_device = file.revision.device;
            doc.file_inode = file.revision.inode;
            doc.undo.clear();
            doc.undo_bytes = 0;
            doc.redo.clear();
            doc.dirty = false;
            (
                old_key,
                (
                    doc.project,
                    doc.root_identity,
                    doc.file_device,
                    doc.file_inode,
                ),
            )
        }) else {
            return;
        };
        self.by_file.remove(&old_key);
        self.by_file.insert(new_key, document);
    }

    /// Record a successful save: clear dirty, adopt the post-write revision.
    /// Undo history survives save so edits remain reversible afterwards.
    pub fn mark_saved(&mut self, document: DocumentId, revision: omaterm_context::FileRevision) {
        let Some((old_key, new_key)) = self.docs.get_mut(&document).map(|doc| {
            let old_key = (
                doc.project,
                doc.root_identity,
                doc.file_device,
                doc.file_inode,
            );
            doc.revision = revision;
            doc.file_device = revision.device;
            doc.file_inode = revision.inode;
            doc.saved_text.clone_from(&doc.text);
            doc.dirty = false;
            (
                old_key,
                (
                    doc.project,
                    doc.root_identity,
                    doc.file_device,
                    doc.file_inode,
                ),
            )
        }) else {
            return;
        };
        self.by_file.remove(&old_key);
        self.by_file.insert(new_key, document);
    }
}

/// Fixed tab rendering width. Buffers keep real `\t` bytes; only
/// presentation and width estimates expand them.
pub const TAB_WIDTH: usize = 4;

/// Syntax token class. Presentation-only; the buffer never changes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TokenKind {
    Comment,
    String,
    Number,
    Keyword,
}

/// One highlighted byte range: `[start, start + len)` over the tokenized
/// text. Spans are sorted, non-overlapping, and always char-boundary
/// aligned (only ASCII bytes are ever classified).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct TokenSpan {
    pub start: usize,
    pub len: usize,
    pub kind: TokenKind,
}

/// Estimated display columns for one char: 2 for wide CJK/emoji ranges,
/// 0 for control chars, 1 otherwise. An estimate — the renderer shapes the
/// caret line exactly; this only sizes scroll extents and offsets.
pub fn char_display_width(ch: char) -> usize {
    ch.width().unwrap_or(0)
}

/// Display columns for a line with tabs expanded to `TAB_WIDTH` stops.
pub fn line_display_width(line: &str) -> usize {
    let mut cols = 0;
    for ch in line.chars() {
        if ch == '\t' {
            cols += TAB_WIDTH - (cols % TAB_WIDTH);
        } else {
            cols += char_display_width(ch);
        }
    }
    cols
}

/// Caret plus optional selection anchor, as buffer byte offsets. The anchor
/// end is exclusive; `anchor == None` or `anchor == cursor` means no
/// selection.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct EditorCaret {
    pub cursor: usize,
    pub anchor: Option<usize>,
}

impl EditorCaret {
    /// Ordered, non-empty selection range, if any.
    pub fn selection_range(&self) -> Option<(usize, usize)> {
        let anchor = self.anchor?;
        if anchor == self.cursor {
            None
        } else {
            Some((anchor.min(self.cursor), anchor.max(self.cursor)))
        }
    }

    pub fn collapse_to(&mut self, offset: usize) {
        self.cursor = offset;
        self.anchor = None;
    }

    pub fn clamp(&mut self, text: &str) {
        self.cursor = clamp_grapheme_offset(text, self.cursor);
        self.anchor = self
            .anchor
            .map(|offset| clamp_grapheme_offset(text, offset))
            .filter(|anchor| *anchor != self.cursor);
    }
}

fn clamp_grapheme_offset(text: &str, offset: usize) -> usize {
    text.grapheme_indices(true)
        .map(|(index, _)| index)
        .chain(std::iter::once(text.len()))
        .take_while(|index| *index <= offset.min(text.len()))
        .last()
        .unwrap_or(0)
}

pub fn previous_grapheme(text: &str, offset: usize) -> usize {
    text.grapheme_indices(true)
        .map(|(index, _)| index)
        .take_while(|index| *index < offset.min(text.len()))
        .last()
        .unwrap_or(0)
}

pub fn next_grapheme(text: &str, offset: usize) -> usize {
    text.grapheme_indices(true)
        .map(|(index, _)| index)
        .find(|index| *index > offset)
        .unwrap_or(text.len())
}

const RUST_KEYWORDS: &[&str] = &[
    "as", "async", "await", "break", "const", "continue", "crate", "dyn", "else", "enum", "extern",
    "false", "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub",
    "ref", "return", "self", "Self", "static", "struct", "super", "trait", "true", "type",
    "unsafe", "use", "where", "while",
];

const BASH_KEYWORDS: &[&str] = &[
    "case", "do", "done", "echo", "elif", "else", "esac", "exit", "export", "fi", "for",
    "function", "if", "in", "local", "readonly", "return", "select", "then", "until", "while",
];

fn is_ident_start(byte: u8) -> bool {
    byte.is_ascii_alphabetic() || byte == b'_'
}

fn is_ident_continue(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || byte == b'_'
}

fn utf8_len(first: u8) -> usize {
    if first < 0x80 {
        1
    } else if first >> 5 == 0b110 {
        2
    } else if first >> 4 == 0b1110 {
        3
    } else {
        4
    }
}

struct Scanner<'a> {
    bytes: &'a [u8],
    pos: usize,
    spans: Vec<TokenSpan>,
    cancelled: Option<&'a std::sync::atomic::AtomicBool>,
}

impl<'a> Scanner<'a> {
    fn new(text: &'a str, cancelled: Option<&'a std::sync::atomic::AtomicBool>) -> Self {
        Self {
            bytes: text.as_bytes(),
            pos: 0,
            spans: Vec::new(),
            cancelled,
        }
    }

    fn is_cancelled(&self) -> bool {
        self.cancelled
            .is_some_and(|flag| flag.load(std::sync::atomic::Ordering::Relaxed))
    }

    fn rest_starts_with(&self, pattern: &[u8]) -> bool {
        self.bytes[self.pos..].starts_with(pattern)
    }

    fn push(&mut self, start: usize, kind: TokenKind) {
        self.spans.push(TokenSpan {
            start,
            len: self.pos - start,
            kind,
        });
    }

    /// Consume a `"`-style string with backslash escapes through the
    /// closing quote or end of input.
    fn string(&mut self, kind: TokenKind) {
        let start = self.pos;
        self.pos += 1;
        while self.pos < self.bytes.len() && !self.is_cancelled() {
            let byte = self.bytes[self.pos];
            if byte == b'\\' {
                self.pos += 1;
                if self.pos < self.bytes.len() {
                    self.pos += utf8_len(self.bytes[self.pos]);
                }
                continue;
            }
            self.pos += utf8_len(byte);
            if byte == b'"' {
                break;
            }
        }
        self.push(start, kind);
    }

    /// Consume a Bash `'...'` string (no escapes) through the quote or EOF.
    fn single_string(&mut self) {
        let start = self.pos;
        self.pos += 1;
        while self.pos < self.bytes.len() && !self.is_cancelled() && self.bytes[self.pos] != b'\'' {
            self.pos += utf8_len(self.bytes[self.pos]);
        }
        self.pos = (self.pos + 1).min(self.bytes.len());
        self.push(start, TokenKind::String);
    }

    /// Consume a line comment through (not including) the newline.
    fn line_comment(&mut self) {
        let start = self.pos;
        while self.pos < self.bytes.len() && !self.is_cancelled() && self.bytes[self.pos] != b'\n' {
            self.pos += utf8_len(self.bytes[self.pos]);
        }
        self.push(start, TokenKind::Comment);
    }

    /// Consume one identifier; emit a keyword span on match. Linear scan:
    /// the tables are tiny and this avoids any sorted-order invariant.
    fn word(&mut self, keywords: &[&str]) {
        let start = self.pos;
        while self.pos < self.bytes.len()
            && !self.is_cancelled()
            && is_ident_continue(self.bytes[self.pos])
        {
            self.pos += 1;
        }
        // Identifier bytes are ASCII alphanumerics by construction.
        if let Ok(word) = std::str::from_utf8(&self.bytes[start..self.pos])
            && keywords.contains(&word)
        {
            self.push(start, TokenKind::Keyword);
        }
    }

    /// Consume a number literal from a leading digit.
    fn number(&mut self) {
        let start = self.pos;
        if self.bytes[self.pos] == b'-' {
            self.pos += 1;
        }
        while self.pos < self.bytes.len()
            && !self.is_cancelled()
            && (self.bytes[self.pos].is_ascii_alphanumeric()
                || matches!(self.bytes[self.pos], b'_' | b'.'))
        {
            self.pos += 1;
        }
        self.push(start, TokenKind::Number);
    }
}

/// Whether a `#` byte starts a shell/TOML comment: start of input, or
/// preceded by whitespace or a command separator — but never `$#` (the
/// shell argument count). Strings are consumed left-to-right before this
/// check, so `#` inside quotes never reaches it.
fn is_comment_hash(scan: &Scanner<'_>) -> bool {
    if scan.pos == 0 {
        return true;
    }
    let prev = scan.bytes[scan.pos - 1];
    if prev == b'$' {
        return false;
    }
    prev.is_ascii_whitespace() || matches!(prev, b';' | b'(' | b')' | b'{' | b'}')
}

/// Tokenize `text` for `language` into sorted, non-overlapping, UTF-8-safe
/// spans. Unclosed strings/comments run to end of input; unknown text is
/// implicitly plain. Fast single pass — safe to rerun per keystroke on a
/// background thread for bounded documents.
pub fn tokenize(language: omaterm_context::EditorLanguage, text: &str) -> Vec<TokenSpan> {
    tokenize_cancellable(language, text, None)
}

fn tokenize_cancellable(
    language: omaterm_context::EditorLanguage,
    text: &str,
    cancelled: Option<&std::sync::atomic::AtomicBool>,
) -> Vec<TokenSpan> {
    use omaterm_context::EditorLanguage as Lang;
    if matches!(language, Lang::Plain) {
        return Vec::new();
    }
    if matches!(language, Lang::Markdown) {
        return tokenize_markdown(text, cancelled);
    }
    let mut scan = Scanner::new(text, cancelled);
    let keywords: &[&str] = match language {
        Lang::Rust => RUST_KEYWORDS,
        Lang::Bash => BASH_KEYWORDS,
        Lang::Toml => &["true", "false"],
        Lang::Json => &["true", "false", "null"],
        _ => &[],
    };
    let bash = matches!(language, Lang::Bash);
    let hash_comment = !matches!(language, Lang::Json);
    let block_comment = matches!(language, Lang::Rust);
    while scan.pos < scan.bytes.len() && !scan.is_cancelled() {
        let byte = scan.bytes[scan.pos];
        if byte == b'"' {
            scan.string(TokenKind::String);
        } else if bash && byte == b'\'' {
            scan.single_string();
        } else if matches!(language, Lang::Rust) && byte == b'\'' {
            // Rust char literal `'x'` / `'\n'` versus lifetime `'a`:
            // only the exact literal shape becomes a string span.
            let rest = &scan.bytes[scan.pos..];
            let content = if rest.get(1) == Some(&b'\\') { 2 } else { 1 };
            let end = rest.get(content).map(|first| content + utf8_len(*first));
            if let Some(end) = end.filter(|end| rest.get(*end) == Some(&b'\'')) {
                let start = scan.pos;
                scan.pos += end + 1;
                scan.push(start, TokenKind::String);
            } else {
                scan.pos += 1;
            }
        } else if (!bash && scan.rest_starts_with(b"//"))
            || (hash_comment && byte == b'#' && is_comment_hash(&scan))
        {
            scan.line_comment();
        } else if block_comment && scan.rest_starts_with(b"/*") {
            let start = scan.pos;
            scan.pos += 2;
            let mut depth = 1;
            while scan.pos < scan.bytes.len() && depth > 0 && !scan.is_cancelled() {
                if scan.rest_starts_with(b"/*") {
                    depth += 1;
                    scan.pos += 2;
                } else if scan.rest_starts_with(b"*/") {
                    depth -= 1;
                    scan.pos += 2;
                } else {
                    scan.pos += utf8_len(scan.bytes[scan.pos]);
                }
            }
            scan.push(start, TokenKind::Comment);
        } else if is_ident_start(byte) {
            scan.word(keywords);
        } else if byte.is_ascii_digit()
            || (byte == b'-'
                && matches!(language, Lang::Json)
                && scan.pos + 1 < scan.bytes.len()
                && scan.bytes[scan.pos + 1].is_ascii_digit())
        {
            scan.number();
        } else {
            scan.pos += utf8_len(byte);
        }
    }
    scan.spans
}

/// Markdown subset: `#`-led headings (whole line) and same-line `` `code` ``
/// spans. Emphasis/link markup stays plain in first delivery.
fn tokenize_markdown(
    text: &str,
    cancelled: Option<&std::sync::atomic::AtomicBool>,
) -> Vec<TokenSpan> {
    let mut spans = Vec::new();
    let mut offset = 0;
    for line in text.split_inclusive('\n') {
        if cancelled.is_some_and(|flag| flag.load(std::sync::atomic::Ordering::Relaxed)) {
            break;
        }
        let trimmed = line.trim_start();
        let indent = line.len() - trimmed.len();
        if trimmed.starts_with('#') {
            let hashes = trimmed.bytes().take_while(|byte| *byte == b'#').count();
            if trimmed.as_bytes().get(hashes) == Some(&b' ') {
                spans.push(TokenSpan {
                    start: offset,
                    len: line.len(),
                    kind: TokenKind::Keyword,
                });
                offset += line.len();
                continue;
            }
        }
        // Backtick pairs on the same line; the indent offset keeps spans
        // absolute without re-scanning.
        let mut search = indent;
        while let Some(open) = line[search..].find('`') {
            let open = search + open;
            let after = open + 1;
            if let Some(close) = line[after..].find('`') {
                spans.push(TokenSpan {
                    start: offset + open,
                    len: close + 2,
                    kind: TokenKind::String,
                });
                search = after + close + 1;
            } else {
                break;
            }
        }
        offset += line.len();
    }
    spans
}

/// Landed highlight state per document: the requested generation it belongs
/// to, token spans over that generation's text, and max display columns
/// for horizontal extents.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct DocHighlight {
    pub requested: u64,
    pub spans: Vec<TokenSpan>,
    pub max_cols: usize,
}

/// Byte offset where every line starts. Line `i` covers
/// `[starts[i], starts[i + 1])`; the final line runs to end of text.
/// Empty text still yields one (empty) line so the caret has a home.
pub fn line_starts(text: &str) -> Vec<usize> {
    let mut starts = vec![0];
    for (index, byte) in text.bytes().enumerate() {
        if byte == b'\n' {
            starts.push(index + 1);
        }
    }
    starts
}

/// `(line, column-bytes)` for a buffer offset, clamped into the text and
/// onto char boundaries.
pub fn offset_to_line_col(starts: &[usize], text: &str, offset: usize) -> (usize, usize) {
    if starts.is_empty() {
        return (0, 0);
    }
    let offset = offset.min(text.len());
    let mut line = starts
        .partition_point(|start| *start <= offset)
        .saturating_sub(1);
    line = line.min(starts.len() - 1);
    let mut col = offset - starts[line];
    while col > 0 && !text.is_char_boundary(starts[line] + col) {
        col -= 1;
    }
    (line, col)
}

/// Buffer offset for `(line, column-bytes)`, clamped to the line end and
/// onto a char boundary.
pub fn line_col_to_offset(text: &str, starts: &[usize], line: usize, col: usize) -> usize {
    if starts.is_empty() {
        return 0;
    }
    let line = line.min(starts.len() - 1);
    let line_start = starts[line];
    let line_end = starts
        .get(line + 1)
        .map(|end| end - 1)
        .unwrap_or(text.len());
    let mut col = col.min(line_end.saturating_sub(line_start));
    while col > 0 && !text.is_char_boundary(line_start + col) {
        col -= 1;
    }
    line_start + col
}

/// Buffer byte column → display byte column (tabs expand, wide chars pass
/// through byte-identical; only their width differs, which shaping owns).
pub fn buffer_col_to_display_col(line: &str, col: usize) -> usize {
    let col = col.min(line.len());
    let mut display = 0;
    let mut cols = 0;
    for (index, ch) in line.char_indices() {
        if index >= col {
            break;
        }
        if ch == '\t' {
            let stop = TAB_WIDTH - (cols % TAB_WIDTH);
            display += stop;
            cols += stop;
        } else {
            display += ch.len_utf8();
            cols += char_display_width(ch);
        }
    }
    display
}

/// Display columns → buffer byte column: the largest buffer prefix whose
/// display width fits. Tabs consume a full stop, wide chars two columns.
/// Always lands on a char boundary by construction.
pub fn display_col_to_buffer_col(line: &str, display_col: usize) -> usize {
    let mut cols = 0usize;
    let mut buf = 0usize;
    for ch in line.chars() {
        let width = if ch == '\t' {
            TAB_WIDTH - (cols % TAB_WIDTH)
        } else {
            char_display_width(ch)
        };
        if cols + width > display_col {
            break;
        }
        cols += width;
        buf += ch.len_utf8();
    }
    buf
}

/// Shaping returns byte indices in the tab-expanded line, not display cells.
pub fn display_byte_to_buffer_col(line: &str, display_byte: usize) -> usize {
    let mut display = 0;
    let mut cols = 0;
    for (index, ch) in line.char_indices() {
        let bytes = if ch == '\t' {
            TAB_WIDTH - cols % TAB_WIDTH
        } else {
            ch.len_utf8()
        };
        if display + bytes > display_byte {
            return index;
        }
        display += bytes;
        cols += if ch == '\t' {
            bytes
        } else {
            char_display_width(ch)
        };
    }
    line.len()
}

/// Map a buffer byte range within one line to display byte coordinates
/// (tabs expand; everything else is byte-identical). Clamps onto char
/// boundaries; empty when the range misses the line.
pub fn buffer_range_to_display_range(line: &str, start: usize, end: usize) -> (usize, usize) {
    let (start, end) = (start.min(line.len()), end.min(line.len()));
    // Empty range: the display position just before the buffer offset
    // (a tab stop belongs to its tab byte).
    if start == end {
        let mut disp = 0usize;
        let mut cols = 0usize;
        for (index, ch) in line.char_indices() {
            if index >= start {
                break;
            }
            disp += ch.len_utf8();
            if ch == '\t' {
                disp += TAB_WIDTH - (cols % TAB_WIDTH) - 1;
                cols += TAB_WIDTH - (cols % TAB_WIDTH);
            } else {
                cols += char_display_width(ch);
            }
        }
        return (disp, disp);
    }
    let mut disp = 0usize;
    let mut cols = 0usize;
    let mut from = None;
    let mut to = None;
    for (index, ch) in line.char_indices() {
        let next = index + ch.len_utf8();
        let width = if ch == '\t' {
            TAB_WIDTH - (cols % TAB_WIDTH)
        } else {
            ch.len_utf8()
        };
        if index < end && next > start {
            if from.is_none() {
                from = Some(disp);
            }
            to = Some(disp + width);
        }
        disp += width;
        if ch == '\t' {
            cols += width;
        } else {
            cols += char_display_width(ch);
        }
        if index >= end {
            break;
        }
    }
    (from.unwrap_or(0), to.unwrap_or(from.unwrap_or(0)))
}

/// Display-expanded line for rendering and shaping: tabs become spaces to
/// the next stop. The buffer keeps real `\t` bytes; only presentation and
/// width estimates expand them.
pub fn display_line(line: &str) -> std::borrow::Cow<'_, str> {
    if line.contains('\t') {
        let mut out = String::with_capacity(line.len() + 8);
        let mut cols = 0;
        for ch in line.chars() {
            if ch == '\t' {
                let stop = TAB_WIDTH - (cols % TAB_WIDTH);
                out.push_str(&" ".repeat(stop));
                cols += stop;
            } else {
                out.push(ch);
                cols += char_display_width(ch);
            }
        }
        std::borrow::Cow::Owned(out)
    } else {
        std::borrow::Cow::Borrowed(line)
    }
}

/// Router-local identity for one accepted editor filesystem operation. This is
/// intentionally unrelated to terminal-launch receipts.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub(crate) struct EditorOperationId(pub u64);

/// Work performed by the single editor I/O worker. All variants are explicit
/// foreground actions and are dequeued FIFO.
pub(crate) enum EditorIoJob {
    Open {
        path: PathBuf,
    },
    Save {
        document: DocumentId,
        path: PathBuf,
        text: String,
        expected: Option<omaterm_context::FileRevision>,
    },
    Revert {
        document: DocumentId,
        path: PathBuf,
    },
}

impl EditorIoJob {
    fn kind(&self) -> EditorIoKind {
        match self {
            Self::Open { .. } => EditorIoKind::Open,
            Self::Save { .. } => EditorIoKind::Save,
            Self::Revert { .. } => EditorIoKind::Revert,
        }
    }

    fn document(&self) -> Option<DocumentId> {
        match self {
            Self::Open { .. } => None,
            Self::Save { document, .. } | Self::Revert { document, .. } => Some(*document),
        }
    }
}

/// The operation type carried in every owner-side completion.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum EditorIoKind {
    Open,
    Save,
    Revert,
}

/// Metadata captured by the owner before enqueueing filesystem work. The root
/// identity is rechecked by the worker after it captures its own descriptor,
/// so a same-path root replacement cannot retarget an accepted operation.
pub(crate) struct EditorIoRequest {
    pub project: ProjectId,
    pub root_path: PathBuf,
    pub root_identity: omaterm_context::RootIdentity,
    pub generation: u64,
    pub job: EditorIoJob,
}

/// Final successful I/O payload. Revert and open stay distinct so the owner
/// cannot accidentally apply a disk reload as a new document open.
#[derive(Debug)]
pub(crate) enum EditorIoSuccess {
    Opened(omaterm_context::EditorFile),
    Saved(omaterm_context::WriteTextOutcome),
    Reverted(omaterm_context::EditorFile),
}

/// Final failure payload. Cancellation is an outcome, not a silently dropped
/// request, so every accepted operation has exactly one completion.
#[derive(Debug)]
pub(crate) enum EditorIoError {
    Cancelled,
    RootChanged,
    Context(omaterm_context::EditorError),
}

/// One final owner-side message. It contains all identity required to reject a
/// stale completion without consulting worker-local state.
#[derive(Debug)]
pub(crate) struct EditorIoCompletion {
    pub operation: EditorOperationId,
    pub project: ProjectId,
    pub root_path: PathBuf,
    pub root_identity: omaterm_context::RootIdentity,
    pub document: Option<DocumentId>,
    pub generation: u64,
    pub kind: EditorIoKind,
    pub result: Result<EditorIoSuccess, EditorIoError>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum EditorIoSubmitError {
    QueueFull,
    Shutdown,
    OperationIdExhausted,
}

const EDITOR_IO_QUEUE_CAPACITY: usize = 16;

struct QueuedEditorIo {
    operation: EditorOperationId,
    request: EditorIoRequest,
    cancelled: Arc<AtomicBool>,
}

struct ActiveEditorIo {
    operation: EditorOperationId,
    cancelled: Arc<AtomicBool>,
}

struct EditorIoState {
    pending: VecDeque<QueuedEditorIo>,
    active: Option<ActiveEditorIo>,
    completions: VecDeque<EditorIoCompletion>,
    next_operation: Option<u64>,
    shutdown: bool,
}

impl Default for EditorIoState {
    fn default() -> Self {
        Self {
            pending: VecDeque::new(),
            active: None,
            completions: VecDeque::new(),
            next_operation: Some(1),
            shutdown: false,
        }
    }
}

/// One bounded, app-local filesystem worker. It owns neither GPUI state nor
/// the document store: the owner prepares immutable input and later decides
/// whether a completion generation is still current.
pub(crate) struct EditorIoQueue {
    state: Arc<(Mutex<EditorIoState>, Condvar)>,
    thread: Option<std::thread::JoinHandle<()>>,
}

impl EditorIoQueue {
    pub(crate) fn new() -> Self {
        Self::with_runner(run_editor_io)
    }

    fn with_runner(
        mut run: impl FnMut(&EditorIoRequest, &AtomicBool) -> Result<EditorIoSuccess, EditorIoError>
        + Send
        + 'static,
    ) -> Self {
        let state = Arc::new((Mutex::new(EditorIoState::default()), Condvar::new()));
        let worker_state = Arc::clone(&state);
        let thread = std::thread::spawn(move || {
            loop {
                let queued = {
                    let (lock, ready) = &*worker_state;
                    let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                    while state.pending.is_empty() && !state.shutdown {
                        state = ready
                            .wait(state)
                            .unwrap_or_else(|poisoned| poisoned.into_inner());
                    }
                    if state.shutdown {
                        return;
                    }
                    let queued = state
                        .pending
                        .pop_front()
                        .expect("pending editor I/O exists");
                    state.active = Some(ActiveEditorIo {
                        operation: queued.operation,
                        cancelled: Arc::clone(&queued.cancelled),
                    });
                    queued
                };

                let result = run(&queued.request, &queued.cancelled);
                let completion = completion_for(queued.operation, queued.request, result);
                let (lock, ready) = &*worker_state;
                let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                if state
                    .active
                    .as_ref()
                    .is_some_and(|active| active.operation == completion.operation)
                {
                    state.active = None;
                }
                state.completions.push_back(completion);
                ready.notify_all();
            }
        });
        Self {
            state,
            thread: Some(thread),
        }
    }

    /// Reserves a foreground slot before taking ownership of the request; the
    /// queue never copies a save payload. A full queue excludes the active job.
    pub(crate) fn submit(
        &self,
        request: EditorIoRequest,
    ) -> Result<EditorOperationId, EditorIoSubmitError> {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if state.shutdown {
            return Err(EditorIoSubmitError::Shutdown);
        }
        if state.pending.len() == EDITOR_IO_QUEUE_CAPACITY {
            return Err(EditorIoSubmitError::QueueFull);
        }
        let operation = EditorOperationId(
            state
                .next_operation
                .ok_or(EditorIoSubmitError::OperationIdExhausted)?,
        );
        state.next_operation = operation.0.checked_add(1);
        state.pending.push_back(QueuedEditorIo {
            operation,
            request,
            cancelled: Arc::new(AtomicBool::new(false)),
        });
        ready.notify_one();
        Ok(operation)
    }

    /// Cancel a queued operation immediately, or request cooperative
    /// cancellation from the active worker. Queued cancellations publish their
    /// final result synchronously; active operations publish at their next
    /// cancellation boundary.
    pub(crate) fn cancel(&self, operation: EditorOperationId) -> bool {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if let Some(index) = state
            .pending
            .iter()
            .position(|queued| queued.operation == operation)
        {
            let queued = state
                .pending
                .remove(index)
                .expect("queued editor I/O exists");
            queued.cancelled.store(true, Ordering::Release);
            state.completions.push_back(completion_for(
                queued.operation,
                queued.request,
                Err(EditorIoError::Cancelled),
            ));
            ready.notify_all();
            return true;
        }
        if let Some(active) = &state.active
            && active.operation == operation
        {
            active.cancelled.store(true, Ordering::Release);
            return true;
        }
        false
    }

    pub(crate) fn take_completion(&self) -> Option<EditorIoCompletion> {
        self.state
            .0
            .lock()
            .ok()
            .and_then(|mut state| state.completions.pop_front())
    }

    /// Stop accepting work, cancel every queued operation, request cooperative
    /// cancellation from the active one, and wake the worker. Call
    /// `shutdown_and_join` to wait for the final active completion.
    pub(crate) fn shutdown(&self) {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if state.shutdown {
            return;
        }
        state.shutdown = true;
        if let Some(active) = &state.active {
            active.cancelled.store(true, Ordering::Release);
        }
        while let Some(queued) = state.pending.pop_front() {
            queued.cancelled.store(true, Ordering::Release);
            state.completions.push_back(completion_for(
                queued.operation,
                queued.request,
                Err(EditorIoError::Cancelled),
            ));
        }
        ready.notify_all();
    }

    /// Clean shutdown boundary: no worker is detached. A filesystem syscall
    /// already in progress remains subject to platform blocking semantics, but
    /// all cancellable boundaries are signalled before joining.
    pub(crate) fn shutdown_and_join(&mut self) -> std::thread::Result<()> {
        self.shutdown();
        if let Some(thread) = self.thread.take() {
            thread.join()
        } else {
            Ok(())
        }
    }

    #[cfg(test)]
    fn completion_count(&self) -> usize {
        self.state
            .0
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .completions
            .len()
    }

    #[cfg(test)]
    fn wait_for_completion_count(&self, count: usize) {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        while state.completions.len() < count {
            let (next, timeout) = ready
                .wait_timeout(state, std::time::Duration::from_secs(5))
                .unwrap_or_else(|poisoned| poisoned.into_inner());
            state = next;
            assert!(
                !timeout.timed_out(),
                "editor I/O worker did not complete in time"
            );
        }
    }
}

impl Default for EditorIoQueue {
    fn default() -> Self {
        Self::new()
    }
}

impl Drop for EditorIoQueue {
    fn drop(&mut self) {
        let _ = self.shutdown_and_join();
    }
}

fn completion_for(
    operation: EditorOperationId,
    request: EditorIoRequest,
    result: Result<EditorIoSuccess, EditorIoError>,
) -> EditorIoCompletion {
    EditorIoCompletion {
        operation,
        project: request.project,
        root_path: request.root_path,
        root_identity: request.root_identity,
        document: request.job.document(),
        generation: request.generation,
        kind: request.job.kind(),
        result,
    }
}

fn run_editor_io(
    request: &EditorIoRequest,
    cancelled: &AtomicBool,
) -> Result<EditorIoSuccess, EditorIoError> {
    if cancelled.load(Ordering::Acquire) {
        return Err(EditorIoError::Cancelled);
    }
    let root =
        omaterm_context::EditorRoot::open(&request.root_path).map_err(EditorIoError::Context)?;
    if root.identity() != request.root_identity {
        return Err(EditorIoError::RootChanged);
    }
    if cancelled.load(Ordering::Acquire) {
        return Err(EditorIoError::Cancelled);
    }
    match &request.job {
        EditorIoJob::Open { path } => {
            let file = omaterm_context::read_text_file_from_root(&root, path)
                .map_err(EditorIoError::Context)?;
            if cancelled.load(Ordering::Acquire) {
                Err(EditorIoError::Cancelled)
            } else {
                Ok(EditorIoSuccess::Opened(file))
            }
        }
        EditorIoJob::Save {
            path,
            text,
            expected,
            ..
        } => {
            // Once write begins it may pass rename; a late cancellation must
            // not hide that durable filesystem outcome from the owner.
            if cancelled.load(Ordering::Acquire) {
                return Err(EditorIoError::Cancelled);
            }
            omaterm_context::write_text_file_from_root(&root, path, text, expected.as_ref())
                .map(EditorIoSuccess::Saved)
                .map_err(EditorIoError::Context)
        }
        EditorIoJob::Revert { path, .. } => {
            let file = omaterm_context::read_text_file_from_root(&root, path)
                .map_err(EditorIoError::Context)?;
            if cancelled.load(Ordering::Acquire) {
                Err(EditorIoError::Cancelled)
            } else {
                Ok(EditorIoSuccess::Reverted(file))
            }
        }
    }
}

/// One background highlight job plus one replaceable pending request,
/// mirroring the diff/palette worker pattern. Superseding keystrokes cancel
/// the active tokenize; a cancelled job never refills the mailbox.
pub struct HighlightRequest {
    pub document: omaterm_core::DocumentId,
    pub generation: u64,
    pub language: omaterm_context::EditorLanguage,
    pub text: String,
    pub cancelled: std::sync::Arc<std::sync::atomic::AtomicBool>,
}

#[derive(Debug)]
pub struct HighlightResult {
    pub document: omaterm_core::DocumentId,
    pub generation: u64,
    pub spans: Vec<TokenSpan>,
    pub max_cols: usize,
}

#[derive(Default)]
struct HighlightWorkerState {
    pending: Option<HighlightRequest>,
    active_cancel: Option<std::sync::Arc<std::sync::atomic::AtomicBool>>,
    result: Option<HighlightResult>,
    shutdown: bool,
}

pub struct HighlightWorker {
    state: std::sync::Arc<(std::sync::Mutex<HighlightWorkerState>, std::sync::Condvar)>,
    thread: Option<std::thread::JoinHandle<()>>,
}

impl HighlightWorker {
    pub fn new() -> Self {
        Self::with_notifier(|| {})
    }

    pub fn with_notifier(notify: impl Fn() + Send + 'static) -> Self {
        Self::with_runner(
            |request: HighlightRequest| {
                let spans =
                    tokenize_cancellable(request.language, &request.text, Some(&request.cancelled));
                let mut max_cols = 0;
                for line in request.text.split('\n') {
                    if request.cancelled.load(std::sync::atomic::Ordering::Relaxed) {
                        break;
                    }
                    max_cols = max_cols.max(line_display_width(line));
                }
                HighlightResult {
                    document: request.document,
                    generation: request.generation,
                    spans,
                    max_cols,
                }
            },
            notify,
        )
    }

    fn with_runner(
        mut run: impl FnMut(HighlightRequest) -> HighlightResult + Send + 'static,
        notify: impl Fn() + Send + 'static,
    ) -> Self {
        let state = std::sync::Arc::new((
            std::sync::Mutex::new(HighlightWorkerState::default()),
            std::sync::Condvar::new(),
        ));
        let worker_state = std::sync::Arc::clone(&state);
        let thread = std::thread::spawn(move || {
            loop {
                let request = {
                    let (lock, ready) = &*worker_state;
                    let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                    while state.pending.is_none() && !state.shutdown {
                        state = ready
                            .wait(state)
                            .unwrap_or_else(|poisoned| poisoned.into_inner());
                    }
                    if state.shutdown {
                        return;
                    }
                    let request = state.pending.take().expect("pending highlight exists");
                    state.active_cancel = Some(std::sync::Arc::clone(&request.cancelled));
                    request
                };
                let cancel = std::sync::Arc::clone(&request.cancelled);
                let landed = run(request);
                {
                    let (lock, _) = &*worker_state;
                    let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                    if state
                        .active_cancel
                        .as_ref()
                        .is_some_and(|active| std::sync::Arc::ptr_eq(active, &cancel))
                    {
                        state.active_cancel = None;
                    }
                    if !state.shutdown && !cancel.load(std::sync::atomic::Ordering::Acquire) {
                        state.result = Some(landed);
                        notify();
                    }
                }
            }
        });
        Self {
            state,
            thread: Some(thread),
        }
    }

    pub fn submit(&self, mut request: HighlightRequest) {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if state.shutdown {
            return;
        }
        if let Some(active) = &state.active_cancel {
            active.store(true, std::sync::atomic::Ordering::Release);
        }
        request.cancelled = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
        state.pending = Some(request);
        state.result = None;
        ready.notify_one();
    }

    pub fn cancel(&self) {
        let (lock, _) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if let Some(active) = &state.active_cancel {
            active.store(true, std::sync::atomic::Ordering::Release);
        }
        state.pending = None;
        state.result = None;
    }

    pub fn shutdown(&self) {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        state.shutdown = true;
        if let Some(active) = &state.active_cancel {
            active.store(true, std::sync::atomic::Ordering::Release);
        }
        state.pending = None;
        state.result = None;
        ready.notify_one();
    }

    /// Transfer the join to shutdown cleanup, like the diff worker.
    pub fn take_shutdown_thread(&mut self) -> Option<std::thread::JoinHandle<()>> {
        self.shutdown();
        self.thread.take()
    }

    pub fn take_result(&self) -> Option<HighlightResult> {
        self.state
            .0
            .lock()
            .ok()
            .and_then(|mut state| state.result.take())
    }
}

impl Default for HighlightWorker {
    fn default() -> Self {
        Self::new()
    }
}

impl Drop for HighlightWorker {
    fn drop(&mut self) {
        self.shutdown();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn open_doc(
        store: &mut DocumentStore,
        project: ProjectId,
        name: &str,
        text: &str,
    ) -> DocumentId {
        let file_inode = name.as_bytes().iter().fold(0_u64, |hash, byte| {
            hash.wrapping_mul(31).wrapping_add(u64::from(*byte))
        });
        let revision = omaterm_context::FileRevision {
            size: text.len() as u64,
            mtime_secs: 1,
            mtime_nanos: 0,
            device: 1,
            inode: file_inode,
            content_digest: [0; 32],
        };
        store.open(
            project,
            PathBuf::from(name),
            PathBuf::from("/repo"),
            omaterm_context::RootIdentity {
                device: 1,
                inode: 1,
            },
            omaterm_context::EditorFile {
                text: text.into(),
                bytes: text.len(),
                lines: count_lines(text),
                revision,
                language: omaterm_context::EditorLanguage::Plain,
            },
        )
    }

    #[test]
    fn undo_tracks_savepoints_and_returns_a_valid_caret() {
        let mut store = DocumentStore::default();
        let id = open_doc(&mut store, ProjectId::new(), "a.txt", "界");
        store.apply_edit(id, 3, 0, "e\u{301}").unwrap();
        assert!(store.undo(id).unwrap());
        assert_eq!(store.is_dirty(id), Some(false));
        assert_eq!(store.history_cursor(id), Some(3));
        assert!(store.redo(id).unwrap());
        assert_eq!(store.history_cursor(id), Some(6));
        store.mark_saved(id, store.revision(id).unwrap());
        assert!(store.undo(id).unwrap());
        assert_eq!(store.is_dirty(id), Some(true));
        assert!(store.redo(id).unwrap());
        assert_eq!(store.is_dirty(id), Some(false));
        store.apply_edit(id, 0, 0, "").unwrap();
        assert_eq!(store.is_dirty(id), Some(false));
        store.apply_edit(id, 0, 0, "x").unwrap();
        store.discard_changes(id);
        assert_eq!(store.text(id), Some("界e\u{301}"));
        assert!(!store.has_dirty_documents());
        assert!(!store.undo(id).unwrap());
    }

    #[test]
    fn unicode_shaping_offsets_and_grapheme_motion_do_not_split_text() {
        let line = "界\te\u{301}🙂x";
        for (buffer, _) in line.char_indices() {
            let display = buffer_col_to_display_col(line, buffer);
            assert_eq!(display_byte_to_buffer_col(line, display), buffer);
        }
        assert_eq!(
            display_byte_to_buffer_col(line, display_line(line).len()),
            line.len()
        );
        assert_eq!(line_display_width("e\u{301}"), 1);
        let text = "e\u{301}👩‍💻界";
        assert_eq!(next_grapheme(text, 0), "e\u{301}".len());
        assert_eq!(next_grapheme(text, 3), "e\u{301}👩‍💻".len());
        assert_eq!(previous_grapheme(text, text.len()), "e\u{301}👩‍💻".len());
        let mut caret = EditorCaret {
            cursor: usize::MAX,
            anchor: Some(1),
        };
        caret.clamp(text);
        assert_eq!(caret.cursor, text.len());
        assert_eq!(caret.anchor, Some(0));
    }

    #[test]
    fn tokenizer_negative_numbers_escaped_unicode_and_cancellation() {
        use omaterm_context::EditorLanguage as Lang;
        let text = "{\"n\": -12, \"s\": \"\\界\"}";
        let spans = tokenize(Lang::Json, text);
        assert!(spans.iter().any(|span| span_text(text, span) == "-12"));
        let text = "let x = '\\n'; let y = '界'; let s = \"\\界\";";
        let spans = tokenize(Lang::Rust, text);
        assert!(spans.iter().any(|span| span_text(text, span) == "'\\n'"));
        assert!(spans.iter().any(|span| span_text(text, span) == "'界'"));
        let cancel = std::sync::atomic::AtomicBool::new(true);
        for lang in [Lang::Rust, Lang::Json, Lang::Markdown] {
            assert!(tokenize_cancellable(lang, text, Some(&cancel)).is_empty());
        }
    }

    #[test]
    fn reopen_returns_the_live_buffer_without_forking() {
        let mut store = DocumentStore::default();
        let project = ProjectId::new();
        let first = open_doc(&mut store, project, "a.md", "# v1\n");
        let second = open_doc(&mut store, project, "a.md", "# v1\n");
        assert_eq!(first, second);
        assert_eq!(store.text(first), Some("# v1\n"));
        assert_eq!(store.is_dirty(first), Some(false));
        // Same path in another project is a separate document.
        let other = open_doc(&mut store, ProjectId::new(), "a.md", "# v1\n");
        assert_ne!(first, other);
    }

    #[test]
    fn edits_are_undoable_redoable_and_bounded() {
        let mut store = DocumentStore::default();
        let id = open_doc(&mut store, ProjectId::new(), "a.txt", "hello");
        store.apply_edit(id, 5, 0, " world").unwrap();
        assert_eq!(store.text(id), Some("hello world"));
        assert_eq!(store.is_dirty(id), Some(true));
        assert!(store.undo(id).unwrap());
        assert_eq!(store.text(id), Some("hello"));
        assert!(store.redo(id).unwrap());
        assert_eq!(store.text(id), Some("hello world"));
        // Redo clears on a new edit.
        assert!(store.undo(id).unwrap());
        store.apply_edit(id, 0, 5, "bye").unwrap();
        assert!(!store.redo(id).unwrap());
        assert_eq!(store.text(id), Some("bye"));
        // Invalid ranges never mutate.
        assert!(store.apply_edit(id, 99, 1, "x").is_err());
        assert!(store.apply_edit(id, 0, 99, "x").is_err());
        assert_eq!(store.text(id), Some("bye"));
        // Unknown documents fail with a stable code.
        assert!(matches!(
            store.apply_edit(DocumentId::new(), 0, 0, "x"),
            Err(error) if error.code == ErrorCode::DocumentNotOpen
        ));
    }

    #[test]
    fn undo_history_drops_oldest_first_under_caps() {
        let mut store = DocumentStore::default();
        let id = open_doc(&mut store, ProjectId::new(), "a.txt", "");
        for index in 0..(MAX_UNDO_ENTRIES + 10) {
            store.apply_edit(id, 0, 0, &format!("{index}\n")).unwrap();
        }
        let doc = store.get(id).unwrap();
        assert_eq!(doc.undo.len(), MAX_UNDO_ENTRIES);
        assert!(doc.undo_bytes <= MAX_UNDO_BYTES + 64);
        // Multibyte boundaries are enforced, not split.
        assert!(store.apply_edit(id, 1, 0, "界").is_ok());
        assert!(store.apply_edit(id, 1, 1, "").is_err());
    }

    fn span_text<'a>(text: &'a str, span: &TokenSpan) -> &'a str {
        &text[span.start..span.start + span.len]
    }

    #[test]
    fn rust_tokens_cover_keywords_strings_comments_numbers_and_chars() {
        use omaterm_context::EditorLanguage as Lang;
        let text = "fn main() {\n    // greet 界\n    let s = \"hi \\\"you\\\"\";\n    let c = 'x';\n    let life = 'a;\n    let n = 42;\n}\n";
        let spans = tokenize(Lang::Rust, text);
        let kinds: Vec<(&str, TokenKind)> = spans
            .iter()
            .map(|span| (span_text(text, span), span.kind))
            .collect();
        assert!(kinds.contains(&("fn", TokenKind::Keyword)));
        assert!(kinds.contains(&("let", TokenKind::Keyword)));
        assert!(kinds.contains(&("// greet 界", TokenKind::Comment)));
        assert!(kinds.contains(&("\"hi \\\"you\\\"\"", TokenKind::String)));
        assert!(kinds.contains(&("'x'", TokenKind::String)));
        assert!(kinds.contains(&("42", TokenKind::Number)));
        // A lifetime is not a char literal: no string span starts at it.
        let life_at = text.find("'a").unwrap();
        assert!(!spans.iter().any(|span| span.start == life_at));
        // Sorted, non-overlapping, char-boundary aligned.
        let mut end = 0;
        for span in &spans {
            assert!(span.start >= end);
            assert!(text.is_char_boundary(span.start));
            assert!(text.is_char_boundary(span.start + span.len));
            end = span.start + span.len;
        }
    }

    #[test]
    fn unclosed_constructs_run_to_end_without_panicking() {
        use omaterm_context::EditorLanguage as Lang;
        let text = "let s = \"closed\";\n/* open comment 界";
        let spans = tokenize(Lang::Rust, text);
        assert_eq!(spans.len(), 3); // let, string, comment-to-EOF
        assert_eq!(spans[2].kind, TokenKind::Comment);
        assert_eq!(spans[2].start + spans[2].len, text.len());
        // An unterminated string swallows the rest, including `/*`.
        let text = "let s = \"never closed;\n/* not a comment";
        let spans = tokenize(Lang::Rust, text);
        assert_eq!(spans.len(), 2);
        assert_eq!(spans[1].kind, TokenKind::String);
        assert_eq!(spans[1].start + spans[1].len, text.len());
    }

    #[test]
    fn bash_toml_json_and_markdown_rules_hold() {
        use omaterm_context::EditorLanguage as Lang;
        let bash = "# comment\nif [ $# -gt 0 ]; then echo 'it' \"works $HOME\"; fi\n";
        let spans = tokenize(Lang::Bash, bash);
        let kinds: Vec<(&str, TokenKind)> = spans
            .iter()
            .map(|span| (span_text(bash, span), span.kind))
            .collect();
        assert!(kinds.contains(&("# comment", TokenKind::Comment)));
        assert!(kinds.contains(&("if", TokenKind::Keyword)));
        assert!(kinds.contains(&("'it'", TokenKind::String)));
        // `$#` must not start a comment: later tokens still classify.
        assert!(kinds.iter().any(|(text, _)| text.contains("works")));
        assert_eq!(tokenize(Lang::Plain, bash), Vec::new());

        let toml = "# heading\ntitle = \"demo\" # trailing\ncount = 7\n";
        let spans = tokenize(Lang::Toml, toml);
        assert!(spans.iter().any(|span| span.kind == TokenKind::Comment));
        assert!(spans.iter().any(|span| span.kind == TokenKind::String));
        assert!(spans.iter().any(|span| span.kind == TokenKind::Number));

        let json = "{\"a\": [1, true, null]}";
        let spans = tokenize(Lang::Json, json);
        let kinds: Vec<(&str, TokenKind)> = spans
            .iter()
            .map(|span| (span_text(json, span), span.kind))
            .collect();
        assert!(kinds.contains(&("\"a\"", TokenKind::String)));
        assert!(kinds.contains(&("1", TokenKind::Number)));
        assert!(kinds.contains(&("true", TokenKind::Keyword)));
        assert!(kinds.contains(&("null", TokenKind::Keyword)));

        let md = "# Title 界\n\nSome `code` here\n\n```\nblock stays plain\n```\n";
        let spans = tokenize(Lang::Markdown, md);
        assert!(
            spans.iter().any(|span| span.kind == TokenKind::Keyword
                && span_text(md, span).starts_with("# Title"))
        );
        assert!(
            spans
                .iter()
                .any(|span| span.kind == TokenKind::String && span_text(md, span) == "`code`")
        );
    }

    #[test]
    fn caret_selection_orders_and_collapses() {
        let mut caret = EditorCaret::default();
        assert_eq!(caret.selection_range(), None);
        caret.cursor = 5;
        caret.anchor = Some(2);
        assert_eq!(caret.selection_range(), Some((2, 5)));
        caret.anchor = Some(9);
        assert_eq!(caret.selection_range(), Some((5, 9)));
        caret.collapse_to(3);
        assert_eq!(caret.selection_range(), None);
        assert_eq!(caret.cursor, 3);
    }

    #[test]
    fn display_width_expands_tabs_and_counts_wide_chars() {
        assert_eq!(line_display_width("ab"), 2);
        assert_eq!(line_display_width("a\tb"), 5); // tab to col 4, then b
        assert_eq!(line_display_width("\t"), 4);
        assert_eq!(line_display_width("界x"), 3);
        assert_eq!(line_display_width(""), 0);
    }

    #[test]
    fn highlight_worker_keeps_only_the_latest_request() {
        use omaterm_context::EditorLanguage as Lang;
        let worker = HighlightWorker::new();
        let document = DocumentId::new();
        let request = |generation: u64, text: &str| HighlightRequest {
            document,
            generation,
            language: Lang::Rust,
            text: text.into(),
            cancelled: std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false)),
        };
        worker.submit(request(1, "fn one() {}"));
        worker.submit(request(2, "fn two() {}"));
        let start = std::time::Instant::now();
        loop {
            if let Some(result) = worker.take_result()
                && result.generation == 2
            {
                assert_eq!(result.document, document);
                assert!(!result.spans.is_empty());
                break;
            }
            assert!(start.elapsed() < std::time::Duration::from_secs(5));
            std::thread::sleep(std::time::Duration::from_millis(5));
        }
        // Shutdown with queued work publishes nothing.
        worker.submit(request(3, "fn three() {}"));
        let mut worker = worker;
        let thread = worker.take_shutdown_thread().unwrap();
        thread.join().unwrap();
        assert!(worker.take_result().is_none());
    }

    #[test]
    fn line_map_round_trips_and_clamps_unicode() {
        let text = "ab\n界x\n\nlast";
        let starts = line_starts(text);
        assert_eq!(starts, vec![0, 3, 3 + "界x\n".len(), 3 + "界x\n".len() + 1]);
        assert_eq!(offset_to_line_col(&starts, text, 0), (0, 0));
        assert_eq!(offset_to_line_col(&starts, text, 2), (0, 2));
        assert_eq!(offset_to_line_col(&starts, text, 999), (3, 4));
        // Mid-character offsets clamp back to a boundary.
        let mid_cjk = 3 + 1;
        assert_eq!(offset_to_line_col(&starts, text, mid_cjk), (1, 0));
        assert_eq!(line_col_to_offset(text, &starts, 1, 3), 3 + "界".len());
        assert_eq!(line_col_to_offset(text, &starts, 1, 99), 3 + "界x".len());
        assert_eq!(line_col_to_offset(text, &starts, 99, 0), text.len() - 4);
        assert_eq!(line_col_to_offset("", &line_starts(""), 0, 5), 0);
        // Display expansion keeps buffer bytes intact.
        assert_eq!(display_line("a\tb").as_ref(), "a   b");
        assert_eq!(display_line("plain"), "plain");
    }

    #[test]
    fn buffer_and_display_columns_map_through_tabs_and_wide_chars() {
        // "a\t界x": a=1 col, tab stop to 4, 界=2 cols, x=1 col.
        let line = "a\t界x";
        assert_eq!(buffer_col_to_display_col(line, 0), 0);
        assert_eq!(buffer_col_to_display_col(line, 1), 1);
        assert_eq!(buffer_col_to_display_col(line, 2), 4);
        assert_eq!(display_col_to_buffer_col(line, 0), 0);
        assert_eq!(display_col_to_buffer_col(line, 1), 1);
        // Display cols 2..4 land inside the tab stop: the tab byte.
        assert_eq!(display_col_to_buffer_col(line, 2), 1);
        assert_eq!(display_col_to_buffer_col(line, 3), 1);
        assert_eq!(display_col_to_buffer_col(line, 4), 2);
        // Display col 5 lands inside the wide char: its first byte.
        assert_eq!(display_col_to_buffer_col(line, 5), 2);
        assert_eq!(display_col_to_buffer_col(line, 6), 2 + "界".len());
        assert_eq!(display_col_to_buffer_col(line, 99), line.len());
        assert_eq!(display_col_to_buffer_col("", 3), 0);
    }

    #[test]
    fn buffer_ranges_map_to_display_through_tabs() {
        let line = "a\txy";
        // Whole line and token-aligned sub-ranges.
        assert_eq!(buffer_range_to_display_range(line, 0, 4), (0, 6));
        assert_eq!(buffer_range_to_display_range(line, 0, 1), (0, 1));
        assert_eq!(buffer_range_to_display_range(line, 2, 4), (4, 6));
        assert_eq!(buffer_range_to_display_range(line, 3, 3), (5, 5));
    }

    #[test]
    fn close_project_drops_only_owned_documents() {
        let mut store = DocumentStore::default();
        let project = ProjectId::new();
        let first = open_doc(&mut store, project, "a.txt", "a");
        let second = open_doc(&mut store, project, "b.txt", "b");
        let other = open_doc(&mut store, ProjectId::new(), "a.txt", "a");
        assert_eq!(store.close_project(project), 2);
        assert!(store.get(first).is_none());
        assert!(store.get(second).is_none());
        assert!(store.get(other).is_some());
        assert!(!store.remove(first));
    }

    fn io_request(generation: u64, job: EditorIoJob) -> EditorIoRequest {
        EditorIoRequest {
            project: ProjectId::new(),
            root_path: PathBuf::from("/test-root"),
            root_identity: omaterm_context::RootIdentity {
                device: 7,
                inode: 11,
            },
            generation,
            job,
        }
    }

    fn open_job(index: u64) -> EditorIoJob {
        EditorIoJob::Open {
            path: PathBuf::from(format!("document-{index}.txt")),
        }
    }

    fn test_io_success(request: &EditorIoRequest) -> Result<EditorIoSuccess, EditorIoError> {
        let revision = omaterm_context::FileRevision {
            size: 0,
            mtime_secs: 0,
            mtime_nanos: 0,
            device: 1,
            inode: 1,
            content_digest: [0; 32],
        };
        let file = || omaterm_context::EditorFile {
            text: String::new(),
            bytes: 0,
            lines: 0,
            revision,
            language: omaterm_context::EditorLanguage::Plain,
        };
        Ok(match request.job.kind() {
            EditorIoKind::Open => EditorIoSuccess::Opened(file()),
            EditorIoKind::Save => {
                EditorIoSuccess::Saved(omaterm_context::WriteTextOutcome::CommittedDurable {
                    revision,
                })
            }
            EditorIoKind::Revert => EditorIoSuccess::Reverted(file()),
        })
    }

    struct IoGate {
        started: Mutex<bool>,
        release: Condvar,
    }

    impl IoGate {
        fn wait_started(&self) {
            let mut started = self
                .started
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner());
            while !*started {
                started = self
                    .release
                    .wait(started)
                    .unwrap_or_else(|poisoned| poisoned.into_inner());
            }
        }

        fn release(&self) {
            let mut started = self
                .started
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner());
            *started = false;
            self.release.notify_all();
        }
    }

    #[test]
    fn editor_io_queue_bounds_pending_foreground_jobs() {
        let gate = Arc::new(IoGate {
            started: Mutex::new(false),
            release: Condvar::new(),
        });
        let runner_gate = Arc::clone(&gate);
        let mut queue = EditorIoQueue::with_runner(move |request, cancelled| {
            let mut started = runner_gate
                .started
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner());
            *started = true;
            runner_gate.release.notify_all();
            while *started {
                started = runner_gate
                    .release
                    .wait(started)
                    .unwrap_or_else(|poisoned| poisoned.into_inner());
            }
            if cancelled.load(Ordering::Acquire) {
                Err(EditorIoError::Cancelled)
            } else {
                test_io_success(request)
            }
        });
        let first = queue.submit(io_request(1, open_job(0))).unwrap();
        gate.wait_started();
        for index in 1..=EDITOR_IO_QUEUE_CAPACITY {
            queue
                .submit(io_request(index as u64 + 1, open_job(index as u64)))
                .unwrap();
        }
        assert_eq!(
            queue.submit(io_request(99, open_job(99))),
            Err(EditorIoSubmitError::QueueFull)
        );
        assert!(queue.cancel(first));
        gate.release();
        assert!(queue.shutdown_and_join().is_ok());
        assert_eq!(queue.completion_count(), EDITOR_IO_QUEUE_CAPACITY + 1);
    }

    #[test]
    fn editor_io_queue_cancels_queued_job_once_without_running_it() {
        let gate = Arc::new(IoGate {
            started: Mutex::new(false),
            release: Condvar::new(),
        });
        let runner_gate = Arc::clone(&gate);
        let ran = Arc::new(Mutex::new(Vec::new()));
        let runner_ran = Arc::clone(&ran);
        let mut queue = EditorIoQueue::with_runner(move |request, _| {
            runner_ran
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .push(request.generation);
            let mut started = runner_gate
                .started
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner());
            *started = true;
            runner_gate.release.notify_all();
            while *started {
                started = runner_gate
                    .release
                    .wait(started)
                    .unwrap_or_else(|poisoned| poisoned.into_inner());
            }
            test_io_success(request)
        });
        let first = queue.submit(io_request(1, open_job(1))).unwrap();
        gate.wait_started();
        let cancelled = queue.submit(io_request(2, open_job(2))).unwrap();
        assert!(queue.cancel(cancelled));
        assert!(!queue.cancel(cancelled));
        gate.release();
        queue.wait_for_completion_count(2);
        assert!(queue.shutdown_and_join().is_ok());

        let mut completions = [
            queue.take_completion().unwrap(),
            queue.take_completion().unwrap(),
        ];
        completions.sort_by_key(|completion| completion.operation);
        assert_eq!(completions[0].operation, first);
        assert!(matches!(
            completions[0].result,
            Ok(EditorIoSuccess::Opened(_))
        ));
        assert_eq!(completions[1].operation, cancelled);
        assert!(matches!(
            completions[1].result,
            Err(EditorIoError::Cancelled)
        ));
        assert_eq!(
            *ran.lock().unwrap_or_else(|poisoned| poisoned.into_inner()),
            vec![1]
        );
        assert!(queue.take_completion().is_none());
    }

    #[test]
    fn editor_io_queue_delivers_latest_completion_once_with_its_metadata() {
        let mut queue = EditorIoQueue::with_runner(|request, _| test_io_success(request));
        let project = ProjectId::new();
        let root = omaterm_context::RootIdentity {
            device: 9,
            inode: 12,
        };
        let first = queue
            .submit(EditorIoRequest {
                project,
                root_path: PathBuf::from("/project"),
                root_identity: root,
                generation: 41,
                job: open_job(1),
            })
            .unwrap();
        let document = DocumentId::new();
        let latest = queue
            .submit(EditorIoRequest {
                project,
                root_path: PathBuf::from("/project"),
                root_identity: root,
                generation: 42,
                job: EditorIoJob::Revert {
                    document,
                    path: PathBuf::from("document-2.txt"),
                },
            })
            .unwrap();
        queue.wait_for_completion_count(2);
        assert!(queue.shutdown_and_join().is_ok());

        let first_completion = queue.take_completion().unwrap();
        let latest_completion = queue.take_completion().unwrap();
        assert_eq!(first_completion.operation, first);
        assert_eq!(latest_completion.operation, latest);
        assert_eq!(latest_completion.project, project);
        assert_eq!(latest_completion.root_path, PathBuf::from("/project"));
        assert_eq!(latest_completion.root_identity, root);
        assert_eq!(latest_completion.document, Some(document));
        assert_eq!(latest_completion.generation, 42);
        assert_eq!(latest_completion.kind, EditorIoKind::Revert);
        assert!(matches!(
            latest_completion.result,
            Ok(EditorIoSuccess::Reverted(_))
        ));
        assert!(queue.take_completion().is_none());
    }

    #[test]
    fn editor_io_queue_shutdown_cancels_pending_and_joins_active_worker() {
        let started = Arc::new((Mutex::new(false), Condvar::new()));
        let runner_started = Arc::clone(&started);
        let mut queue = EditorIoQueue::with_runner(move |_, cancelled| {
            let (lock, ready) = &*runner_started;
            *lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner()) = true;
            ready.notify_all();
            while !cancelled.load(Ordering::Acquire) {
                std::thread::sleep(std::time::Duration::from_millis(1));
            }
            Err(EditorIoError::Cancelled)
        });
        let active = queue.submit(io_request(1, open_job(1))).unwrap();
        let (lock, ready) = &*started;
        let mut active_started = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        while !*active_started {
            active_started = ready
                .wait(active_started)
                .unwrap_or_else(|poisoned| poisoned.into_inner());
        }
        drop(active_started);
        let queued = queue.submit(io_request(2, open_job(2))).unwrap();
        assert!(queue.shutdown_and_join().is_ok());
        assert_eq!(queue.completion_count(), 2);
        assert_eq!(
            queue.submit(io_request(3, open_job(3))),
            Err(EditorIoSubmitError::Shutdown)
        );

        let mut operations = [
            queue.take_completion().unwrap(),
            queue.take_completion().unwrap(),
        ];
        operations.sort_by_key(|completion| completion.operation);
        assert_eq!(operations[0].operation, active);
        assert_eq!(operations[1].operation, queued);
        assert!(
            operations
                .iter()
                .all(|completion| matches!(completion.result, Err(EditorIoError::Cancelled)))
        );
    }
}
