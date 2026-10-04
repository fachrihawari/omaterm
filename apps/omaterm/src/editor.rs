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
use std::ffi::OsString;
use std::ops::Range;
use std::os::unix::ffi::OsStringExt;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use unicode_segmentation::UnicodeSegmentation;
use unicode_width::UnicodeWidthChar;

use omaterm_core::{CommandError, DocumentId, EditorDocumentInfo, ErrorCode, ProjectId};
use omaterm_state::{DocumentDescriptor, DocumentRegistry};

/// Bounded undo history: at most this many edits plus this many retained
/// bytes per document. A 1 MiB buffer with pathological 1-byte edits stays
/// under ~8 MiB of undo state.
const MAX_UNDO_ENTRIES: usize = 100;
const MAX_UNDO_BYTES: usize = 8 * 1024 * 1024;

/// Store-wide limits. Text includes the live and savepoint buffers; render
/// snapshots only retain shared `Arc` allocations and never copy their text.
pub const MAX_OPEN_DOCUMENTS: usize = 32;
pub const MAX_RETAINED_TEXT_BYTES: usize = 64 * 1024 * 1024;
pub const MAX_RETAINED_HISTORY_BYTES: usize = 64 * 1024 * 1024;
const MAX_RETAINED_TOKEN_BYTES: usize = 64 * 1024 * 1024;

/// Hard ceiling on highlight spans for one document. A tokenizer that would
/// exceed it publishes no spans at all (explicit plain fallback) rather than
/// a truncated partial list: an indexed renderer must never paint a span set
/// that disagrees with the installed document version.
pub const MAX_HIGHLIGHT_SPANS: usize = 131_072;

/// One reversible text replacement: `removed.len()` bytes at `start` were
/// replaced, and `added_len` bytes now occupy that span.
#[derive(Debug, Clone, PartialEq, Eq)]
struct TextEdit {
    start: usize,
    removed: String,
    added_len: usize,
    sequence: u64,
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
    text: Arc<str>,
    saved_text: Arc<str>,
    line_starts: Arc<[usize]>,
    history_cursor: Option<usize>,
    dirty: bool,
    /// In-memory generation for owner-side I/O completion guards. Every
    /// buffer mutation advances it so a completed save/revert cannot apply
    /// over newer edits.
    generation: u64,
    revision: omaterm_context::FileRevision,
    language: omaterm_context::EditorLanguage,
    undo: Vec<TextEdit>,
    undo_bytes: usize,
    redo: Vec<TextEdit>,
    redo_bytes: usize,
    /// Landed highlight state, shared by every render snapshot. The per-line
    /// index lives here so a visible row never scans the full span list.
    highlight: Arc<DocHighlight>,
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

    fn retained_text_bytes(&self) -> usize {
        if Arc::ptr_eq(&self.text, &self.saved_text) {
            self.text.len()
        } else {
            self.text.len() + self.saved_text.len()
        }
    }

    fn retained_history_bytes(&self) -> usize {
        self.undo_bytes + self.redo_bytes
    }

    fn replace_text(&mut self, text: String) {
        self.text = Arc::from(text);
        self.line_starts = line_starts(&self.text).into();
        self.clear_highlight();
    }

    fn clear_highlight(&mut self) {
        self.highlight = Arc::new(DocHighlight::default());
    }

    fn advance_generation(&mut self) {
        self.generation = self
            .generation
            .checked_add(1)
            .expect("document buffer generation exhausted");
    }
}

fn count_lines(text: &str) -> usize {
    if text.is_empty() {
        0
    } else {
        text.matches('\n').count() + 1
    }
}

/// Lifecycle state of a metadata-only document entry that has no live buffer
/// yet: a restart read is in flight, or it failed and needs Retry/Close.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PlaceholderState {
    /// A bounded restore read is queued or running.
    Loading,
    /// The document could not be opened (missing, replaced root, binary,
    /// oversize, inaccessible); no text is exposed.
    Unavailable(String),
}

/// Metadata-only entry for a document whose text has not been loaded. It keeps
/// the persisted `DocumentId` and root-scoped identity so a restore can commit
/// under the original identity, while never exposing any stale text.
#[derive(Debug, Clone)]
pub struct DocumentPlaceholder {
    id: DocumentId,
    project: ProjectId,
    path_bytes: Vec<u8>,
    root_identity: omaterm_context::RootIdentity,
    state: PlaceholderState,
}

impl DocumentPlaceholder {
    pub fn id(&self) -> DocumentId {
        self.id
    }

    pub fn project(&self) -> ProjectId {
        self.project
    }

    /// Root-relative path as supplied on save (decoded from raw bytes).
    pub fn path(&self) -> PathBuf {
        PathBuf::from(OsString::from_vec(self.path_bytes.clone()))
    }

    pub fn path_bytes(&self) -> &[u8] {
        &self.path_bytes
    }

    pub fn root_identity(&self) -> omaterm_context::RootIdentity {
        self.root_identity
    }

    pub fn state(&self) -> &PlaceholderState {
        &self.state
    }

    pub fn is_unavailable(&self) -> bool {
        matches!(self.state, PlaceholderState::Unavailable(_))
    }
}

/// One accepted restore reservation. Its `DocumentId` is the persisted snapshot
/// id; committing a successful read must reuse it, not mint a new one.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RestoreReservation {
    pub id: DocumentId,
    pub project: ProjectId,
    pub path_bytes: Vec<u8>,
    pub root_identity: omaterm_context::RootIdentity,
}

/// Why a restore reservation (or its commit) was refused. Every variant is a
/// stable, testable condition; none of them creates a partial buffer.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RestoreReserveError {
    /// The persisted id is already live or reserved.
    IdCollision,
    /// No free document slot remains (cap counts live buffers + placeholders).
    Capacity,
    /// The path bytes are empty, absolute, NUL-bearing or contain traversal.
    InvalidPath,
}

/// Why a reserved restore could not be committed. The placeholder is retained
/// (or explicitly unavailable) so the failure stays reachable.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RestoreCommitError {
    /// No reservation remains: the document was closed or already retired.
    NotReserved,
    /// The persisted id was taken by a different live buffer.
    IdCollision,
    /// The opened file is a contained alias already owned by another id.
    DuplicateLive(DocumentId),
}

#[derive(Debug, Default)]
pub struct DocumentStore {
    docs: HashMap<DocumentId, Document>,
    by_file: HashMap<(ProjectId, omaterm_context::RootIdentity, u64, u64), DocumentId>,
    /// Metadata-only entries with no live buffer (loading/unavailable).
    placeholders: HashMap<DocumentId, DocumentPlaceholder>,
    next_history_sequence: u64,
}

/// Validate raw root-relative path bytes before reserving a restore. Mirrors
/// the snapshot validator so a broadcast descriptor cannot reserve an escaping
/// or malformed path; decoding stays lossless via `OsString::from_vec`.
fn validate_restore_path(path_bytes: &[u8]) -> Result<(), RestoreReserveError> {
    if path_bytes.is_empty() || path_bytes.contains(&0) || path_bytes.starts_with(b"/") {
        return Err(RestoreReserveError::InvalidPath);
    }
    if path_bytes
        .split(|byte| *byte == b'/')
        .any(|component| component.is_empty() || matches!(component, b"." | b".."))
    {
        return Err(RestoreReserveError::InvalidPath);
    }
    Ok(())
}

/// Root-relative path bytes from a decoded `Path`, for the rare caller that
/// has a path rather than a persisted descriptor.
fn path_bytes_of(path: &Path) -> Vec<u8> {
    path.as_os_str().as_encoded_bytes().to_vec()
}

/// Immutable, shared data for one render generation. Cloning this value only
/// increments `Arc` reference counts; unchanged frames do not clone document
/// text or rebuild line/token indexes.
#[derive(Debug, Clone)]
pub struct DocumentRenderSnapshot {
    document: DocumentId,
    generation: u64,
    text: Arc<str>,
    line_starts: Arc<[usize]>,
    highlight: Arc<DocHighlight>,
}

impl DocumentRenderSnapshot {
    pub fn document(&self) -> DocumentId {
        self.document
    }

    pub fn generation(&self) -> u64 {
        self.generation
    }

    pub fn text(&self) -> &str {
        &self.text
    }

    pub fn line_count(&self) -> usize {
        self.line_starts.len()
    }

    pub fn line_range(&self, line: usize) -> Option<Range<usize>> {
        line_range(&self.line_starts, &self.text, line)
    }

    pub fn line(&self, line: usize) -> Option<&str> {
        let range = self.line_range(line)?;
        Some(&self.text[range])
    }

    pub fn offset_to_line_col(&self, offset: usize) -> (usize, usize) {
        offset_to_line_col(&self.line_starts, &self.text, offset)
    }

    pub fn line_col_to_offset(&self, line: usize, col: usize) -> usize {
        line_col_to_offset(&self.text, &self.line_starts, line, col)
    }

    pub fn tokens_for_line(&self, line: usize) -> LineTokens<'_> {
        let indexes = self
            .highlight
            .line_ranges
            .get(line)
            .map(|range| &self.highlight.line_indices[range.clone()])
            .unwrap_or_default();
        LineTokens {
            tokens: &self.highlight.spans,
            indexes,
        }
    }

    /// Landed highlight state for this generation (spans, per-line index,
    /// memory accounting, cap/fallback flags).
    pub fn highlight(&self) -> &DocHighlight {
        &self.highlight
    }
}

/// Borrowed bounded token access for one line. The renderer visits only spans
/// recorded for the requested row.
pub struct LineTokens<'a> {
    tokens: &'a [TokenSpan],
    indexes: &'a [usize],
}

impl<'a> Iterator for LineTokens<'a> {
    type Item = &'a TokenSpan;

    fn next(&mut self) -> Option<Self::Item> {
        let (index, rest) = self.indexes.split_first()?;
        self.indexes = rest;
        self.tokens.get(*index)
    }
}

impl DocumentStore {
    /// Validate a prospective open without reserving it. New lifecycle callers
    /// use [`Self::try_open`] so saturation is an explicit stable error rather
    /// than an eviction of a live (possibly dirty) buffer.
    pub fn can_open(&self, file: &omaterm_context::EditorFile) -> Result<(), CommandError> {
        if self.docs.len() >= MAX_OPEN_DOCUMENTS {
            return Err(CommandError::new(
                ErrorCode::InvalidRequest,
                "the 32-document editor limit is reached",
            ));
        }
        if file.text.len() > omaterm_core::validation::MAX_EDITOR_BYTES
            || count_lines(&file.text) > omaterm_core::validation::MAX_EDITOR_LINES
        {
            return Err(CommandError::new(
                ErrorCode::DocumentTooLarge,
                "document exceeds the editor size limit",
            ));
        }
        if self.retained_text_bytes().saturating_add(file.text.len()) > MAX_RETAINED_TEXT_BYTES {
            return Err(CommandError::new(
                ErrorCode::DocumentTooLarge,
                "opening the document would exceed the editor text budget",
            ));
        }
        Ok(())
    }

    /// Checked open path for lifecycle code that has not already reserved
    /// capacity. Reopening the same file is always accepted and returns the
    /// existing live buffer.
    pub fn try_open(
        &mut self,
        project: ProjectId,
        path: PathBuf,
        root: PathBuf,
        root_identity: omaterm_context::RootIdentity,
        file: omaterm_context::EditorFile,
    ) -> Result<DocumentId, CommandError> {
        let file_key = (
            project,
            root_identity,
            file.revision.device,
            file.revision.inode,
        );
        if let Some(id) = self.by_file.get(&file_key) {
            return Ok(*id);
        }
        self.can_open(&file)?;
        Ok(self.open_unchecked(project, path, root, root_identity, file))
    }

    /// Open a buffer over an already-validated read. Returns the live document
    /// when the same opened file (including a contained symlink alias) is open
    /// beneath the same captured root identity. Existing lifecycle callers
    /// validate the read before this point; new callers should prefer
    /// [`Self::try_open`] to receive an explicit capacity error.
    pub fn open(
        &mut self,
        project: ProjectId,
        path: PathBuf,
        root: PathBuf,
        root_identity: omaterm_context::RootIdentity,
        file: omaterm_context::EditorFile,
    ) -> DocumentId {
        self.open_unchecked(project, path, root, root_identity, file)
    }

    fn open_unchecked(
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
        self.insert_live(id, project, path, root, root_identity, file);
        id
    }

    /// Insert a live buffer under an explicit id (restore commit only). The
    /// caller has already rejected collisions and deduplicated on file
    /// identity, so this method trusts its inputs.
    fn insert_live(
        &mut self,
        id: DocumentId,
        project: ProjectId,
        path: PathBuf,
        root: PathBuf,
        root_identity: omaterm_context::RootIdentity,
        file: omaterm_context::EditorFile,
    ) {
        let file_key = (
            project,
            root_identity,
            file.revision.device,
            file.revision.inode,
        );
        let language = file.language;
        let revision = file.revision;
        let text: Arc<str> = Arc::from(file.text);
        let line_starts = line_starts(&text).into();
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
                saved_text: Arc::clone(&text),
                text,
                line_starts,
                history_cursor: None,
                dirty: false,
                generation: 0,
                revision,
                language,
                undo: Vec::new(),
                undo_bytes: 0,
                redo: Vec::new(),
                redo_bytes: 0,
                highlight: Arc::new(DocHighlight::default()),
            },
        );
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
        self.docs.get(&document).map(|doc| doc.text.to_string())
    }

    pub fn text(&self, document: DocumentId) -> Option<&str> {
        self.docs.get(&document).map(|doc| &*doc.text)
    }

    /// Shared immutable text/index snapshot for an unchanged render frame.
    pub fn render_snapshot(&self, document: DocumentId) -> Option<DocumentRenderSnapshot> {
        let doc = self.docs.get(&document)?;
        Some(DocumentRenderSnapshot {
            document,
            generation: doc.generation,
            text: Arc::clone(&doc.text),
            line_starts: Arc::clone(&doc.line_starts),
            highlight: Arc::clone(&doc.highlight),
        })
    }

    pub fn line_count(&self, document: DocumentId) -> Option<usize> {
        self.docs.get(&document).map(|doc| doc.line_starts.len())
    }

    pub fn line_range(&self, document: DocumentId, line: usize) -> Option<Range<usize>> {
        let doc = self.docs.get(&document)?;
        line_range(&doc.line_starts, &doc.text, line)
    }

    pub fn line(&self, document: DocumentId, line: usize) -> Option<&str> {
        let doc = self.docs.get(&document)?;
        let range = line_range(&doc.line_starts, &doc.text, line)?;
        Some(&doc.text[range])
    }

    pub fn offset_to_line_col(
        &self,
        document: DocumentId,
        offset: usize,
    ) -> Option<(usize, usize)> {
        let doc = self.docs.get(&document)?;
        Some(offset_to_line_col(&doc.line_starts, &doc.text, offset))
    }

    pub fn line_col_to_offset(
        &self,
        document: DocumentId,
        line: usize,
        col: usize,
    ) -> Option<usize> {
        let doc = self.docs.get(&document)?;
        Some(line_col_to_offset(&doc.text, &doc.line_starts, line, col))
    }

    /// Install raw token spans for exactly the current buffer generation,
    /// building the per-line index here. Convenience/simple-callers path; the
    /// highlight worker uses [`Self::set_highlight`] with its prebuilt index.
    /// Returns `false` for stale, invalid or over-cap results without changing
    /// the store.
    pub fn set_tokens(
        &mut self,
        document: DocumentId,
        generation: u64,
        tokens: Vec<TokenSpan>,
    ) -> bool {
        let Some(doc) = self.docs.get(&document) else {
            return false;
        };
        if doc.generation != generation {
            return false;
        }
        let highlight = build_highlight(generation, &doc.line_starts, &doc.text, 0, tokens);
        if highlight.capped {
            return false;
        }
        self.install_highlight(document, &highlight)
    }

    /// Install a highlight result already tokenized and line-indexed on the
    /// highlight worker. The generation must still be current and the index
    /// must agree with the installed line map; a stale result is rejected
    /// without mutating the store. Cancelled results are never installed.
    pub fn set_highlight(&mut self, result: HighlightResult) -> bool {
        if result.cancelled {
            return false;
        }
        let Some(doc) = self.docs.get(&result.document) else {
            return false;
        };
        if doc.generation != result.generation {
            return false;
        }
        if result.highlight.requested != result.generation
            || result.highlight.span_count != result.highlight.spans.len()
            || result.highlight.span_count > MAX_HIGHLIGHT_SPANS
            || (!result.highlight.capped
                && (result.highlight.line_ranges.len() != doc.line_starts.len()
                    || !valid_tokens(&doc.text, &result.highlight.spans)))
        {
            return false;
        }
        self.install_highlight(result.document, &result.highlight)
    }

    /// Shared install path: capacity-checked, Arc-swapped, no text copy.
    fn install_highlight(&mut self, document: DocumentId, highlight: &DocHighlight) -> bool {
        let Some(doc) = self.docs.get(&document) else {
            return false;
        };
        let retained_without_document = self
            .retained_token_bytes()
            .saturating_sub(doc.highlight.span_bytes);
        if retained_without_document.saturating_add(highlight.span_bytes) > MAX_RETAINED_TOKEN_BYTES
        {
            return false;
        }
        let doc = self
            .docs
            .get_mut(&document)
            .expect("document was checked above");
        doc.highlight = Arc::new(highlight.clone());
        true
    }

    pub fn retained_text_bytes(&self) -> usize {
        self.docs.values().map(Document::retained_text_bytes).sum()
    }

    pub fn retained_history_bytes(&self) -> usize {
        self.docs
            .values()
            .map(Document::retained_history_bytes)
            .sum()
    }

    pub fn retained_token_bytes(&self) -> usize {
        self.docs.values().map(|doc| doc.highlight.span_bytes).sum()
    }

    pub fn is_dirty(&self, document: DocumentId) -> Option<bool> {
        self.docs.get(&document).map(|doc| doc.dirty)
    }

    /// In-memory mutation generation captured before asynchronous editor I/O.
    pub fn generation(&self, document: DocumentId) -> Option<u64> {
        self.docs.get(&document).map(|doc| doc.generation)
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
            doc.text = Arc::clone(&doc.saved_text);
            doc.line_starts = line_starts(&doc.text).into();
            doc.clear_highlight();
            doc.dirty = false;
            doc.undo.clear();
            doc.redo.clear();
            doc.undo_bytes = 0;
            doc.redo_bytes = 0;
            doc.history_cursor = None;
            doc.advance_generation();
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

    /// Drop a metadata-only placeholder (close/retry-close). Returns whether
    /// one existed. Never touches a live buffer.
    pub fn release_placeholder(&mut self, document: DocumentId) -> bool {
        self.placeholders.remove(&document).is_some()
    }

    pub fn placeholder(&self, document: DocumentId) -> Option<&DocumentPlaceholder> {
        self.placeholders.get(&document)
    }

    pub fn is_placeholder(&self, document: DocumentId) -> bool {
        self.placeholders.contains_key(&document)
    }

    /// Metadata-only placeholders owned by `project`, stable-sorted by path so
    /// chips render in the same deterministic order as live buffers.
    pub fn project_placeholders(&self, project: ProjectId) -> Vec<DocumentId> {
        let mut entries: Vec<(PathBuf, DocumentId)> = self
            .placeholders
            .iter()
            .filter(|(_, entry)| entry.project == project)
            .map(|(id, entry)| (entry.path(), *id))
            .collect();
        entries.sort_by(|left, right| left.0.cmp(&right.0));
        entries.into_iter().map(|(_, id)| id).collect()
    }

    /// Total document slots occupied: live buffers plus metadata-only
    /// placeholders. Used by the cap check so an in-flight restore cannot
    /// over-subscribe the editor.
    fn occupied_slots(&self) -> usize {
        self.docs.len() + self.placeholders.len()
    }

    /// Reserve a metadata-only restore entry under the persisted id. Rejects
    /// id collisions and cap saturation before any filesystem work starts, and
    /// validates the raw path bytes so a corrupt descriptor cannot reserve.
    /// Re-reserving the same id is idempotent (a retry after failure).
    pub fn reserve_restore(
        &mut self,
        reservation: &RestoreReservation,
    ) -> Result<(), RestoreReserveError> {
        validate_restore_path(&reservation.path_bytes)?;
        if self.placeholders.contains_key(&reservation.id) {
            return Ok(());
        }
        if self.docs.contains_key(&reservation.id) {
            return Err(RestoreReserveError::IdCollision);
        }
        if self.occupied_slots() >= MAX_OPEN_DOCUMENTS {
            return Err(RestoreReserveError::Capacity);
        }
        self.placeholders.insert(
            reservation.id,
            DocumentPlaceholder {
                id: reservation.id,
                project: reservation.project,
                path_bytes: reservation.path_bytes.clone(),
                root_identity: reservation.root_identity,
                state: PlaceholderState::Loading,
            },
        );
        Ok(())
    }

    /// Mark a reserved entry unavailable after a failed restore read. The id
    /// stays reserved so Retry keeps the same persisted identity; no text is
    /// ever exposed for this entry.
    pub fn mark_restore_unavailable(&mut self, document: DocumentId, reason: impl Into<String>) {
        if let Some(placeholder) = self.placeholders.get_mut(&document) {
            placeholder.state = PlaceholderState::Unavailable(reason.into());
        }
    }

    /// Return an unavailable placeholder to the loading state for a retry.
    pub fn retry_restore(&mut self, document: DocumentId) -> bool {
        let Some(placeholder) = self.placeholders.get_mut(&document) else {
            return false;
        };
        placeholder.state = PlaceholderState::Loading;
        true
    }

    /// Commit a successful restore read through the checked restore path.
    /// The reservation must still exist (a closed/late completion is rejected
    /// and can never recreate a retired document), the persisted id is reused,
    /// and the opened file is deduplicated on (project, root identity, file
    /// identity) against live buffers.
    pub fn commit_restore(
        &mut self,
        document: DocumentId,
        root_path: PathBuf,
        file: omaterm_context::EditorFile,
    ) -> Result<DocumentId, RestoreCommitError> {
        let Some(placeholder) = self.placeholders.remove(&document) else {
            return Err(RestoreCommitError::NotReserved);
        };
        if self.docs.contains_key(&document) {
            self.placeholders.insert(document, placeholder);
            return Err(RestoreCommitError::IdCollision);
        }
        let file_key = (
            placeholder.project,
            placeholder.root_identity,
            file.revision.device,
            file.revision.inode,
        );
        if let Some(existing) = self.by_file.get(&file_key) {
            // A contained alias or concurrent open already owns this file:
            // converge on one buffer and drop the redundant reservation.
            return Err(RestoreCommitError::DuplicateLive(*existing));
        }
        self.insert_live(
            document,
            placeholder.project,
            placeholder.path(),
            root_path,
            placeholder.root_identity,
            file,
        );
        Ok(document)
    }

    /// Build the bounded metadata-only registry to persist, keyed by project.
    /// Live buffers and metadata-only placeholders contribute descriptors (no
    /// text). `active` is reported only when the selected id is a live buffer
    /// owned by that project, so a loading/unavailable selection is never
    /// persisted as the restored active document.
    pub fn export_document_registry(
        &self,
        active: &HashMap<ProjectId, DocumentId>,
    ) -> HashMap<ProjectId, DocumentRegistry> {
        let cap = omaterm_state::SnapshotLimits::default().max_documents_per_project;
        let mut registries: HashMap<ProjectId, DocumentRegistry> = HashMap::new();
        for doc in self.docs.values() {
            let registry = registries.entry(doc.project).or_default();
            if registry.documents.len() >= cap {
                continue;
            }
            registry.documents.push(DocumentDescriptor {
                id: doc.id,
                path_bytes: path_bytes_of(&doc.path),
                root_device: doc.root_identity.device,
                root_inode: doc.root_identity.inode,
            });
        }
        for placeholder in self.placeholders.values() {
            let registry = registries.entry(placeholder.project).or_default();
            if registry.documents.len() >= cap {
                continue;
            }
            registry.documents.push(DocumentDescriptor {
                id: placeholder.id,
                path_bytes: placeholder.path_bytes.clone(),
                root_device: placeholder.root_identity.device,
                root_inode: placeholder.root_identity.inode,
            });
        }
        for (project, document) in active {
            let Some(registry) = registries.get_mut(project) else {
                continue;
            };
            let owned = self
                .docs
                .get(document)
                .is_some_and(|doc| doc.project == *project);
            if owned && registry.documents.iter().any(|entry| entry.id == *document) {
                registry.active_document = Some(*document);
            }
        }
        registries
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
    /// written — deletion is an explicit user action. Metadata-only
    /// placeholders retire with the project too.
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
        let placeholders: Vec<DocumentId> = self
            .placeholders
            .iter()
            .filter(|(_, entry)| entry.project == project)
            .map(|(id, _)| *id)
            .collect();
        for id in placeholders {
            self.placeholders.remove(&id);
        }
        count
    }

    /// Retire the oldest reversible entries across documents until the shared
    /// budget holds. Only the front of an undo stack is removed, preserving
    /// each document's valid redo chain.
    fn enforce_history_budget(&mut self) {
        while self.retained_history_bytes() > MAX_RETAINED_HISTORY_BYTES {
            let oldest = self
                .docs
                .iter()
                .filter_map(|(id, doc)| doc.undo.first().map(|edit| (*id, edit.sequence)))
                .min_by_key(|(_, sequence)| *sequence);
            let Some((document, _)) = oldest else {
                break;
            };
            let doc = self.docs.get_mut(&document).expect("document was indexed");
            let dropped = doc.undo.remove(0);
            doc.undo_bytes = doc.undo_bytes.saturating_sub(dropped.retained_bytes());
        }
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
        let retained_text_without_document = self.retained_text_bytes().saturating_sub(
            self.docs
                .get(&document)
                .map_or(0, Document::retained_text_bytes),
        );
        self.next_history_sequence = self.next_history_sequence.wrapping_add(1);
        let sequence = self.next_history_sequence;
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
        if retained_text_without_document
            .saturating_add(next.len())
            .saturating_add(doc.saved_text.len())
            > MAX_RETAINED_TEXT_BYTES
        {
            return Err(CommandError::new(
                ErrorCode::DocumentTooLarge,
                "edit would exceed the aggregate editor text budget",
            ));
        }
        let removed = doc.text[start..end].to_owned();
        let edit = TextEdit {
            start,
            removed,
            added_len: insert.len(),
            sequence,
        };
        doc.undo_bytes += edit.retained_bytes();
        doc.undo.push(edit);
        doc.redo.clear();
        doc.redo_bytes = 0;
        while doc.undo.len() > MAX_UNDO_ENTRIES || doc.retained_history_bytes() > MAX_UNDO_BYTES {
            if let Some(dropped) = doc.undo.first() {
                doc.undo_bytes = doc.undo_bytes.saturating_sub(dropped.retained_bytes());
            }
            doc.undo.remove(0);
        }
        doc.replace_text(next);
        doc.dirty = doc.text != doc.saved_text;
        doc.history_cursor = None;
        doc.advance_generation();
        self.enforce_history_budget();
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
            sequence: edit.sequence,
        };
        doc.redo_bytes += redo.retained_bytes();
        doc.redo.push(redo);
        doc.replace_text(next);
        doc.history_cursor = Some(edit.start + edit.removed.len());
        doc.dirty = doc.text != doc.saved_text;
        doc.advance_generation();
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
            sequence: edit.sequence,
        };
        doc.undo_bytes += undo.retained_bytes();
        doc.redo_bytes = doc.redo_bytes.saturating_sub(edit.retained_bytes());
        doc.undo.push(undo);
        while doc.undo.len() > MAX_UNDO_ENTRIES || doc.retained_history_bytes() > MAX_UNDO_BYTES {
            if let Some(dropped) = doc.undo.first() {
                doc.undo_bytes = doc.undo_bytes.saturating_sub(dropped.retained_bytes());
            }
            doc.undo.remove(0);
        }
        doc.replace_text(next);
        doc.history_cursor = Some(edit.start + edit.removed.len());
        doc.dirty = doc.text != doc.saved_text;
        doc.advance_generation();
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
            let text: Arc<str> = Arc::from(file.text);
            doc.saved_text = Arc::clone(&text);
            doc.text = text;
            doc.line_starts = line_starts(&doc.text).into();
            doc.clear_highlight();
            doc.history_cursor = None;
            doc.revision = file.revision;
            doc.language = file.language;
            doc.file_device = file.revision.device;
            doc.file_inode = file.revision.inode;
            doc.undo.clear();
            doc.undo_bytes = 0;
            doc.redo.clear();
            doc.redo_bytes = 0;
            doc.dirty = false;
            doc.advance_generation();
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
    /// This changes the savepoint, not buffer content, so the buffer generation
    /// remains stable. Undo history survives save so edits remain reversible.
    pub fn mark_saved(&mut self, document: DocumentId, revision: omaterm_context::FileRevision) {
        let Some(baseline) = self.docs.get(&document).map(|doc| doc.text.clone()) else {
            return;
        };
        self.mark_saved_baseline(document, revision, baseline);
    }

    /// Record a successful save whose captured baseline text is `saved_text`.
    /// When the user edited to a newer generation while the write was in
    /// flight, the live text stays and `dirty` remains true: the disk holds
    /// the captured G text, so G is the new baseline, not the live G+1 text.
    /// The buffer generation is left unchanged (content did not change).
    pub fn mark_saved_baseline(
        &mut self,
        document: DocumentId,
        revision: omaterm_context::FileRevision,
        saved_text: Arc<str>,
    ) {
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
            doc.saved_text = saved_text;
            doc.dirty = doc.text != doc.saved_text;
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
        // absolute without re-scanning. Check cancellation inside the loop so
        // a backtick-dense line cannot monopolize the worker.
        let mut search = indent;
        while let Some(open) = line[search..].find('`') {
            if cancelled.is_some_and(|flag| flag.load(std::sync::atomic::Ordering::Relaxed)) {
                return spans;
            }
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

/// Landed highlight state per document: the generation it belongs to, shared
/// token spans over that generation's text, a compact per-line index into
/// those spans, exact memory accounting, and the cap/fallback flags.
///
/// Cloning this value is Arc-cheap; it never copies spans or the index. The
/// per-line index (`line_ranges` + `line_indices`) lets a renderer consult
/// only the spans intersecting one visible row.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DocHighlight {
    /// Buffer generation the spans were tokenized from.
    pub requested: u64,
    pub spans: Arc<[TokenSpan]>,
    /// For each line, the slice of `line_indices` holding its span indexes.
    pub line_ranges: Arc<[Range<usize>]>,
    /// Flattened span indexes, grouped by line via `line_ranges`.
    pub line_indices: Arc<[usize]>,
    /// Number of installed spans; always `spans.len()` on a valid value.
    pub span_count: usize,
    /// Retained bytes (spans + index) for aggregate budget accounting.
    pub span_bytes: usize,
    pub max_cols: usize,
    /// True when the document exceeded [`MAX_HIGHLIGHT_SPANS`] and was
    /// deliberately left un-highlighted (explicit plain fallback).
    pub capped: bool,
}

impl Default for DocHighlight {
    fn default() -> Self {
        Self {
            requested: 0,
            spans: Arc::from([]),
            line_ranges: Arc::from([]),
            line_indices: Arc::from([]),
            span_count: 0,
            span_bytes: 0,
            max_cols: 0,
            capped: false,
        }
    }
}

/// Build landed highlight state from raw, validated spans and the document's
/// line starts. Enforces [`MAX_HIGHLIGHT_SPANS`] by publishing an explicit
/// plain fallback (no spans, `capped = true`) rather than a partial list.
fn build_highlight(
    requested: u64,
    starts: &[usize],
    text: &str,
    max_cols: usize,
    spans: Vec<TokenSpan>,
) -> DocHighlight {
    if spans.len() > MAX_HIGHLIGHT_SPANS {
        return DocHighlight {
            requested,
            max_cols,
            capped: true,
            ..DocHighlight::default()
        };
    }
    if !valid_tokens(text, &spans) {
        return DocHighlight {
            requested,
            max_cols,
            capped: true,
            ..DocHighlight::default()
        };
    }
    let Some((ranges, indices, span_bytes)) = token_line_index(starts, &spans) else {
        return DocHighlight {
            requested,
            max_cols,
            capped: true,
            ..DocHighlight::default()
        };
    };
    let span_count = spans.len();
    DocHighlight {
        requested,
        spans: spans.into(),
        line_ranges: ranges.into(),
        line_indices: indices.into(),
        span_count,
        span_bytes,
        max_cols,
        capped: false,
    }
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

fn line_range(starts: &[usize], text: &str, line: usize) -> Option<Range<usize>> {
    let start = *starts.get(line)?;
    let end = starts
        .get(line + 1)
        .map(|end| end.saturating_sub(1))
        .unwrap_or(text.len());
    Some(start..end)
}

fn valid_tokens(text: &str, tokens: &[TokenSpan]) -> bool {
    let mut previous_end = 0;
    tokens.iter().all(|token| {
        let Some(end) = token.start.checked_add(token.len) else {
            return false;
        };
        let valid = token.len > 0
            && token.start >= previous_end
            && end <= text.len()
            && text.is_char_boundary(token.start)
            && text.is_char_boundary(end);
        previous_end = end;
        valid
    })
}

/// Build a compact per-line index. Non-overlapping spans can intersect at
/// most `spans + lines` rows, keeping this bounded for validated documents.
fn token_line_index(
    starts: &[usize],
    tokens: &[TokenSpan],
) -> Option<(Vec<Range<usize>>, Vec<usize>, usize)> {
    if tokens.is_empty() {
        return Some((Vec::new(), Vec::new(), 0));
    }
    let mut lines = vec![Vec::new(); starts.len()];
    let mut entries = 0usize;
    for (index, token) in tokens.iter().enumerate() {
        let end = token.start + token.len;
        let first = starts
            .partition_point(|start| *start <= token.start)
            .saturating_sub(1);
        let last = starts
            .partition_point(|start| *start < end)
            .saturating_sub(1)
            .min(starts.len().saturating_sub(1));
        for entry in lines.iter_mut().take(last + 1).skip(first) {
            entries = entries.checked_add(1)?;
            let bytes = tokens
                .len()
                .checked_mul(std::mem::size_of::<TokenSpan>())?
                .checked_add(
                    starts
                        .len()
                        .checked_mul(std::mem::size_of::<Range<usize>>())?,
                )?
                .checked_add(entries.checked_mul(std::mem::size_of::<usize>())?)?;
            if bytes > MAX_RETAINED_TOKEN_BYTES {
                return None;
            }
            entry.push(index);
        }
    }
    let mut ranges = Vec::with_capacity(lines.len());
    let mut indices = Vec::with_capacity(entries);
    for line in lines {
        let start = indices.len();
        indices.extend(line);
        ranges.push(start..indices.len());
    }
    let bytes = std::mem::size_of_val(tokens)
        + std::mem::size_of_val(ranges.as_slice())
        + std::mem::size_of_val(indices.as_slice());
    Some((ranges, indices, bytes))
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

    /// Test-only constructor exposing the deterministic runner seam. Never
    /// used by the desktop binary.
    #[cfg(test)]
    pub(crate) fn with_test_runner(
        run: impl FnMut(&EditorIoRequest, &AtomicBool) -> Result<EditorIoSuccess, EditorIoError>
        + Send
        + 'static,
    ) -> Self {
        Self::with_runner(run)
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

/// A completed tokenize attempt. It carries the shared Arc spans and the
/// per-line index built on the worker, exact span accounting, and the
/// cap/fallback state. `cancelled` marks a cooperative abort whose spans are
/// partial and must never be installed.
#[derive(Debug)]
pub struct HighlightResult {
    pub document: omaterm_core::DocumentId,
    pub generation: u64,
    pub highlight: DocHighlight,
    pub cancelled: bool,
}

impl HighlightResult {
    /// Shared spans over the tokenized generation.
    pub fn spans(&self) -> &[TokenSpan] {
        &self.highlight.spans
    }

    pub fn max_cols(&self) -> usize {
        self.highlight.max_cols
    }

    pub fn capped(&self) -> bool {
        self.highlight.capped
    }
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
                let cancelled = request.cancelled.load(std::sync::atomic::Ordering::Acquire);
                let mut max_cols = 0;
                for line in request.text.split('\n') {
                    if request.cancelled.load(std::sync::atomic::Ordering::Relaxed) {
                        break;
                    }
                    max_cols = max_cols.max(line_display_width(line));
                }
                // Build the per-line index on the worker so the owner thread
                // and the paint path never scan the full span list. A cancelled
                // tokenize keeps `cancelled = true`; `with_runner` discards it.
                let highlight = build_highlight(
                    request.generation,
                    &line_starts(&request.text),
                    &request.text,
                    max_cols,
                    spans,
                );
                HighlightResult {
                    document: request.document,
                    generation: request.generation,
                    highlight,
                    cancelled,
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
        store
            .try_open(
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
            .unwrap()
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
                assert!(!result.spans().is_empty());
                assert!(!result.cancelled);
                assert!(!result.capped());
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

    #[test]
    fn checked_open_enforces_the_aggregate_document_cap_without_eviction() {
        let mut store = DocumentStore::default();
        let project = ProjectId::new();
        let mut documents = Vec::new();
        for index in 0..MAX_OPEN_DOCUMENTS {
            documents.push(open_doc(
                &mut store,
                project,
                &format!("document-{index}.txt"),
                "text",
            ));
        }
        let rejected = store.try_open(
            project,
            PathBuf::from("overflow.txt"),
            PathBuf::from("/repo"),
            omaterm_context::RootIdentity {
                device: 1,
                inode: 1,
            },
            omaterm_context::EditorFile {
                text: "overflow".into(),
                bytes: 8,
                lines: 1,
                revision: omaterm_context::FileRevision {
                    size: 8,
                    mtime_secs: 1,
                    mtime_nanos: 0,
                    device: 1,
                    inode: u64::MAX,
                    content_digest: [0; 32],
                },
                language: omaterm_context::EditorLanguage::Plain,
            },
        );
        assert!(matches!(
            rejected,
            Err(error) if error.code == ErrorCode::InvalidRequest
        ));
        assert_eq!(store.project_documents(project).len(), MAX_OPEN_DOCUMENTS);
        assert!(
            documents
                .into_iter()
                .all(|document| store.text(document) == Some("text"))
        );
    }

    #[test]
    fn aggregate_history_cap_retires_oldest_undo_entries_first() {
        let mut store = DocumentStore::default();
        let project = ProjectId::new();
        let documents: Vec<_> = (0..9)
            .map(|index| open_doc(&mut store, project, &format!("{index}.txt"), "x"))
            .collect();
        for (sequence, document) in documents.iter().copied().enumerate() {
            let edit = TextEdit {
                start: 0,
                removed: "x".into(),
                added_len: MAX_UNDO_BYTES - 1,
                sequence: sequence as u64,
            };
            let doc = store.docs.get_mut(&document).unwrap();
            doc.undo_bytes = edit.retained_bytes();
            doc.undo.push(edit);
        }
        store.enforce_history_budget();
        assert!(store.retained_history_bytes() <= MAX_RETAINED_HISTORY_BYTES);
        assert!(store.docs[&documents[0]].undo.is_empty());
        assert_eq!(store.docs[&documents[8]].undo.len(), 1);
    }

    #[test]
    fn render_snapshots_share_unchanged_text_and_track_generation() {
        let mut store = DocumentStore::default();
        let document = open_doc(&mut store, ProjectId::new(), "a.txt", "one\ntwo");
        let first = store.render_snapshot(document).unwrap();
        let same = store.render_snapshot(document).unwrap();
        assert_eq!(first.generation(), 0);
        assert!(Arc::ptr_eq(&first.text, &same.text));
        assert!(Arc::ptr_eq(&first.line_starts, &same.line_starts));

        store.apply_edit(document, 0, 3, "ONE").unwrap();
        let second = store.render_snapshot(document).unwrap();
        assert_eq!(second.generation(), 1);
        assert_eq!(first.text(), "one\ntwo");
        assert_eq!(second.text(), "ONE\ntwo");
        assert!(!Arc::ptr_eq(&first.text, &second.text));
        assert!(store.undo(document).unwrap());
        assert_eq!(store.generation(document), Some(2));
        assert!(store.redo(document).unwrap());
        assert_eq!(store.generation(document), Some(3));
        store.discard_changes(document);
        assert_eq!(store.generation(document), Some(4));
    }

    #[test]
    fn cached_line_and_token_indexes_update_with_buffer_mutations() {
        let mut store = DocumentStore::default();
        let document = open_doc(&mut store, ProjectId::new(), "a.rs", "one\n界\nthree");
        let generation = store.generation(document).unwrap();
        assert!(store.set_tokens(
            document,
            generation,
            vec![
                TokenSpan {
                    start: 0,
                    len: 3,
                    kind: TokenKind::Keyword,
                },
                TokenSpan {
                    start: 4,
                    len: "界".len(),
                    kind: TokenKind::String,
                },
            ],
        ));
        let snapshot = store.render_snapshot(document).unwrap();
        assert_eq!(snapshot.line_count(), 3);
        assert_eq!(snapshot.line(1), Some("界"));
        assert_eq!(snapshot.offset_to_line_col(5), (1, 0));
        assert_eq!(snapshot.line_col_to_offset(2, 2), "one\n界\n".len() + 2);
        assert_eq!(snapshot.tokens_for_line(1).collect::<Vec<_>>().len(), 1);

        store.apply_edit(document, 0, 4, "zero\n").unwrap();
        let updated = store.render_snapshot(document).unwrap();
        assert_eq!(updated.line(0), Some("zero"));
        assert_eq!(updated.line(1), Some("界"));
        assert!(updated.tokens_for_line(1).next().is_none());
        assert!(!store.set_tokens(document, generation, Vec::new()));
    }

    #[test]
    fn highlight_over_span_cap_publishes_plain_fallback_not_partial_spans() {
        // One past the ceiling: the builder must drop every span and mark the
        // document as an explicit plain fallback. Partial spans never leak.
        let spans: Vec<TokenSpan> = (0..=MAX_HIGHLIGHT_SPANS)
            .map(|index| TokenSpan {
                start: index,
                len: 1,
                kind: TokenKind::Keyword,
            })
            .collect();
        let text = "a".repeat(spans.len() + 1);
        let highlight = build_highlight(7, &line_starts(&text), &text, 0, spans);
        assert!(highlight.capped);
        assert_eq!(highlight.requested, 7);
        assert!(highlight.spans.is_empty());
        assert_eq!(highlight.span_count, 0);
        assert!(highlight.line_ranges.is_empty());
        assert_eq!(highlight.span_bytes, 0);

        // Exactly at the ceiling still publishes the full indexed set.
        let spans: Vec<TokenSpan> = (0..MAX_HIGHLIGHT_SPANS)
            .map(|index| TokenSpan {
                start: index,
                len: 1,
                kind: TokenKind::Keyword,
            })
            .collect();
        let text = "a".repeat(spans.len() + 1);
        let highlight = build_highlight(8, &line_starts(&text), &text, 0, spans);
        assert!(!highlight.capped);
        assert_eq!(highlight.span_count, MAX_HIGHLIGHT_SPANS);
        assert_eq!(highlight.spans.len(), MAX_HIGHLIGHT_SPANS);
        assert!(highlight.span_bytes > 0);
    }

    #[test]
    fn store_installs_worker_capped_fallback_as_empty_highlight() {
        let mut store = DocumentStore::default();
        let document = open_doc(&mut store, ProjectId::new(), "dense.rs", "fn a() {}\n");
        let generation = store.generation(document).unwrap();
        // Build a capped fallback directly, as the worker would after seeing
        // more spans than the ceiling allows.
        let spans: Vec<TokenSpan> = (0..=MAX_HIGHLIGHT_SPANS)
            .map(|index| TokenSpan {
                start: index,
                len: 1,
                kind: TokenKind::Keyword,
            })
            .collect();
        let text = "a".repeat(MAX_HIGHLIGHT_SPANS + 2);
        let highlight = build_highlight(generation, &line_starts(&text), &text, 0, spans);
        assert!(highlight.capped);
        assert!(store.set_highlight(HighlightResult {
            document,
            generation,
            highlight,
            cancelled: false,
        }));
        let snapshot = store.render_snapshot(document).unwrap();
        assert!(snapshot.highlight().capped);
        assert_eq!(snapshot.highlight().span_count, 0);
        assert!(snapshot.tokens_for_line(0).next().is_none());
    }

    #[test]
    fn highlight_line_index_is_built_on_worker_and_read_per_visible_row() {
        use omaterm_context::EditorLanguage as Lang;
        let worker = HighlightWorker::new();
        let document = DocumentId::new();
        let text = "fn a() {}\nlet b = 2;\n// c\n";
        worker.submit(HighlightRequest {
            document,
            generation: 5,
            language: Lang::Rust,
            text: text.into(),
            cancelled: std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false)),
        });
        let start = std::time::Instant::now();
        let result = loop {
            if let Some(result) = worker.take_result() {
                break result;
            }
            assert!(start.elapsed() < std::time::Duration::from_secs(5));
            std::thread::sleep(std::time::Duration::from_millis(5));
        };
        assert!(!result.cancelled);
        assert!(!result.capped());
        assert_eq!(result.generation, 5);
        assert_eq!(result.highlight.requested, 5);
        assert_eq!(result.highlight.span_count, result.highlight.spans.len());
        // The index has one (possibly empty) range per line.
        assert_eq!(result.highlight.line_ranges.len(), line_starts(text).len());
        // Row 0 (`fn a() {}`) holds only its own spans.
        let line0: Vec<_> = {
            let range = result.highlight.line_ranges[0].clone();
            result.highlight.line_indices[range]
                .iter()
                .map(|index| result.highlight.spans[*index])
                .collect()
        };
        assert!(line0.iter().all(|span| span.start < "fn a() {}\n".len()));
        assert!(line0.iter().any(|span| { span_text(text, span) == "fn" }));
        // Row 2 holds the comment only.
        let line2_range = result.highlight.line_ranges[2].clone();
        let line2: Vec<_> = result.highlight.line_indices[line2_range]
            .iter()
            .map(|index| result.highlight.spans[*index])
            .collect();
        assert_eq!(line2.len(), 1);
        assert_eq!(span_text(text, &line2[0]), "// c");
    }

    #[test]
    fn markdown_backtick_loop_honors_cooperative_cancellation() {
        // A backtick-dense line: cancellation is observed inside the inner
        // loop, so tokenizing stops without publishing a partial span list.
        let dense = "`a`".repeat(50_000);
        let cancel = std::sync::atomic::AtomicBool::new(true);
        let spans = tokenize_cancellable(
            omaterm_context::EditorLanguage::Markdown,
            &dense,
            Some(&cancel),
        );
        assert!(spans.is_empty());
        // The builder then reports a fallback-free empty highlight.
        let highlight = build_highlight(1, &line_starts(&dense), &dense, 0, spans);
        assert!(!highlight.capped);
        assert_eq!(highlight.span_count, 0);
    }

    #[test]
    fn stale_or_cancelled_highlight_results_never_install() {
        let mut store = DocumentStore::default();
        let document = open_doc(&mut store, ProjectId::new(), "a.rs", "fn a() {}\n");
        let generation = store.generation(document).unwrap();
        let spans = vec![TokenSpan {
            start: 0,
            len: 2,
            kind: TokenKind::Keyword,
        }];
        let highlight = |requested: u64| {
            build_highlight(
                requested,
                &line_starts("fn a() {}\n"),
                "fn a() {}\n",
                4,
                spans.clone(),
            )
        };

        // Stale generation: the buffer has not moved but the result claims a
        // later one, so the store refuses it.
        assert!(!store.set_highlight(HighlightResult {
            document,
            generation: generation + 1,
            highlight: highlight(generation + 1),
            cancelled: false,
        }));
        assert!(
            store
                .render_snapshot(document)
                .unwrap()
                .tokens_for_line(0)
                .next()
                .is_none()
        );

        // Cancelled result: explicit abort is never installed even when the
        // generation matches.
        assert!(!store.set_highlight(HighlightResult {
            document,
            generation,
            highlight: highlight(generation),
            cancelled: true,
        }));
        assert!(
            store
                .render_snapshot(document)
                .unwrap()
                .tokens_for_line(0)
                .next()
                .is_none()
        );

        // Fresh, complete result installs and is visible by line.
        assert!(store.set_highlight(HighlightResult {
            document,
            generation,
            highlight: highlight(generation),
            cancelled: false,
        }));
        let snapshot = store.render_snapshot(document).unwrap();
        assert_eq!(snapshot.highlight().span_count, 1);
        assert!(!snapshot.highlight().capped);
        assert_eq!(snapshot.tokens_for_line(0).collect::<Vec<_>>().len(), 1);
        assert!(snapshot.tokens_for_line(1).next().is_none());
    }

    #[test]
    fn closing_retires_buffer_history_and_indexes() {
        let mut store = DocumentStore::default();
        let document = open_doc(&mut store, ProjectId::new(), "a.txt", "text");
        store.apply_edit(document, 4, 0, "!").unwrap();
        let generation = store.generation(document).unwrap();
        assert!(store.set_tokens(
            document,
            generation,
            vec![TokenSpan {
                start: 0,
                len: 4,
                kind: TokenKind::Keyword,
            }],
        ));
        assert!(store.retained_text_bytes() > 0);
        assert!(store.retained_history_bytes() > 0);
        assert!(store.retained_token_bytes() > 0);

        assert!(store.remove(document));
        assert_eq!(store.retained_text_bytes(), 0);
        assert_eq!(store.retained_history_bytes(), 0);
        assert_eq!(store.retained_token_bytes(), 0);
        assert!(store.render_snapshot(document).is_none());
    }

    fn restore_file(inode: u64, text: &str) -> omaterm_context::EditorFile {
        omaterm_context::EditorFile {
            text: text.into(),
            bytes: text.len(),
            lines: count_lines(text),
            revision: omaterm_context::FileRevision {
                size: text.len() as u64,
                mtime_secs: 1,
                mtime_nanos: 0,
                device: 1,
                inode,
                content_digest: [0; 32],
            },
            language: omaterm_context::EditorLanguage::Plain,
        }
    }

    fn reservation(id: DocumentId, project: ProjectId, path: &[u8]) -> RestoreReservation {
        RestoreReservation {
            id,
            project,
            path_bytes: path.to_vec(),
            root_identity: omaterm_context::RootIdentity {
                device: 1,
                inode: 1,
            },
        }
    }

    #[test]
    fn restore_reserve_commit_preserves_id_and_dedups_on_file_identity() {
        let mut store = DocumentStore::default();
        let project = ProjectId::new();
        let first = DocumentId::new();
        store
            .reserve_restore(&reservation(first, project, b"src/a.rs"))
            .unwrap();
        assert!(store.is_placeholder(first));
        assert_eq!(
            store.placeholder(first).unwrap().path(),
            PathBuf::from("src/a.rs")
        );

        let committed = store
            .commit_restore(
                first,
                PathBuf::from("/repo"),
                restore_file(42, "fn main() {}\n"),
            )
            .unwrap();
        assert_eq!(committed, first);
        assert_eq!(store.text(first), Some("fn main() {}\n"));
        assert_eq!(store.relative_path(first), Some(PathBuf::from("src/a.rs")));
        assert!(!store.is_placeholder(first));

        // A second reservation for the same (project, root, file) identity
        // converges on the existing buffer instead of forking it.
        let second = DocumentId::new();
        store
            .reserve_restore(&reservation(second, project, b"src/alias.rs"))
            .unwrap();
        let error = store
            .commit_restore(second, PathBuf::from("/repo"), restore_file(42, "ignored"))
            .unwrap_err();
        assert_eq!(error, RestoreCommitError::DuplicateLive(first));
        assert!(!store.is_placeholder(second));
        assert_eq!(store.project_documents(project), vec![first]);
    }

    #[test]
    fn restore_reserve_rejects_collision_capacity_and_invalid_paths() {
        let mut store = DocumentStore::default();
        let project = ProjectId::new();
        let live = open_doc(&mut store, project, "live.txt", "live");

        let collision = store.reserve_restore(&reservation(live, project, b"other.txt"));
        assert_eq!(collision, Err(RestoreReserveError::IdCollision));

        for path in [
            &b"".to_vec(),
            &b"/absolute.rs".to_vec(),
            &b"src/../escape.rs".to_vec(),
            &b"src//empty.rs".to_vec(),
            &b"src/nul\0.rs".to_vec(),
        ] {
            assert_eq!(
                store.reserve_restore(&reservation(DocumentId::new(), project, path)),
                Err(RestoreReserveError::InvalidPath)
            );
        }

        // Fill every remaining slot, then reject one more reservation.
        while store.occupied_slots() < MAX_OPEN_DOCUMENTS {
            let index = store.occupied_slots();
            open_doc(&mut store, project, &format!("fill-{index}.txt"), "x");
        }
        assert_eq!(
            store.reserve_restore(&reservation(DocumentId::new(), project, b"overflow.txt")),
            Err(RestoreReserveError::Capacity)
        );
    }

    #[test]
    fn restore_commit_after_close_or_collision_is_rejected() {
        let mut store = DocumentStore::default();
        let project = ProjectId::new();
        let id = DocumentId::new();
        store
            .reserve_restore(&reservation(id, project, b"closed.rs"))
            .unwrap();
        assert!(store.release_placeholder(id));
        assert_eq!(
            store.commit_restore(id, PathBuf::from("/repo"), restore_file(1, "x")),
            Err(RestoreCommitError::NotReserved)
        );
        assert!(store.document_info(id).is_none());
        assert!(!store.is_placeholder(id));
    }

    #[test]
    fn exporter_includes_placeholders_but_active_only_when_live() {
        let mut store = DocumentStore::default();
        let project = ProjectId::new();
        let loading = DocumentId::new();
        store
            .reserve_restore(&reservation(loading, project, b"pending.rs"))
            .unwrap();

        let registries = store.export_document_registry(&HashMap::from([(project, loading)]));
        let registry = &registries[&project];
        assert_eq!(registry.documents.len(), 1);
        assert_eq!(registry.documents[0].id, loading);
        assert_eq!(registry.documents[0].path_bytes, b"pending.rs".to_vec());
        assert_eq!(registry.active_document, None);

        store
            .commit_restore(loading, PathBuf::from("/repo"), restore_file(9, "clean\n"))
            .unwrap();
        let registries = store.export_document_registry(&HashMap::from([(project, loading)]));
        assert_eq!(registries[&project].active_document, Some(loading));
        assert_eq!(registries[&project].documents.len(), 1);
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
