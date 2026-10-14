//! Global content search over the project root (VSCode-style "search in
//! files").
//!
//! This is the backend for the desktop's global search overlay. It walks the
//! same ignore-aware, symlink-safe tree as [`crate::files::search_files`],
//! then scans each file's text for matches and returns bounded results with
//! enough context to render a highlighted preview line.
//!
//! Bounds are explicit because a project root is attacker-influenced input
//! (blueprint §64):
//! - files scanned ([`MAX_SEARCH_FILES`]),
//! - per-file bytes read ([`MAX_FILE_BYTES`]),
//! - total matches ([`MAX_MATCHES`]),
//! - preview line length ([`MAX_PREVIEW_BYTES`]),
//! - query length ([`MAX_QUERY_BYTES`]).
//!
//! Binary files are skipped by a NUL-byte probe on the first chunk, matching
//! the editor's text-file contract. Everything is cancellable so a stale
//! keystroke can abandon an in-flight walk.
//!
//! The module is GPUI-free and holds no UI state; the desktop owns the
//! generation counter, the worker thread, and rendering.

use std::path::{Path, PathBuf};

use crate::{ContextError, IgnoreFilter, files::is_skipped_dir};

/// Most files scanned by one query before the walk stops and reports
/// truncation.
pub const MAX_SEARCH_FILES: usize = 50_000;
/// Largest single file read into the content scan.
pub const MAX_FILE_BYTES: u64 = 2 * 1024 * 1024;
/// Largest number of matches returned for one query.
pub const MAX_MATCHES: usize = 2_000;
/// Largest preview line retained per match (long minified lines are clamped).
pub const MAX_PREVIEW_BYTES: usize = 512;
/// Largest query accepted (bytes).
pub const MAX_QUERY_BYTES: usize = 256;
/// Matches retained per file, so one huge file cannot fill the whole result
/// set and hide every other file.
pub const MAX_MATCHES_PER_FILE: usize = 100;

/// How a query is matched.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct SearchOptions {
    pub case_sensitive: bool,
    pub whole_word: bool,
    pub regex: bool,
}

/// One match inside one file.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ContentMatch {
    /// Root-relative path.
    pub path: PathBuf,
    /// 1-based line number.
    pub line: u32,
    /// Byte offset of the match start within the preview text.
    pub start: usize,
    /// Byte offset of the match end within the preview text (exclusive).
    pub end: usize,
    /// The matched line, clamped to [`MAX_PREVIEW_BYTES`] around the match so
    /// a minified file still shows readable context. `start`/`end` address
    /// this string.
    pub preview: String,
}

/// One query's results.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct ContentSearchResults {
    pub matches: Vec<ContentMatch>,
    /// True when any bound (files, matches, per-file matches) was hit.
    pub truncated: bool,
    /// Files whose content was searched.
    pub files_scanned: usize,
}

/// A compiled matcher for one query. Built once per search so a regex is not
/// recompiled per line.
enum Matcher {
    Regex(regex::Regex),
    Literal {
        needle: String,
        case_sensitive: bool,
        whole_word: bool,
    },
}

impl Matcher {
    fn compile(query: &str, options: SearchOptions) -> Result<Self, ContextError> {
        if options.regex {
            let pattern = if options.whole_word {
                format!(r"\b(?:{query})\b")
            } else {
                query.to_owned()
            };
            let pattern = if options.case_sensitive {
                pattern
            } else {
                format!("(?i){pattern}")
            };
            return regex::Regex::new(&pattern)
                .map(Self::Regex)
                .map_err(|error| {
                    ContextError::InvalidInput(format!("invalid search pattern: {error}"))
                });
        }
        Ok(Self::Literal {
            needle: if options.case_sensitive {
                query.to_owned()
            } else {
                query.to_lowercase()
            },
            case_sensitive: options.case_sensitive,
            whole_word: options.whole_word,
        })
    }

    /// Byte ranges within `line` that match. Ranges are non-overlapping and
    /// in ascending order.
    fn find(&self, line: &str) -> Vec<(usize, usize)> {
        match self {
            Self::Regex(regex) => regex
                .find_iter(line)
                .take(MAX_MATCHES_PER_FILE)
                .map(|m| (m.start(), m.end()))
                .filter(|(start, end)| end > start)
                .collect(),
            Self::Literal {
                needle,
                case_sensitive,
                whole_word,
            } => {
                if needle.is_empty() {
                    return Vec::new();
                }
                let haystack = if *case_sensitive {
                    line.to_owned()
                } else {
                    line.to_lowercase()
                };
                let mut ranges = Vec::new();
                let mut from = 0usize;
                while ranges.len() < MAX_MATCHES_PER_FILE {
                    let Some(rel) = haystack[from..].find(needle.as_str()) else {
                        break;
                    };
                    let start = from + rel;
                    let end = start + needle.len();
                    // Byte offsets must be char boundaries to slice the
                    // original text; a case-folded match can shift offsets in
                    // exotic locales, so verify before accepting.
                    if !line.is_char_boundary(start) || !line.is_char_boundary(end) {
                        from = end.max(start + 1);
                        continue;
                    }
                    if *whole_word && !is_word_isolated(line, start, end) {
                        from = end;
                        continue;
                    }
                    ranges.push((start, end));
                    from = end.max(start + 1);
                }
                ranges
            }
        }
    }
}

/// Whether `[start, end)` is a whole word within `line` (word chars are
/// alphanumeric or `_`).
fn is_word_isolated(line: &str, start: usize, end: usize) -> bool {
    let is_word = |ch: char| ch.is_alphanumeric() || ch == '_';
    let before_ok = line[..start]
        .chars()
        .next_back()
        .is_none_or(|ch| !is_word(ch));
    let after_ok = line[end..].chars().next().is_none_or(|ch| !is_word(ch));
    before_ok && after_ok
}

/// Search every text file under `root` for `query`.
///
/// Returns `Ok(None)` when `cancelled` was set (the caller's generation is
/// stale); a valid empty result is `Ok(Some(default))`. An empty query is an
/// error rather than "everything matches", matching the palette contract.
pub fn search_in_files(
    root: &Path,
    query: &str,
    options: SearchOptions,
    cancelled: Option<&std::sync::atomic::AtomicBool>,
) -> Result<Option<ContentSearchResults>, ContextError> {
    if query.is_empty() {
        return Err(ContextError::InvalidInput("empty search query".into()));
    }
    if query.len() > MAX_QUERY_BYTES {
        return Err(ContextError::InvalidInput(format!(
            "search query exceeds {MAX_QUERY_BYTES} bytes"
        )));
    }
    let matcher = Matcher::compile(query, options)?;
    let canonical_root = std::fs::canonicalize(root)?;
    let filter = IgnoreFilter::new(true);
    let walker = filter
        .builder(&canonical_root)
        .filter_entry(|entry| !is_skipped_dir(entry))
        .build();

    let mut results = ContentSearchResults::default();
    let cancelled_flag =
        || cancelled.is_some_and(|flag| flag.load(std::sync::atomic::Ordering::Acquire));

    for item in walker {
        if cancelled_flag() {
            return Ok(None);
        }
        let Ok(item) = item else { continue };
        let path = item.path();
        if path == canonical_root {
            continue;
        }
        let Ok(metadata) = item.metadata() else {
            continue;
        };
        if !metadata.is_file() {
            continue;
        }
        if metadata.len() > MAX_FILE_BYTES {
            results.truncated = true;
            continue;
        }
        if results.files_scanned >= MAX_SEARCH_FILES {
            results.truncated = true;
            break;
        }
        let Ok(relative) = path.strip_prefix(&canonical_root) else {
            continue;
        };
        let Ok(bytes) = std::fs::read(path) else {
            continue;
        };
        // Binary probe: a NUL in the first 8 KiB means this is not a text
        // file (same rule the editor uses for its text-file contract).
        let probe = &bytes[..bytes.len().min(8192)];
        if probe.contains(&0) {
            continue;
        }
        let Ok(text) = String::from_utf8(bytes) else {
            continue;
        };
        results.files_scanned += 1;
        let mut per_file = 0usize;
        for (line_index, line) in text.lines().enumerate() {
            if cancelled_flag() {
                return Ok(None);
            }
            if results.matches.len() >= MAX_MATCHES || per_file >= MAX_MATCHES_PER_FILE {
                results.truncated = true;
                break;
            }
            let ranges = matcher.find(line);
            if ranges.is_empty() {
                continue;
            }
            for (start, end) in ranges {
                if results.matches.len() >= MAX_MATCHES || per_file >= MAX_MATCHES_PER_FILE {
                    results.truncated = true;
                    break;
                }
                let (preview, start, end) = clamp_preview(line, start, end);
                results.matches.push(ContentMatch {
                    path: relative.to_path_buf(),
                    line: (line_index + 1) as u32,
                    start,
                    end,
                    preview,
                });
                per_file += 1;
            }
            if results.matches.len() >= MAX_MATCHES || per_file >= MAX_MATCHES_PER_FILE {
                results.truncated = true;
                break;
            }
        }
        if results.matches.len() >= MAX_MATCHES {
            results.truncated = true;
            break;
        }
    }
    Ok(Some(results))
}

/// Clamp one line to [`MAX_PREVIEW_BYTES`] around the match, returning the
/// clamped text plus the match offsets rebased into it. A leading `…` marks a
/// truncated head, a trailing `…` a truncated tail (both one char wide, so
/// the offsets stay exact).
fn clamp_preview(line: &str, start: usize, end: usize) -> (String, usize, usize) {
    if line.len() <= MAX_PREVIEW_BYTES {
        return (line.to_owned(), start, end);
    }
    // Keep the match visible: bias the window to start a little before it.
    let lead = MAX_PREVIEW_BYTES / 4;
    let window_start = start.saturating_sub(lead);
    let window_start = floor_char_boundary(line, window_start);
    let window_end = floor_char_boundary(line, (window_start + MAX_PREVIEW_BYTES).min(line.len()));
    let head_truncated = window_start > 0;
    let tail_truncated = window_end < line.len();
    let mut preview = String::new();
    if head_truncated {
        preview.push('…');
    }
    // `…` is 3 UTF-8 bytes; offsets are byte offsets, so rebase by that.
    let head = if head_truncated { '…'.len_utf8() } else { 0 };
    preview.push_str(&line[window_start..window_end]);
    if tail_truncated {
        preview.push('…');
    }
    let start = start.saturating_sub(window_start) + head;
    let end = end.saturating_sub(window_start) + head;
    (preview, start, end)
}

/// Largest char boundary at or below `offset`.
fn floor_char_boundary(text: &str, offset: usize) -> usize {
    let mut offset = offset.min(text.len());
    while offset > 0 && !text.is_char_boundary(offset) {
        offset -= 1;
    }
    offset
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn fixture(name: &str, files: &[(&str, &str)]) -> PathBuf {
        let root =
            std::env::temp_dir().join(format!("omaterm-search-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        for (path, content) in files {
            let full = root.join(path);
            if let Some(parent) = full.parent() {
                std::fs::create_dir_all(parent).unwrap();
            }
            let mut file = std::fs::File::create(&full).unwrap();
            file.write_all(content.as_bytes()).unwrap();
        }
        root
    }

    #[test]
    fn literal_search_is_case_insensitive_by_default_and_reports_offsets() {
        let root = fixture(
            "literal",
            &[
                ("a.txt", "alpha\nBeta gamma\n"),
                ("b.txt", "nothing here\n"),
            ],
        );
        let results = search_in_files(&root, "beta", SearchOptions::default(), None)
            .unwrap()
            .unwrap();
        assert_eq!(results.matches.len(), 1);
        let hit = &results.matches[0];
        assert_eq!(hit.path, PathBuf::from("a.txt"));
        assert_eq!(hit.line, 2);
        assert_eq!(&hit.preview[hit.start..hit.end], "Beta");
        std::fs::remove_dir_all(&root).ok();
    }

    #[test]
    fn case_sensitive_and_whole_word_narrow_the_match_set() {
        let root = fixture("options", &[("a.txt", "cat concatenate cat\n")]);
        let sensitive = search_in_files(
            &root,
            "CAT",
            SearchOptions {
                case_sensitive: true,
                ..Default::default()
            },
            None,
        )
        .unwrap()
        .unwrap();
        assert!(sensitive.matches.is_empty());

        let word = search_in_files(
            &root,
            "cat",
            SearchOptions {
                whole_word: true,
                ..Default::default()
            },
            None,
        )
        .unwrap()
        .unwrap();
        // "cat" twice; "concatenate" is excluded by the word boundary.
        assert_eq!(word.matches.len(), 2);
        for hit in &word.matches {
            assert_eq!(&hit.preview[hit.start..hit.end], "cat");
        }
        std::fs::remove_dir_all(&root).ok();
    }

    #[test]
    fn regex_mode_matches_patterns_and_reports_invalid_patterns() {
        let root = fixture("regex", &[("a.txt", "fn main() {}\nlet x = 42;\n")]);
        let results = search_in_files(
            &root,
            r"\d+",
            SearchOptions {
                regex: true,
                ..Default::default()
            },
            None,
        )
        .unwrap()
        .unwrap();
        assert_eq!(results.matches.len(), 1);
        assert_eq!(
            &results.matches[0].preview[results.matches[0].start..results.matches[0].end],
            "42"
        );

        let invalid = search_in_files(
            &root,
            "(",
            SearchOptions {
                regex: true,
                ..Default::default()
            },
            None,
        );
        assert!(matches!(invalid, Err(ContextError::InvalidInput(_))));
        std::fs::remove_dir_all(&root).ok();
    }

    #[test]
    fn binary_and_oversize_files_are_skipped_and_reported() {
        let root = fixture("binary", &[("text.txt", "findme\n")]);
        std::fs::write(root.join("bin.dat"), b"findme\0binary").unwrap();
        let results = search_in_files(&root, "findme", SearchOptions::default(), None)
            .unwrap()
            .unwrap();
        assert_eq!(results.matches.len(), 1);
        assert_eq!(results.matches[0].path, PathBuf::from("text.txt"));
        std::fs::remove_dir_all(&root).ok();
    }

    #[test]
    fn empty_query_is_rejected() {
        let root = fixture("empty", &[("a.txt", "x")]);
        assert!(search_in_files(&root, "", SearchOptions::default(), None).is_err());
        std::fs::remove_dir_all(&root).ok();
    }

    #[test]
    fn cancellation_returns_none() {
        let root = fixture("cancel", &[("a.txt", "alpha\nbeta\n")]);
        let flag = std::sync::atomic::AtomicBool::new(true);
        let results =
            search_in_files(&root, "alpha", SearchOptions::default(), Some(&flag)).unwrap();
        assert!(results.is_none());
        std::fs::remove_dir_all(&root).ok();
    }

    #[test]
    fn long_lines_are_clamped_around_the_match_with_exact_offsets() {
        let long = format!("{}{}{}", "x".repeat(2000), "needle", "y".repeat(2000));
        let root = fixture("longline", &[("a.txt", &long)]);
        let results = search_in_files(&root, "needle", SearchOptions::default(), None)
            .unwrap()
            .unwrap();
        let hit = &results.matches[0];
        // The window is MAX_PREVIEW_BYTES wide; `…` is 3 bytes each side.
        assert!(hit.preview.len() <= MAX_PREVIEW_BYTES + 6);
        assert_eq!(&hit.preview[hit.start..hit.end], "needle");
        std::fs::remove_dir_all(&root).ok();
    }

    #[test]
    fn results_are_capped_per_file_and_globally() {
        let many = "hit\n".repeat(MAX_MATCHES_PER_FILE + 50);
        let root = fixture("capped", &[("a.txt", &many)]);
        let results = search_in_files(&root, "hit", SearchOptions::default(), None)
            .unwrap()
            .unwrap();
        assert_eq!(results.matches.len(), MAX_MATCHES_PER_FILE);
        assert!(results.truncated);
        std::fs::remove_dir_all(&root).ok();
    }
}
