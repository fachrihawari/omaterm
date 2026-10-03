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
//! - One buffer per (project, canonical path); reopening returns the live
//!   document instead of forking a second buffer.
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

use std::collections::HashMap;
use std::path::PathBuf;

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
    /// Canonical absolute path at open time (dedup form).
    absolute: PathBuf,
    /// Canonical project root at open time. Saves re-resolve the live root
    /// and refuse on mismatch instead of writing into a replaced tree.
    root: PathBuf,
    text: String,
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
    by_path: HashMap<(ProjectId, PathBuf), DocumentId>,
}

impl DocumentStore {
    /// Open a buffer over an already-validated read. Returns the live
    /// document when the same (project, canonical path) is already open.
    pub fn open(
        &mut self,
        project: ProjectId,
        path: PathBuf,
        absolute: PathBuf,
        root: PathBuf,
        file: omaterm_context::EditorFile,
    ) -> DocumentId {
        if let Some(id) = self.by_path.get(&(project, absolute.clone())) {
            return *id;
        }
        let id = DocumentId::new();
        let language = file.language;
        let revision = file.revision;
        self.by_path.insert((project, absolute.clone()), id);
        self.docs.insert(
            id,
            Document {
                id,
                project,
                path,
                absolute,
                root,
                text: file.text,
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

    pub fn language(&self, document: DocumentId) -> Option<omaterm_context::EditorLanguage> {
        self.docs.get(&document).map(|doc| doc.language)
    }

    pub fn remove(&mut self, document: DocumentId) -> bool {
        let Some(doc) = self.docs.remove(&document) else {
            return false;
        };
        self.by_path.remove(&(doc.project, doc.absolute));
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
        let mut next = String::with_capacity(doc.text.len() - remove_len + insert.len());
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
        doc.dirty = true;
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
        doc.dirty = true;
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
        doc.dirty = true;
        Ok(true)
    }

    /// Replace buffer text from a fresh disk read (revert path): clears
    /// undo/redo, clears dirty, adopts the new revision.
    pub fn adopt_disk_text(&mut self, document: DocumentId, file: omaterm_context::EditorFile) {
        if let Some(doc) = self.docs.get_mut(&document) {
            doc.text = file.text;
            doc.revision = file.revision;
            doc.language = file.language;
            doc.undo.clear();
            doc.undo_bytes = 0;
            doc.redo.clear();
            doc.dirty = false;
        }
    }

    /// Record a successful save: clear dirty, adopt the post-write revision.
    /// Undo history survives save so edits remain reversible afterwards.
    pub fn mark_saved(&mut self, document: DocumentId, revision: omaterm_context::FileRevision) {
        if let Some(doc) = self.docs.get_mut(&document) {
            doc.revision = revision;
            doc.dirty = false;
        }
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
    let code = ch as u32;
    if ch.is_control() {
        return 0;
    }
    match code {
        0x1100..=0x115F
        | 0x2E80..=0xA4CF
        | 0xAC00..=0xD7A3
        | 0xF900..=0xFAFF
        | 0xFE30..=0xFE4F
        | 0xFF00..=0xFF60
        | 0xFFE0..=0xFFE6
        | 0x1F300..=0x1FAFF => 2,
        _ => 1,
    }
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
}

impl<'a> Scanner<'a> {
    fn new(text: &'a str) -> Self {
        Self {
            bytes: text.as_bytes(),
            pos: 0,
            spans: Vec::new(),
        }
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
        while self.pos < self.bytes.len() {
            let byte = self.bytes[self.pos];
            if byte == b'\\' {
                self.pos += 2.min(self.bytes.len() - self.pos);
                continue;
            }
            self.pos += 1;
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
        while self.pos < self.bytes.len() && self.bytes[self.pos] != b'\'' {
            self.pos += utf8_len(self.bytes[self.pos]);
        }
        self.pos = (self.pos + 1).min(self.bytes.len());
        self.push(start, TokenKind::String);
    }

    /// Consume a line comment through (not including) the newline.
    fn line_comment(&mut self) {
        let start = self.pos;
        while self.pos < self.bytes.len() && self.bytes[self.pos] != b'\n' {
            self.pos += utf8_len(self.bytes[self.pos]);
        }
        self.push(start, TokenKind::Comment);
    }

    /// Consume one identifier; emit a keyword span on match. Linear scan:
    /// the tables are tiny and this avoids any sorted-order invariant.
    fn word(&mut self, keywords: &[&str]) {
        let start = self.pos;
        while self.pos < self.bytes.len() && is_ident_continue(self.bytes[self.pos]) {
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
        while self.pos < self.bytes.len()
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
    use omaterm_context::EditorLanguage as Lang;
    if matches!(language, Lang::Plain) {
        return Vec::new();
    }
    if matches!(language, Lang::Markdown) {
        return tokenize_markdown(text);
    }
    let mut scan = Scanner::new(text);
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
    while scan.pos < scan.bytes.len() {
        let byte = scan.bytes[scan.pos];
        if byte == b'"' {
            scan.string(TokenKind::String);
        } else if bash && byte == b'\'' {
            scan.single_string();
        } else if matches!(language, Lang::Rust) && byte == b'\'' {
            // Rust char literal `'x'` / `'\n'` versus lifetime `'a`:
            // only the exact literal shape becomes a string span.
            let rest = &scan.bytes[scan.pos..];
            let literal = rest.len() >= 3
                && rest[2] == b'\''
                && (rest[1] != b'\\' || rest.len() >= 4 && rest[3] == b'\'');
            if literal {
                let end = if rest[1] == b'\\' { 4 } else { 3 };
                scan.pos += end.min(rest.len());
                scan.push(scan.pos - end.min(rest.len()), TokenKind::String);
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
            while scan.pos < scan.bytes.len() && depth > 0 {
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
fn tokenize_markdown(text: &str) -> Vec<TokenSpan> {
    let mut spans = Vec::new();
    let mut offset = 0;
    for line in text.split_inclusive('\n') {
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
        Self::with_runner(|request: HighlightRequest| {
            let spans = tokenize(request.language, &request.text);
            let mut max_cols = 0;
            for line in request.text.split('\n') {
                max_cols = max_cols.max(line_display_width(line));
            }
            HighlightResult {
                document: request.document,
                generation: request.generation,
                spans,
                max_cols,
            }
        })
    }

    fn with_runner(
        mut run: impl FnMut(HighlightRequest) -> HighlightResult + Send + 'static,
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
        let revision = omaterm_context::FileRevision {
            size: text.len() as u64,
            mtime_secs: 1,
            mtime_nanos: 0,
        };
        store.open(
            project,
            PathBuf::from(name),
            PathBuf::from(format!("/repo/{name}")),
            PathBuf::from("/repo"),
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
}
