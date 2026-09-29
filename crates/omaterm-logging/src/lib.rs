//! Shared std-only `tracing` subscriber for both binaries (M11 task 11G).
//!
//! No new I/O or filtering dependency: filtering is a small directive parser
//! over `OMATERM_LOG` (fallback `RUST_LOG`, fallback `"warn"`), and events
//! render as single stderr lines. Targets follow the blueprint §45 categories
//! under the `omaterm::` prefix:
//!
//! ```text
//! omaterm::workspace  projects, tabs, selection, folder picker
//! omaterm::pane       splits, close, focus, resize, equalize
//! omaterm::terminal   session launch, readers, input, paste
//! omaterm::pty        PTY lifecycle, reap, resize propagation
//! omaterm::render     fonts, grid metrics, renderer state
//! omaterm::ipc        socket server, owner bridge, credentials lifecycle
//! omaterm::cli        CLI resolution, launch, request outcomes
//! omaterm::persistence snapshots, recovery, history config/writer
//! omaterm::files      project root resolution, file listing policy
//! omaterm::git        git status/refresh lifecycle (never diff bodies)
//! omaterm::search     filename search ranking and limits
//! ```
//!
//! Redaction is a call-site contract, not a subscriber feature: the
//! subscriber prints whatever fields it receives, so capability tokens,
//! passwords, clipboard content, terminal payloads, file contents, diff
//! bodies, and git stderr MUST NOT be emitted (see the
//! `no_secret_fields_at_call_sites` audit note in status.md). The v0.2
//! categories (`files`, `git`, `search`) log IDs, sources, counts, and
//! truncation flags only — never path contents or command output.

use std::io::Write;
use std::sync::Mutex;

use tracing::field::Visit;
use tracing::span::{Attributes, Id, Record};
use tracing::{Event, Level, Metadata, Subscriber};

/// Blueprint §45 categories under the shared prefix.
pub const CATEGORIES: &[&str] = &[
    "omaterm::workspace",
    "omaterm::pane",
    "omaterm::terminal",
    "omaterm::pty",
    "omaterm::render",
    "omaterm::ipc",
    "omaterm::cli",
    "omaterm::persistence",
    "omaterm::files",
    "omaterm::git",
    "omaterm::search",
];

/// One parsed directive: `target` (empty = default) and an optional ceiling.
/// `None` means `off`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Directive {
    pub target: String,
    pub level: Option<Level>,
}

fn parse_level(text: &str) -> Option<Option<Level>> {
    match text.trim().to_ascii_lowercase().as_str() {
        "trace" => Some(Some(Level::TRACE)),
        "debug" => Some(Some(Level::DEBUG)),
        "info" => Some(Some(Level::INFO)),
        "warn" | "warning" => Some(Some(Level::WARN)),
        "error" => Some(Some(Level::ERROR)),
        "off" => Some(None),
        _ => None,
    }
}

/// Parse `target=level,target=level,level`. Invalid entries are skipped so
/// one typo cannot silence logging; [`init_logging`] reports the count on
/// stderr once.
pub fn parse_directives(spec: &str) -> (Vec<Directive>, usize) {
    let mut directives = Vec::new();
    let mut invalid = 0;
    for part in spec.split(',') {
        let part = part.trim();
        if part.is_empty() {
            continue;
        }
        match part.split_once('=') {
            Some((target, level)) => match parse_level(level) {
                Some(level) => directives.push(Directive {
                    target: target.trim().to_owned(),
                    level,
                }),
                None => invalid += 1,
            },
            None => match parse_level(part) {
                Some(level) => directives.push(Directive {
                    target: String::new(),
                    level,
                }),
                None => invalid += 1,
            },
        }
    }
    (directives, invalid)
}

/// Ceiling for `target`: longest-prefix directive wins, else the default.
/// Absent default means `off`.
pub fn max_level_for(directives: &[Directive], target: &str) -> Option<Level> {
    let mut best: Option<(usize, Option<Level>)> = None;
    for directive in directives {
        if directive.target.is_empty() {
            if best.is_none() {
                best = Some((0, directive.level));
            }
            continue;
        }
        if target == directive.target || target.starts_with(&format!("{}::", directive.target)) {
            let length = directive.target.len();
            if best.is_none_or(|(len, _)| length > len) {
                best = Some((length, directive.level));
            }
        }
    }
    best.and_then(|(_, level)| level)
}

#[derive(Debug, Default)]
struct FieldRecorder {
    fields: Vec<(String, String)>,
}

impl Visit for FieldRecorder {
    fn record_debug(&mut self, field: &tracing::field::Field, value: &dyn std::fmt::Debug) {
        self.fields
            .push((field.name().to_owned(), format!("{value:?}")));
    }
}

/// Single-line stderr subscriber. Generic over the sink so tests can capture
/// output without a global install.
pub struct OmaSubscriber<W: Write + Send + 'static> {
    directives: Vec<Directive>,
    writer: Mutex<W>,
}

impl<W: Write + Send + 'static> OmaSubscriber<W> {
    pub fn new(spec: &str, writer: W) -> (Self, usize) {
        let (directives, invalid) = parse_directives(spec);
        (
            Self {
                directives,
                writer: Mutex::new(writer),
            },
            invalid,
        )
    }

    fn format_event(&self, event: &Event<'_>) -> Vec<u8> {
        let metadata = event.metadata();
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|elapsed| elapsed.as_millis())
            .unwrap_or(0);
        let mut recorder = FieldRecorder::default();
        event.record(&mut recorder);
        let mut line = format!("{now} {} {}: ", metadata.level(), metadata.target());
        let mut rest = Vec::new();
        for (name, value) in &recorder.fields {
            if name == "message" {
                line.push_str(value);
            } else {
                rest.push(format!("{name}={value}"));
            }
        }
        if !rest.is_empty() {
            line.push(' ');
            line.push_str(&rest.join(" "));
        }
        line.push('\n');
        line.into_bytes()
    }
}

impl<W: Write + Send + 'static> Subscriber for OmaSubscriber<W> {
    fn enabled(&self, metadata: &Metadata<'_>) -> bool {
        match max_level_for(&self.directives, metadata.target()) {
            None => false,
            Some(ceiling) => metadata.level() <= &ceiling,
        }
    }

    fn new_span(&self, _span: &Attributes<'_>) -> Id {
        use std::sync::atomic::{AtomicU64, Ordering};
        static NEXT: AtomicU64 = AtomicU64::new(1);
        Id::from_u64(NEXT.fetch_add(1, Ordering::Relaxed))
    }

    fn record(&self, _span: &Id, _values: &Record<'_>) {}

    fn record_follows_from(&self, _span: &Id, _follows: &Id) {}

    fn event(&self, event: &Event<'_>) {
        if !self.enabled(event.metadata()) {
            return;
        }
        let bytes = self.format_event(event);
        if let Ok(mut writer) = self.writer.lock() {
            let _ = writer.write_all(&bytes);
            let _ = writer.flush();
        }
    }

    fn enter(&self, _span: &Id) {}

    fn exit(&self, _span: &Id) {}
}

/// Install the process-wide subscriber from `OMATERM_LOG`/`RUST_LOG`.
/// Idempotent: a second call (notably in tests) keeps the first install.
pub fn init_logging() {
    let spec = std::env::var("OMATERM_LOG")
        .or_else(|_| std::env::var("RUST_LOG"))
        .unwrap_or_default();
    let spec = if spec.trim().is_empty() {
        "warn".to_owned()
    } else {
        spec
    };
    let (subscriber, invalid) = OmaSubscriber::new(&spec, std::io::stderr());
    if invalid > 0 {
        eprintln!("omaterm: ignoring {invalid} invalid log directive(s) in {spec:?}");
    }
    let _ = tracing::subscriber::set_global_default(subscriber);
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{Arc, Mutex as StdMutex};

    #[derive(Clone)]
    struct SharedWriter(Arc<StdMutex<Vec<u8>>>);

    impl Write for SharedWriter {
        fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
            self.0.lock().unwrap().extend_from_slice(buf);
            Ok(buf.len())
        }

        fn flush(&mut self) -> std::io::Result<()> {
            Ok(())
        }
    }

    #[test]
    fn directives_parse_and_filter_by_prefix() {
        let (directives, invalid) =
            parse_directives("warn,omaterm::pane=debug,omaterm::ipc=off,nope");
        assert_eq!(invalid, 1);
        assert_eq!(max_level_for(&directives, "other"), Some(Level::WARN));
        assert_eq!(
            max_level_for(&directives, "omaterm::pane"),
            Some(Level::DEBUG)
        );
        // Prefix match reaches sub-targets; exact `off` silences the subtree.
        assert_eq!(
            max_level_for(&directives, "omaterm::pane::x"),
            Some(Level::DEBUG)
        );
        assert_eq!(max_level_for(&directives, "omaterm::ipc"), None);
        let (empty, _) = parse_directives("");
        assert_eq!(max_level_for(&empty, "omaterm::pane"), None);
        let (off, _) = parse_directives("off");
        assert_eq!(max_level_for(&off, "anything"), None);
    }

    #[test]
    fn events_render_single_targeted_lines() {
        let buffer = Arc::new(StdMutex::new(Vec::new()));
        let (subscriber, _) =
            OmaSubscriber::new("warn,omaterm::pane=debug", SharedWriter(buffer.clone()));
        tracing::subscriber::with_default(subscriber, || {
            tracing::warn!(target: "omaterm::pane", "split failed");
            tracing::debug!(target: "omaterm::pane", pane_id = "p1", "sized");
            tracing::debug!(target: "omaterm::ipc", "must not appear");
        });
        let text = String::from_utf8(buffer.lock().unwrap().clone()).unwrap();
        let lines: Vec<&str> = text.lines().collect();
        assert_eq!(lines.len(), 2, "unexpected output:\n{text}");
        assert!(lines[0].contains("WARN omaterm::pane: split failed"));
        assert!(lines[1].contains("DEBUG omaterm::pane: sized pane_id="));
    }

    #[test]
    fn init_logging_is_idempotent() {
        init_logging();
        init_logging();
    }
}
