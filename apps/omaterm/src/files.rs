//! M13 file panel state: contextual-sidebar tree rows plus persistence.
//!
//! GPUI-free. Rendering and key/mouse wiring live in `main.rs`. Depth is
//! never limited: every directory listing loads lazily — the root lists
//! synchronously (one bounded walk) while expanded directories resolve
//! from a per-project listing cache, fetching in the background on first
//! use with dimmed `loading` rows in between. The view owns the worker
//! threads (same generation-guarded pattern as the `Ctrl+P` search);
//! this panel only tracks cache, pending, rows, and expansion.
//!
//! Paths are relative to the project root. Expansion is bounded to 128
//! directories per project (snapshot schema v2); row rendering is capped
//! separately so large repos never build thousands of GPUI nodes.

use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};

use omaterm_core::{FileEntry, FileKind, ProjectId};

/// Rows built per refresh at most (virtualization: the tree never holds an
/// unbounded repo walk).
pub const MAX_TREE_ROWS: usize = 2_000;
/// Rows rendered at most; the footer names the truncation.
pub const MAX_RENDER_ROWS: usize = 400;
/// Vertical scrollbar width: thin VSCode-style rail beside the rows.
pub const SCROLLBAR_WIDTH_PX: f32 = 12.0;
/// Horizontal scroll clamp in pixels: far past any plausible filename at
/// the tree font, tight enough to keep the offset state honest.
pub const MAX_SCROLL_COLS_PX: f32 = 1600.0;
/// Thumbs never shrink past this: still grabbable at the bottom of deep
/// trees.
pub const MIN_THUMB_PX: f32 = 14.0;
/// Bounded expanded directories per project (snapshot schema v2).
pub const MAX_EXPANDED_DIRS: usize = 128;
/// Cache key for the top-level listing.
fn root_key() -> PathBuf {
    PathBuf::new()
}

/// A Nerd Font glyph with a VSCode-Seti-inspired color (`None` inherits the
/// row color, used for chevrons). Codepoints verified against the official
/// Nerd Fonts 3.5.1 reference plus the locally installed
/// JetBrainsMonoNerdFont-Regular.ttf (see the `icon_glyphs_exist` test and
/// the M13 status record); the app already resolves this family for its
/// terminal grid, so no new font dependency is introduced.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct FileIcon {
    pub glyph: char,
    pub color: Option<u32>,
}

/// Exact lowercase filenames first (VSCode gives these dedicated icons).
const NAME_ICONS: &[(&str, char, u32)] = &[
    ("dockerfile", '\u{e650}', 0x0DB7ED),
    ("docker-compose.yml", '\u{e650}', 0x0DB7ED),
    ("docker-compose.yaml", '\u{e650}', 0x0DB7ED),
    ("makefile", '\u{e673}', 0x9A9A9A),
    ("package.json", '\u{e616}', 0xCB3837),
    ("package-lock.json", '\u{e672}', 0x8A8A8A),
    ("cargo.toml", '\u{e6b2}', 0x9DACB7),
    ("cargo.lock", '\u{e672}', 0x8A8A8A),
    (".gitignore", '\u{e65d}', 0xF05032),
    (".gitattributes", '\u{e65d}', 0xF05032),
    (".editorconfig", '\u{e615}', 0x8A8A8A),
];

/// Lowercase extensions (after the last dot).
const EXT_ICONS: &[(&str, char, u32)] = &[
    ("rs", '\u{e68b}', 0xDEA584),
    ("toml", '\u{e6b2}', 0x9DACB7),
    ("md", '\u{e609}', 0x519ABA),
    ("markdown", '\u{e609}', 0x519ABA),
    ("mkd", '\u{e609}', 0x519ABA),
    ("json", '\u{e60b}', 0xCBCB41),
    ("js", '\u{e60c}', 0xE8D44D),
    ("mjs", '\u{e60c}', 0xE8D44D),
    ("cjs", '\u{e60c}', 0xE8D44D),
    ("jsx", '\u{e60c}', 0xE8D44D),
    ("ts", '\u{e628}', 0x519ABA),
    ("mts", '\u{e628}', 0x519ABA),
    ("cts", '\u{e628}', 0x519ABA),
    ("tsx", '\u{e628}', 0x519ABA),
    ("py", '\u{e606}', 0x3572A5),
    ("html", '\u{e60e}', 0xE34C26),
    ("htm", '\u{e60e}', 0xE34C26),
    ("css", '\u{e614}', 0x2965F1),
    ("scss", '\u{e614}', 0x2965F1),
    ("less", '\u{e614}', 0x2965F1),
    ("yml", '\u{e6a8}', 0xCB171E),
    ("yaml", '\u{e6a8}', 0xCB171E),
    ("xml", '\u{e619}', 0xE37933),
    ("sh", '\u{e691}', 0x89E051),
    ("bash", '\u{e691}', 0x89E051),
    ("zsh", '\u{e691}', 0x89E051),
    ("fish", '\u{e691}', 0x89E051),
    ("c", '\u{e649}', 0x8A8A8A),
    ("h", '\u{e649}', 0x8A8A8A),
    ("cpp", '\u{e646}', 0xF34B7D),
    ("cc", '\u{e646}', 0xF34B7D),
    ("cxx", '\u{e646}', 0xF34B7D),
    ("hpp", '\u{e646}', 0xF34B7D),
    ("cs", '\u{e648}', 0x178600),
    ("go", '\u{e627}', 0x00ADD8),
    ("java", '\u{e66d}', 0xED8B00),
    ("rb", '\u{e605}', 0x701516),
    ("php", '\u{e608}', 0x4F5D95),
    ("swift", '\u{e699}', 0xF05138),
    ("kt", '\u{e634}', 0x7F52FF),
    ("kts", '\u{e634}', 0x7F52FF),
    ("lua", '\u{e620}', 0x51A0D5),
    ("tf", '\u{e69a}', 0x7B42BC),
    ("png", '\u{e60d}', 0xA074C4),
    ("jpg", '\u{e60d}', 0xA074C4),
    ("jpeg", '\u{e60d}', 0xA074C4),
    ("gif", '\u{e60d}', 0xA074C4),
    ("svg", '\u{e60d}', 0xA074C4),
    ("ico", '\u{e60d}', 0xA074C4),
    ("webp", '\u{e60d}', 0xA074C4),
    ("mp3", '\u{e638}', 0x7B83DB),
    ("wav", '\u{e638}', 0x7B83DB),
    ("ogg", '\u{e638}', 0x7B83DB),
    ("flac", '\u{e638}', 0x7B83DB),
    ("mp4", '\u{e69f}', 0xFD971F),
    ("mkv", '\u{e69f}', 0xFD971F),
    ("webm", '\u{e69f}', 0xFD971F),
    ("mov", '\u{e69f}', 0xFD971F),
    ("zip", '\u{e6aa}', 0x9A9A9A),
    ("tar", '\u{e6aa}', 0x9A9A9A),
    ("gz", '\u{e6aa}', 0x9A9A9A),
    ("tgz", '\u{e6aa}', 0x9A9A9A),
    ("bz2", '\u{e6aa}', 0x9A9A9A),
    ("xz", '\u{e6aa}', 0x9A9A9A),
    ("7z", '\u{e6aa}', 0x9A9A9A),
    ("rar", '\u{e6aa}', 0x9A9A9A),
    ("pdf", '\u{e67d}', 0xEC1C24),
    ("ttf", '\u{e659}', 0x9A9A9A),
    ("otf", '\u{e659}', 0x9A9A9A),
    ("woff", '\u{e659}', 0x9A9A9A),
    ("woff2", '\u{e659}', 0x9A9A9A),
    ("csv", '\u{e64a}', 0x4CAF50),
    ("sql", '\u{e64d}', 0x4B8BBE),
    ("db", '\u{e64d}', 0x4B8BBE),
    ("sqlite", '\u{e64d}', 0x4B8BBE),
    ("sqlite3", '\u{e64d}', 0x4B8BBE),
    ("log", '\u{e64e}', 0xB0B0B0),
    ("txt", '\u{e64e}', 0xB0B0B0),
    ("text", '\u{e64e}', 0xB0B0B0),
    ("vim", '\u{e62b}', 0x019733),
    ("vimrc", '\u{e62b}', 0x019733),
    ("nvim", '\u{e62b}', 0x019733),
];

const FALLBACK_FILE_ICON: FileIcon = FileIcon {
    glyph: '\u{f15b}',
    color: Some(0xA1A1AA),
};
const FOLDER_CLOSED_ICON: FileIcon = FileIcon {
    glyph: '\u{f07b}',
    color: Some(0x8A8A8A),
};
const FOLDER_OPEN_ICON: FileIcon = FileIcon {
    glyph: '\u{f07c}',
    color: Some(0xA1A1AA),
};

/// Chevron markers, reserved for future nesting affordances (e.g. the M16
/// palette). Tree and finder rows intentionally use none: directory state
/// rides on the folder open/closed glyphs alone, so a second leading
/// marker would be redundant. Kept (and glyph-covered by test) rather than
/// deleted so the verified codepoints stay available.
#[allow(dead_code)]
pub const CHEVRON_RIGHT: char = '\u{f054}';
#[allow(dead_code)]
pub const CHEVRON_DOWN: char = '\u{f078}';

/// Magnifier for the finder input box (fa-search, Nerd Font).
pub const SEARCH_ICON: char = '\u{f002}';

/// VSCode-style match-highlight accent for finder results.
pub const MATCH_ACCENT: u32 = 0x4C9AFF;

/// Convert skim char `indices` into byte ranges over `text` for
/// `StyledText` highlights. Consecutive indices merge into single ranges;
/// out-of-range indices are ignored. Unicode-safe: char indices map
/// through `char_indices`, never assumed to be byte offsets.
pub fn highlight_ranges(text: &str, matched: &[usize]) -> Vec<std::ops::Range<usize>> {
    let byte_at: Vec<usize> = text.char_indices().map(|(byte, _)| byte).collect();
    let mut ordered = matched.to_vec();
    ordered.sort_unstable();
    ordered.dedup();
    let mut ranges = Vec::new();
    let mut run: Option<(usize, usize)> = None;
    for index in ordered {
        if index >= byte_at.len() {
            continue;
        }
        match run {
            Some((start, end)) if index == end + 1 => run = Some((start, index)),
            _ => {
                if let Some((start, end)) = run {
                    ranges
                        .push(byte_at[start]..byte_at.get(end + 1).copied().unwrap_or(text.len()));
                }
                run = Some((index, index));
            }
        }
    }
    if let Some((start, end)) = run {
        ranges.push(byte_at[start]..byte_at.get(end + 1).copied().unwrap_or(text.len()));
    }
    ranges
}

/// VSCode-style icon for a tree/finder row. Exact filenames win over
/// extensions; `README*`/`LICENSE*` families match by prefix; `.git` and
/// `node_modules` directories get their own marks.
pub fn icon_for(path: &Path, kind: FileKind, expanded: bool) -> FileIcon {
    if kind == FileKind::Directory {
        let name = path
            .file_name()
            .map(|name| name.to_string_lossy().to_lowercase())
            .unwrap_or_default();
        if name == ".git" {
            return FileIcon {
                glyph: '\u{e65d}',
                color: Some(0xF05032),
            };
        }
        if name == "node_modules" {
            return FileIcon {
                glyph: '\u{e616}',
                color: Some(0xCB3837),
            };
        }
        return if expanded {
            FOLDER_OPEN_ICON
        } else {
            FOLDER_CLOSED_ICON
        };
    }
    let name = path
        .file_name()
        .map(|name| name.to_string_lossy().to_lowercase())
        .unwrap_or_default();
    if name.starts_with("readme") {
        return FileIcon {
            glyph: '\u{e609}',
            color: Some(0x519ABA),
        };
    }
    if name.starts_with("license") || name.starts_with("licence") {
        return FileIcon {
            glyph: '\u{e60a}',
            color: Some(0xD4A017),
        };
    }
    if let Some((_, glyph, color)) = NAME_ICONS.iter().find(|(known, _, _)| *known == name) {
        return FileIcon {
            glyph: *glyph,
            color: Some(*color),
        };
    }
    if let Some(extension) = Path::new(&name).extension().and_then(|ext| ext.to_str())
        && let Some((_, glyph, color)) = EXT_ICONS.iter().find(|(known, _, _)| *known == extension)
    {
        return FileIcon {
            glyph: *glyph,
            color: Some(*color),
        };
    }
    FALLBACK_FILE_ICON
}

/// One visible tree row.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FileRow {
    pub path: PathBuf,
    pub depth: usize,
    pub kind: FileKind,
    pub expanded: bool,
    /// Expanded but children not loaded yet (background fetch in flight or
    /// queued). Renders dimmed; rows fill in when the fetch lands.
    pub loading: bool,
}

/// Scrollbar thumb geometry as track fractions `(top, height)`, each in
/// `[0, 1]`. The thumb fills the track when everything fits; otherwise
/// its height is the visible share and its top slides over the remaining
/// travel. Over-clamped offsets pin to the nearer end, never NaN.
pub fn scroll_thumb(total: usize, visible: usize, offset: usize) -> (f32, f32) {
    if total <= visible || total == 0 || visible == 0 {
        return (0.0, 1.0);
    }
    let height = (visible as f32 / total as f32).clamp(0.0, 1.0);
    let max_offset = total.saturating_sub(visible);
    let top = (offset.min(max_offset) as f32 / max_offset.max(1) as f32) * (1.0 - height);
    (top, height)
}

/// Per-project expansion + selection state with a listing cache and a row
/// cache for the selected project.
#[derive(Debug, Default)]
pub struct FilePanel {
    expanded: HashMap<ProjectId, HashSet<PathBuf>>,
    /// Cached single-level listings by relative dir (empty path = root).
    /// Only expanded dirs (plus root) are ever cached, so the cache stays
    /// bounded by the expansion bound.
    listings: HashMap<ProjectId, HashMap<PathBuf, Vec<FileEntry>>>,
    /// Directories with a background fetch in flight.
    pending: HashMap<ProjectId, HashSet<PathBuf>>,
    selected: HashMap<ProjectId, PathBuf>,
    rows: Vec<FileRow>,
    rows_project: Option<ProjectId>,
    truncated: bool,
    empty_root: bool,
    /// The top-level listing hit its cap (drives the `Show more` row).
    root_truncated: bool,
}

impl FilePanel {
    /// Bounded expansion map for snapshot capture (schema v2).
    pub fn expanded_snapshot(&self) -> HashMap<ProjectId, Vec<PathBuf>> {
        self.expanded
            .iter()
            .map(|(project, dirs)| {
                let mut kept: Vec<PathBuf> = dirs.iter().take(MAX_EXPANDED_DIRS).cloned().collect();
                kept.sort();
                (*project, kept)
            })
            .collect()
    }

    /// Install validated snapshot state after restore. Unknown projects are
    /// kept (their rows build on selection); over-bound sets truncate. No
    /// depth filtering: every restored expansion loads lazily on demand.
    pub fn restore(&mut self, expanded: Vec<(ProjectId, Vec<PathBuf>)>) {
        self.expanded.clear();
        self.listings.clear();
        self.pending.clear();
        self.selected.clear();
        self.rows.clear();
        self.rows_project = None;
        self.truncated = false;
        self.root_truncated = false;
        self.empty_root = false;
        for (project, dirs) in expanded {
            let kept: HashSet<PathBuf> = dirs
                .into_iter()
                .filter(|dir| !dir.as_os_str().is_empty())
                .take(MAX_EXPANDED_DIRS)
                .collect();
            if !kept.is_empty() {
                self.expanded.insert(project, kept);
            }
        }
    }

    /// Toggle a directory's expansion. Returns true when the row cache is
    /// stale (caller rebuilds + marks persistence dirty). Files select
    /// instead of expanding. Expanding queues a background fetch; the rows
    /// fill in when it lands. Collapsing drops the cached listing.
    pub fn toggle(&mut self, project: ProjectId, path: &Path, is_dir: bool) -> bool {
        self.selected.insert(project, path.to_path_buf());
        if !is_dir {
            return true;
        }
        let dirs = self.expanded.entry(project).or_default();
        if dirs.contains(path) {
            dirs.remove(path);
            if let Some(listings) = self.listings.get_mut(&project) {
                listings.remove(path);
            }
            if let Some(pending) = self.pending.get_mut(&project) {
                pending.remove(path);
            }
        } else if dirs.len() < MAX_EXPANDED_DIRS {
            dirs.insert(path.to_path_buf());
        }
        true
    }

    /// Store a fetched listing; pending clears. Callers rebuild rows after.
    pub fn insert_listing(&mut self, project: ProjectId, dir: PathBuf, entries: Vec<FileEntry>) {
        self.listings
            .entry(project)
            .or_default()
            .insert(dir.clone(), entries);
        if let Some(pending) = self.pending.get_mut(&project) {
            pending.remove(&dir);
        }
    }

    /// Mark a directory fetch in flight (dedupes worker spawns).
    pub fn mark_pending(&mut self, project: ProjectId, dir: PathBuf) {
        self.pending.entry(project).or_default().insert(dir);
    }

    pub fn pending_count(&self, project: ProjectId) -> usize {
        self.pending.get(&project).map(HashSet::len).unwrap_or(0)
    }

    /// Retire one in-flight fetch without storing (stale generation).
    pub fn unpend(&mut self, project: ProjectId, dir: &Path) {
        if let Some(pending) = self.pending.get_mut(&project) {
            pending.remove(dir);
        }
    }

    /// Retire all in-flight fetches (generation bump).
    pub fn unpend_all(&mut self, project: ProjectId) {
        self.pending.remove(&project);
    }

    pub fn is_pending(&self, project: ProjectId, dir: &Path) -> bool {
        self.pending
            .get(&project)
            .is_some_and(|pending| pending.contains(dir))
    }

    /// Expanded directories with neither cache nor flight: the view must
    /// spawn background fetches for exactly these (sorted for determinism).
    pub fn needed_dirs(&self, project: ProjectId) -> Vec<PathBuf> {
        let mut needed: Vec<PathBuf> = self
            .expanded
            .get(&project)
            .map(|dirs| {
                dirs.iter()
                    .filter(|dir| {
                        !self
                            .listings
                            .get(&project)
                            .is_some_and(|listings| listings.contains_key(*dir))
                            && !self.is_pending(project, dir)
                    })
                    .cloned()
                    .collect()
            })
            .unwrap_or_default();
        needed.sort();
        needed
    }

    /// Drop one cached listing (watcher event); rows rebuild on next sync.
    pub fn invalidate(&mut self, project: ProjectId, dir: &Path) {
        if let Some(listings) = self.listings.get_mut(&project) {
            listings.remove(dir);
        }
    }

    /// Whether any ancestor listing of `dir` (including the root) is
    /// missing from cache — i.e. fetches are still resolving above it.
    fn ancestor_listings_missing(&self, project: ProjectId, dir: &Path) -> bool {
        let mut prefix = PathBuf::new();
        let cached_root = self
            .listings
            .get(&project)
            .is_some_and(|map| map.contains_key(&root_key()));
        if !cached_root {
            return true;
        }
        for component in dir.components() {
            prefix.push(component);
            if prefix == dir {
                break;
            }
            let known = self
                .listings
                .get(&project)
                .is_some_and(|map| map.contains_key(&prefix));
            if !known {
                return true;
            }
        }
        false
    }

    /// Drop a project's cache + flights (project switch, root change).
    pub fn clear_project(&mut self, project: ProjectId) {
        self.listings.remove(&project);
        self.pending.remove(&project);
        if self.rows_project == Some(project) {
            self.rows_project = None;
            self.rows.clear();
        }
    }

    pub fn select(&mut self, project: ProjectId, path: PathBuf) {
        self.selected.insert(project, path);
    }

    pub fn selected_path(&self, project: ProjectId) -> Option<&Path> {
        self.selected.get(&project).map(PathBuf::as_path)
    }

    /// Rebuild the row cache purely from the listing cache — no filesystem
    /// work. The view guarantees the root listing is cached first (one
    /// bounded synchronous walk); expanded directories resolve from cache
    /// or render as `loading` rows until their background fetch lands.
    /// Missing parent rows prune dead expansions (deleted directories).
    /// `empty_root` renders the no-root state.
    pub fn refresh(&mut self, project: ProjectId, empty_root: bool) {
        self.rows_project = Some(project);
        self.empty_root = empty_root;
        self.truncated = false;
        self.root_truncated = false;
        self.rows.clear();
        if empty_root {
            return;
        }
        let expanded: Vec<PathBuf> = self
            .expanded
            .get(&project)
            .map(|dirs| {
                let mut sorted: Vec<PathBuf> = dirs.iter().cloned().collect();
                sorted.sort();
                sorted
            })
            .unwrap_or_default();
        let mut live: HashSet<PathBuf> = HashSet::new();
        if let Some(top) = self
            .listings
            .get(&project)
            .and_then(|map| map.get(&root_key()))
        {
            for entry in top.clone() {
                if self.rows.len() >= MAX_TREE_ROWS {
                    self.truncated = true;
                    break;
                }
                self.rows.push(FileRow {
                    path: entry.path,
                    depth: 0,
                    kind: entry.kind,
                    expanded: false,
                    loading: false,
                });
            }
        }
        for dir in &expanded {
            if self.rows.len() >= MAX_TREE_ROWS {
                self.truncated = true;
                break;
            }
            // A missing parent row prunes the expansion ONLY with full
            // ground truth: every ancestor listing up to the root is
            // cached yet the row is still absent, so the directory is
            // truly gone. While any ancestor fetch is outstanding the
            // expansion is kept so multi-level chains materialize as
            // background completions land one level at a time.
            let Some(at) = self.rows.iter().position(|row| &row.path == dir) else {
                if self.ancestor_listings_missing(project, dir) {
                    live.insert(dir.clone());
                } else if let Some(map) = self.listings.get_mut(&project) {
                    map.remove(dir);
                }
                continue;
            };
            live.insert(dir.clone());
            let depth = dir.components().count();
            let Some(children) = self
                .listings
                .get(&project)
                .and_then(|map| map.get(dir))
                .cloned()
            else {
                // Not yet fetched: the parent row shows a dimmed loading
                // state until the background fetch lands.
                if let Some(row) = self.rows.get_mut(at) {
                    row.loading = true;
                }
                continue;
            };
            let mut nested = Vec::with_capacity(children.len());
            for entry in children {
                if self.rows.len() + nested.len() >= MAX_TREE_ROWS {
                    self.truncated = true;
                    break;
                }
                nested.push(FileRow {
                    path: entry.path,
                    depth,
                    kind: entry.kind,
                    expanded: false,
                    loading: false,
                });
            }
            self.rows.splice(at + 1..at + 1, nested);
        }
        // Mark every expanded directory row (including nested ones), then
        // prune expansions that no longer resolve (deleted directories).
        if let Some(dirs) = self.expanded.get(&project) {
            for row in self.rows.iter_mut() {
                if row.kind == FileKind::Directory && dirs.contains(&row.path) {
                    row.expanded = true;
                }
            }
        }
        if live.len() != expanded.len()
            && let Some(dirs) = self.expanded.get_mut(&project)
        {
            dirs.retain(|dir| live.contains(dir));
        }
        // Clamp a stale selection back onto a visible row.
        let visible = self.rows.iter().any(|row| {
            self.selected
                .get(&project)
                .is_some_and(|selected| selected == &row.path)
        });
        if !visible {
            self.selected.remove(&project);
        }
    }

    pub fn rows_for(&self, project: ProjectId) -> Option<&[FileRow]> {
        (self.rows_project == Some(project)).then_some(&self.rows)
    }

    /// Project whose rows are cached (poll/tick bookkeeping).
    pub fn rows_project_for_tick(&self) -> Option<ProjectId> {
        self.rows_project
    }

    pub fn is_truncated(&self) -> bool {
        self.truncated
    }

    pub fn is_empty_root(&self) -> bool {
        self.empty_root
    }

    pub fn set_root_truncated(&mut self, truncated: bool) {
        self.root_truncated = truncated;
    }

    pub fn is_root_truncated(&self) -> bool {
        self.root_truncated
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entries(paths: &[(&str, FileKind)]) -> Vec<FileEntry> {
        paths
            .iter()
            .map(|(path, kind)| FileEntry {
                path: PathBuf::from(path),
                kind: *kind,
            })
            .collect()
    }

    fn seed(panel: &mut FilePanel, project: ProjectId, dir: &str, rows: &[(&str, FileKind)]) {
        panel.insert_listing(project, PathBuf::from(dir), entries(rows));
    }

    #[test]
    fn refresh_builds_nested_rows_and_prunes_missing_dirs() {
        let mut panel = FilePanel::default();
        let project = ProjectId::new();
        panel.toggle(project, Path::new("src"), true);
        panel.toggle(project, Path::new("gone"), true);
        seed(
            &mut panel,
            project,
            "",
            &[("src", FileKind::Directory), ("README.md", FileKind::File)],
        );
        seed(
            &mut panel,
            project,
            "src",
            &[("src/main.rs", FileKind::File)],
        );
        panel.refresh(project, false);
        let rows = panel.rows_for(project).unwrap();
        let paths: Vec<_> = rows
            .iter()
            .map(|row| (row.path.to_string_lossy().into_owned(), row.depth))
            .collect();
        assert_eq!(
            paths,
            vec![
                ("src".to_owned(), 0),
                ("src/main.rs".to_owned(), 1),
                ("README.md".to_owned(), 0),
            ]
        );
        assert!(rows[0].expanded);
        // `gone` had no parent row: pruned from the expansion set.
        assert!(!panel.expanded[&project].contains(Path::new("gone")));
        assert!(panel.expanded[&project].contains(Path::new("src")));
    }

    #[test]
    fn uncached_expansions_render_loading_and_report_need() {
        let mut panel = FilePanel::default();
        let project = ProjectId::new();
        panel.toggle(project, Path::new("src"), true);
        seed(
            &mut panel,
            project,
            "",
            &[("src", FileKind::Directory), ("README.md", FileKind::File)],
        );
        // No listing for `src` yet: parent row loads, fetch needed.
        assert_eq!(panel.needed_dirs(project), vec![PathBuf::from("src")]);
        panel.refresh(project, false);
        let rows = panel.rows_for(project).unwrap();
        let parent = rows
            .iter()
            .find(|row| row.path == Path::new("src"))
            .unwrap();
        assert!(parent.expanded && parent.loading);
        // Marking the fetch in flight clears the need without rows.
        panel.mark_pending(project, PathBuf::from("src"));
        assert!(panel.needed_dirs(project).is_empty());
        assert!(!panel.is_pending(project, Path::new("other")));
        // Landing the fetch fills the rows and clears pending.
        panel.insert_listing(
            project,
            PathBuf::from("src"),
            entries(&[("src/main.rs", FileKind::File)]),
        );
        assert!(!panel.is_pending(project, Path::new("src")));
        assert!(panel.needed_dirs(project).is_empty());
        panel.refresh(project, false);
        let rows = panel.rows_for(project).unwrap();
        assert!(rows.iter().any(|row| row.path == Path::new("src/main.rs")));
        assert!(
            !rows
                .iter()
                .find(|row| row.path == Path::new("src"))
                .unwrap()
                .loading
        );
        // Watcher invalidation drops the cache so the next sync refetches.
        panel.invalidate(project, Path::new("src"));
        assert_eq!(panel.needed_dirs(project), vec![PathBuf::from("src")]);
        // Project switches drop cache and flights wholesale, while the
        // expansion intent survives so the next sync refetches.
        panel.mark_pending(project, PathBuf::from("src"));
        panel.clear_project(project);
        assert!(panel.rows_project_for_tick().is_none());
        assert!(!panel.is_pending(project, Path::new("src")));
        assert_eq!(panel.needed_dirs(project), vec![PathBuf::from("src")]);
    }

    #[test]
    fn uncached_deep_expansions_survive_refresh_until_fetched() {
        let mut panel = FilePanel::default();
        let project = ProjectId::new();
        panel.restore(vec![(
            project,
            vec![
                PathBuf::from("a"),
                PathBuf::from("a/b"),
                PathBuf::from("a/b/c"),
            ],
        )]);
        // Only the root listing has landed; nothing may prune yet, and the
        // deeper levels report need so the view fetches them.
        seed(&mut panel, project, "", &[("a", FileKind::Directory)]);
        panel.refresh(project, false);
        assert!(panel.expanded[&project].contains(Path::new("a/b")));
        assert!(panel.expanded[&project].contains(Path::new("a/b/c")));
        assert_eq!(
            panel.needed_dirs(project),
            vec![
                PathBuf::from("a"),
                PathBuf::from("a/b"),
                PathBuf::from("a/b/c")
            ]
        );
        // Landing one level keeps the deeper intent alive.
        panel.insert_listing(
            project,
            PathBuf::from("a"),
            entries(&[("a/b", FileKind::Directory)]),
        );
        panel.refresh(project, false);
        assert!(panel.expanded[&project].contains(Path::new("a/b/c")));
        // Ground truth prunes only when the parent listing denies the row.
        panel.insert_listing(project, PathBuf::from("a/b"), Vec::new());
        panel.refresh(project, false);
        assert!(!panel.expanded[&project].contains(Path::new("a/b/c")));
        assert!(panel.expanded[&project].contains(Path::new("a/b")));
    }

    #[test]
    fn prune_waits_for_full_ancestor_ground_truth() {
        let mut panel = FilePanel::default();
        let project = ProjectId::new();
        panel.restore(vec![(
            project,
            vec![
                PathBuf::from("a"),
                PathBuf::from("a/b"),
                PathBuf::from("a/b/c"),
            ],
        )]);
        seed(&mut panel, project, "", &[("a", FileKind::Directory)]);
        seed(&mut panel, project, "a", &[("a/b", FileKind::Directory)]);
        seed(
            &mut panel,
            project,
            "a/b",
            &[("a/b/c", FileKind::Directory)],
        );
        panel.refresh(project, false);
        assert!(panel.expanded[&project].contains(Path::new("a/b/c")));
        // Invalidate one middle level (watcher event): the deeper intent
        // survives because its ancestor fetch is outstanding.
        panel.invalidate(project, Path::new("a/b"));
        panel.refresh(project, false);
        assert!(panel.expanded[&project].contains(Path::new("a/b")));
        assert!(panel.expanded[&project].contains(Path::new("a/b/c")));
        // A fresh listing that denies the row prunes it for real.
        panel.insert_listing(project, PathBuf::from("a/b"), Vec::new());
        panel.refresh(project, false);
        assert!(!panel.expanded[&project].contains(Path::new("a/b/c")));
        assert!(panel.expanded[&project].contains(Path::new("a/b")));
    }

    #[test]
    fn deep_chains_load_without_any_depth_limit() {
        let mut panel = FilePanel::default();
        let project = ProjectId::new();
        panel.restore(vec![(
            project,
            vec![
                PathBuf::from("a"),
                PathBuf::from("a/b"),
                PathBuf::from("a/b/c"),
                PathBuf::from("a/b/c/d"),
                PathBuf::from("a/b/c/d/e"),
            ],
        )]);
        seed(&mut panel, project, "", &[("a", FileKind::Directory)]);
        seed(&mut panel, project, "a", &[("a/b", FileKind::Directory)]);
        seed(
            &mut panel,
            project,
            "a/b",
            &[("a/b/c", FileKind::Directory)],
        );
        seed(
            &mut panel,
            project,
            "a/b/c",
            &[("a/b/c/d", FileKind::Directory)],
        );
        seed(
            &mut panel,
            project,
            "a/b/c/d",
            &[("a/b/c/d/e", FileKind::Directory)],
        );
        seed(
            &mut panel,
            project,
            "a/b/c/d/e",
            &[("a/b/c/d/e/f.txt", FileKind::File)],
        );
        panel.refresh(project, false);
        let rows = panel.rows_for(project).unwrap();
        let leaf = rows
            .iter()
            .find(|row| row.path == Path::new("a/b/c/d/e/f.txt"))
            .unwrap();
        assert_eq!(leaf.depth, 5);
        assert!(!leaf.loading);
        assert!(rows.iter().all(|row| !row.loading));
        assert!(panel.needed_dirs(project).is_empty());
    }

    #[test]
    fn empty_root_renders_no_rows_and_selections_clamp() {
        let mut panel = FilePanel::default();
        let project = ProjectId::new();
        panel.refresh(project, true);
        assert!(panel.rows_for(project).is_none_or(|rows| rows.is_empty()));
        assert!(panel.is_empty_root());
    }

    #[test]
    fn toggle_selects_files_and_expands_dirs() {
        let mut panel = FilePanel::default();
        let project = ProjectId::new();
        panel.insert_listing(
            project,
            PathBuf::new(),
            entries(&[("b", FileKind::File), ("a", FileKind::File)]),
        );
        panel.refresh(project, false);
        panel.select(project, PathBuf::from("b"));
        assert_eq!(panel.selected_path(project), Some(Path::new("b")));
        // Toggling a file only selects; toggling a dir expands.
        panel.toggle(project, Path::new("a"), false);
        assert!(
            !panel
                .expanded
                .get(&project)
                .is_some_and(|dirs| !dirs.is_empty())
        );
        panel.toggle(project, Path::new("a"), true);
        assert!(panel.expanded[&project].contains(Path::new("a")));
    }

    #[test]
    fn restore_keeps_full_chains_without_depth_filtering() {
        let mut panel = FilePanel::default();
        let project = ProjectId::new();
        panel.restore(vec![(
            project,
            vec![
                PathBuf::from("a"),
                PathBuf::from("a/b/c"),
                PathBuf::from("a/b/c/d"),
                PathBuf::from("a/b/c/d/e"),
            ],
        )]);
        let kept = &panel.expanded[&project];
        assert!(kept.contains(Path::new("a")));
        assert!(kept.contains(Path::new("a/b/c")));
        assert!(kept.contains(Path::new("a/b/c/d")));
        assert!(kept.contains(Path::new("a/b/c/d/e")));
    }

    #[test]
    fn icon_table_covers_common_types_with_sane_fallbacks() {
        use omaterm_core::FileKind;
        let rust = icon_for(Path::new("src/main.rs"), FileKind::File, false);
        assert_eq!((rust.glyph, rust.color), ('\u{e68b}', Some(0xDEA584)));
        assert_eq!(
            icon_for(Path::new("Cargo.toml"), FileKind::File, false).glyph,
            '\u{e6b2}'
        );
        assert_eq!(
            icon_for(Path::new("README.md"), FileKind::File, false).glyph,
            '\u{e609}'
        );
        assert_eq!(
            icon_for(Path::new("LICENSE-MIT"), FileKind::File, false).glyph,
            '\u{e60a}'
        );
        assert_eq!(
            icon_for(Path::new("notes.txt"), FileKind::File, false).glyph,
            '\u{e64e}'
        );
        // Unknown extensions fall back; folders pair open/closed.
        assert_eq!(
            icon_for(Path::new("data.blob"), FileKind::File, false),
            FALLBACK_FILE_ICON
        );
        assert_eq!(
            icon_for(Path::new("src"), FileKind::Directory, false),
            FOLDER_CLOSED_ICON
        );
        assert_eq!(
            icon_for(Path::new("src"), FileKind::Directory, true),
            FOLDER_OPEN_ICON
        );
        assert_eq!(
            icon_for(Path::new(".git"), FileKind::Directory, false).glyph,
            '\u{e65d}'
        );
        assert_eq!(
            icon_for(Path::new("node_modules"), FileKind::Directory, false).glyph,
            '\u{e616}'
        );
    }

    /// Every glyph in the icon table must exist in the shipped Nerd Font.
    /// Skips gracefully where the font is not installed (CI runners).
    #[test]
    fn icon_glyphs_exist_in_nerd_font() {
        let candidates = [
            "/usr/share/fonts/TTF/JetBrainsMonoNerdFont-Regular.ttf",
            "/usr/share/fonts/JetBrainsMonoNerdFont-Regular.ttf",
        ];
        let path = candidates
            .iter()
            .find(|candidate| std::path::Path::new(candidate).is_file());
        let Some(path) = path else {
            eprintln!("nerd font not installed; skipping glyph coverage");
            return;
        };
        let bytes = std::fs::read(path).expect("readable nerd font");
        let face = ttf_parser::Face::parse(&bytes, 0).expect("parseable nerd font");
        let mut glyphs: Vec<char> = NAME_ICONS
            .iter()
            .map(|(_, glyph, _)| *glyph)
            .chain(EXT_ICONS.iter().map(|(_, glyph, _)| *glyph))
            .chain([
                FALLBACK_FILE_ICON.glyph,
                FOLDER_CLOSED_ICON.glyph,
                FOLDER_OPEN_ICON.glyph,
                SEARCH_ICON,
                CHEVRON_RIGHT,
                CHEVRON_DOWN,
                crate::git_panel::STAGE_ICON,
                crate::git_panel::UNSTAGE_ICON,
                crate::git_panel::DISCARD_ICON,
                crate::git_panel::REFRESH_ICON,
                crate::git_panel::COMMIT_ICON,
                '\u{e609}',
                '\u{e60a}',
                '\u{e616}',
                '\u{e65d}',
            ])
            .collect();
        glyphs.sort_unstable();
        glyphs.dedup();
        assert!(!glyphs.is_empty());
        for glyph in glyphs {
            assert!(
                face.glyph_index(glyph).is_some(),
                "U+{:04X} missing from JetBrainsMono Nerd Font",
                glyph as u32
            );
        }
    }

    #[test]
    fn scroll_thumb_covers_fit_top_middle_bottom_and_clamp() {
        // Everything fits: full thumb, no travel.
        assert_eq!(scroll_thumb(0, 10, 0), (0.0, 1.0));
        assert_eq!(scroll_thumb(10, 10, 0), (0.0, 1.0));
        assert_eq!(scroll_thumb(5, 10, 3), (0.0, 1.0));
        // Quarter visible: quarter thumb sliding over the rest.
        let (top, height) = scroll_thumb(40, 10, 0);
        assert!((height - 0.25).abs() < 1e-6);
        assert!((top - 0.0).abs() < 1e-6);
        let (top, _) = scroll_thumb(40, 10, 15);
        assert!((top - 0.375).abs() < 1e-6);
        let (top, _) = scroll_thumb(40, 10, 30);
        assert!((top - 0.75).abs() < 1e-6);
        // Over-clamped offsets pin to the end, never past it or NaN.
        let (top, _) = scroll_thumb(40, 10, 999);
        assert!((top - 0.75).abs() < 1e-6);
        assert!(scroll_thumb(40, 10, 999).0.is_finite());
    }

    #[test]
    fn highlight_ranges_merge_consecutive_and_survive_unicode() {
        assert_eq!(highlight_ranges("main.rs", &[0, 1, 4]), vec![0..2, 4..5]);
        assert!(highlight_ranges("", &[0]).is_empty());
        assert!(highlight_ranges("ab", &[7]).is_empty());
        // Wide chars map char indices to byte ranges without splitting.
        // "雪main": bytes 0..3, 3..4, 4..5, 5..6, 6..7.
        assert_eq!(highlight_ranges("雪main", &[0, 2]), vec![0..3, 4..5]);
        assert_eq!(highlight_ranges("雪main", &[0, 1, 2, 3, 4]), vec![0..7]);
    }

    #[test]
    fn restore_and_snapshot_round_trip_within_bounds() {
        let mut panel = FilePanel::default();
        let project = ProjectId::new();
        let dirs: Vec<PathBuf> = (0..200)
            .map(|index| PathBuf::from(format!("dir{index}")))
            .collect();
        panel.restore(vec![(project, dirs)]);
        let snapshot = panel.expanded_snapshot();
        assert_eq!(snapshot[&project].len(), MAX_EXPANDED_DIRS);
        assert_eq!(snapshot[&project][0], PathBuf::from("dir0"));
    }
}
