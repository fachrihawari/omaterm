//! S8 resource instrumentation: a bounded, content-free metrics recorder.
//!
//! The wire shape is intentionally frozen to the strict schema consumed by
//! `scripts/m19-resource-bench.py`:
//!
//! ```json
//! {
//!   "timings_ms": {
//!     "open_enqueue_to_ready": [1.2],
//!     "edit_to_frame": [0.8],
//!     "edit_to_highlight": [0.9],
//!     "save_to_commit": [2.0],
//!     "close_to_retirement": [0.7],
//!     "restart_to_usable": [3.1]
//!   },
//!   "counters": {
//!     "rendered_rows": 42,
//!     "consulted_spans": 90,
//!     "buffer_copy_count": 1,
//!     "queue_depth": 0,
//!     "worker_count": 1,
//!     "retained_document_bytes": 1024,
//!     "active_jobs": 0,
//!     "pending_jobs": 0,
//!     "result_count": 0,
//!     "token_memory_bytes": 4096
//!   }
//! }
//! ```
//!
//! This module is deliberately GPUI-free. Keys are fixed
//! `&'static str` values: the recorder never accepts an arbitrary name, so it
//! cannot leak text, paths, tokens or secrets into the report. Timing samples
//! are kept in bounded rings. Unobserved timings are empty arrays, never
//! fabricated zero samples. The current benchmark must learn to accept these.

use std::collections::VecDeque;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Condvar, Mutex};
use std::time::Duration;

/// Cap on retained timing samples per key (percentile-ready, bounded memory).
pub const TIMING_RING_CAPACITY: usize = 512;

/// Exactly the timing series the bench schema requires.
pub const TIMING_KEYS: [&str; 6] = [
    "open_enqueue_to_ready",
    "edit_to_frame",
    "edit_to_highlight",
    "save_to_commit",
    "close_to_retirement",
    "restart_to_usable",
];

/// Exactly the counters the bench schema accepts.
pub const COUNTER_KEYS: [&str; 10] = [
    "rendered_rows",
    "consulted_spans",
    "buffer_copy_count",
    "queue_depth",
    "worker_count",
    "retained_document_bytes",
    "active_jobs",
    "pending_jobs",
    "result_count",
    "token_memory_bytes",
];

fn timing_index(name: &str) -> Option<usize> {
    TIMING_KEYS.iter().position(|key| *key == name)
}

fn counter_index(name: &str) -> Option<usize> {
    COUNTER_KEYS.iter().position(|key| *key == name)
}

/// A generation-tagged start instant used to time "edit to first matching
/// frame" and "edit to highlight landed" without retaining any text.
#[derive(Debug, Clone, Copy)]
pub struct PendingTiming {
    pub document: omaterm_core::DocumentId,
    pub generation: u64,
    pub started: std::time::Instant,
}

impl PendingTiming {
    pub fn new(document: omaterm_core::DocumentId, generation: u64) -> Self {
        Self {
            document,
            generation,
            started: std::time::Instant::now(),
        }
    }
}

#[derive(Default)]
struct MetricsInner {
    timings: [VecDeque<f64>; TIMING_KEYS.len()],
    counters: [u64; COUNTER_KEYS.len()],
}

impl MetricsInner {
    fn new() -> Self {
        Self {
            timings: std::array::from_fn(|_| VecDeque::with_capacity(16)),
            counters: [0; COUNTER_KEYS.len()],
        }
    }
}

/// Cloneable handle to the process metrics recorder. All state is behind a
/// mutex so the handle is `Send + Sync` and can be captured by worker
/// closures; `Clone` shares the same underlying rings.
#[derive(Clone)]
pub struct MetricsRecorder {
    inner: Arc<Mutex<MetricsInner>>,
    enabled: bool,
}

impl Default for MetricsRecorder {
    fn default() -> Self {
        Self::new()
    }
}

impl MetricsRecorder {
    pub fn new() -> Self {
        Self {
            inner: Arc::new(Mutex::new(MetricsInner::new())),
            enabled: true,
        }
    }

    pub fn disabled() -> Self {
        Self {
            enabled: false,
            ..Self::new()
        }
    }

    /// Record one timing sample (milliseconds) under a fixed key. Unknown
    /// names are ignored; samples past the ring capacity drop the oldest.
    pub fn record_timing(&self, name: &'static str, elapsed: Duration) {
        if !self.enabled {
            return;
        }
        let Some(index) = timing_index(name) else {
            return;
        };
        let millis = elapsed.as_secs_f64() * 1000.0;
        if !millis.is_finite() {
            return;
        }
        let mut inner = self.lock();
        let ring = &mut inner.timings[index];
        if ring.len() == TIMING_RING_CAPACITY {
            ring.pop_front();
        }
        ring.push_back(millis);
    }

    /// Add `delta` to a fixed counter, saturating at `u64::MAX`.
    pub fn add_counter(&self, name: &'static str, delta: u64) {
        if !self.enabled {
            return;
        }
        let Some(index) = counter_index(name) else {
            return;
        };
        let mut inner = self.lock();
        inner.counters[index] = inner.counters[index].saturating_add(delta);
    }

    /// Set a fixed counter to an absolute observed value.
    pub fn set_counter(&self, name: &'static str, value: u64) {
        if !self.enabled {
            return;
        }
        let Some(index) = counter_index(name) else {
            return;
        };
        let mut inner = self.lock();
        inner.counters[index] = value;
    }

    /// Read a counter's current value (tests and accessors).
    #[cfg(test)]
    pub fn counter(&self, name: &'static str) -> u64 {
        counter_index(name)
            .map(|index| self.lock().counters[index])
            .unwrap_or(0)
    }

    /// Number of retained samples for one timing key (tests).
    #[cfg(test)]
    pub fn timing_sample_count(&self, name: &'static str) -> usize {
        timing_index(name)
            .map(|index| self.lock().timings[index].len())
            .unwrap_or(0)
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, MetricsInner> {
        self.inner
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }

    /// Produce an ordered snapshot of bounded timing series and counters.
    pub fn snapshot(&self) -> MetricsSnapshot {
        let inner = self.lock();
        let timings = TIMING_KEYS
            .iter()
            .enumerate()
            .map(|(index, key)| {
                let values: Vec<f64> = inner.timings[index].iter().copied().collect();
                ((*key).to_string(), values)
            })
            .collect();
        let counters = COUNTER_KEYS
            .iter()
            .enumerate()
            .map(|(index, key)| ((*key).to_string(), inner.counters[index]))
            .collect();
        MetricsSnapshot { timings, counters }
    }

    /// Serialize a snapshot into exactly `{"timings_ms":{...},"counters":{...}}`.
    #[cfg(test)]
    pub fn to_json(&self) -> String {
        self.snapshot().to_json()
    }

    /// The configured metrics output path, or `None` when instrumentation is
    /// disabled. Instrumentation is a no-op unless `OMATERM_METRICS_PATH` is a
    /// non-empty absolute-or-relative path.
    pub fn output_path() -> Option<PathBuf> {
        let value = std::env::var_os("OMATERM_METRICS_PATH")?;
        if value.is_empty() {
            return None;
        }
        Some(PathBuf::from(value))
    }
}

/// Immutable, ordered view of recorder state.
#[derive(Debug, Clone, PartialEq)]
pub struct MetricsSnapshot {
    timings: Vec<(String, Vec<f64>)>,
    counters: Vec<(String, u64)>,
}

impl MetricsSnapshot {
    /// Serialize using fixed keys and numeric values only. Strings appear only
    /// as the frozen schema keys, never as recorded data.
    pub fn to_json(&self) -> String {
        let mut out = String::with_capacity(512);
        out.push_str("{\"timings_ms\":{");
        for (index, (key, values)) in self.timings.iter().enumerate() {
            if index > 0 {
                out.push(',');
            }
            write_json_key(&mut out, key);
            out.push(':');
            out.push('[');
            for (value_index, value) in values.iter().enumerate() {
                if value_index > 0 {
                    out.push(',');
                }
                out.push_str(&value.to_string());
            }
            out.push(']');
        }
        out.push_str("},\"counters\":{");
        for (index, (key, value)) in self.counters.iter().enumerate() {
            if index > 0 {
                out.push(',');
            }
            write_json_key(&mut out, key);
            out.push(':');
            out.push_str(&value.to_string());
        }
        out.push_str("}}");
        out
    }
}

fn write_json_key(out: &mut String, key: &str) {
    out.push('"');
    for ch in key.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            _ => out.push(ch),
        }
    }
    out.push('"');
}

#[derive(Default)]
struct EmissionState {
    pending: Option<MetricsSnapshot>,
    shutdown: bool,
}

/// One background writer, with one replaceable pending snapshot. Only the UI
/// owner owns this handle; render closures own recorder clones, never writers.
pub struct MetricsEmitter {
    state: Arc<(Mutex<EmissionState>, Condvar)>,
    thread: Option<std::thread::JoinHandle<()>>,
}

impl MetricsEmitter {
    pub fn new(path: PathBuf) -> Self {
        Self::with_writer(move |snapshot| {
            if let Err(error) = write_atomic(&path, &snapshot.to_json()) {
                // Do not log paths or arbitrary filesystem error text.
                tracing::warn!(target: "omaterm::metrics", kind = ?error.kind(), "metrics write failed");
            }
        })
    }

    fn with_writer(mut write: impl FnMut(MetricsSnapshot) + Send + 'static) -> Self {
        let state = Arc::new((Mutex::new(EmissionState::default()), Condvar::new()));
        let worker = Arc::clone(&state);
        let thread = std::thread::spawn(move || {
            loop {
                let snapshot = {
                    let (lock, wake) = &*worker;
                    let mut state = lock.lock().unwrap_or_else(|p| p.into_inner());
                    while state.pending.is_none() && !state.shutdown {
                        state = wake.wait(state).unwrap_or_else(|p| p.into_inner());
                    }
                    match state.pending.take() {
                        Some(snapshot) => snapshot,
                        None => return,
                    }
                };
                write(snapshot);
            }
        });
        Self {
            state,
            thread: Some(thread),
        }
    }

    pub fn submit(&self, snapshot: MetricsSnapshot) {
        let (lock, wake) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|p| p.into_inner());
        if !state.shutdown {
            state.pending = Some(snapshot);
            wake.notify_one();
        }
    }

    /// Drain the last snapshot and transfer the join to background teardown.
    pub fn take_shutdown_thread(&mut self) -> Option<std::thread::JoinHandle<()>> {
        let (lock, wake) = &*self.state;
        lock.lock().unwrap_or_else(|p| p.into_inner()).shutdown = true;
        wake.notify_one();
        self.thread.take()
    }
}

impl Drop for MetricsEmitter {
    fn drop(&mut self) {
        // Abnormal view destruction still drains; never block the UI on disk.
        let _ = self.take_shutdown_thread();
    }
}

/// Atomically write `contents` to `path` via a sibling temp file plus rename.
/// The temp file is created in the destination directory so `rename` stays on
/// one filesystem.
pub fn write_atomic(path: &Path, contents: &str) -> std::io::Result<()> {
    static NEXT_TEMP: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
    let mut temp = path.to_path_buf();
    let file_name = path
        .file_name()
        .map(|name| name.to_string_lossy().into_owned())
        .unwrap_or_else(|| "omaterm-metrics".to_string());
    temp.set_file_name(format!(
        ".{file_name}.tmp-{}-{}",
        std::process::id(),
        NEXT_TEMP.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
    ));
    let mut file = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&temp)?;
    let result = (|| {
        file.write_all(contents.as_bytes())?;
        file.flush()?;
        file.sync_all()?;
        std::fs::rename(&temp, path)
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&temp);
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn blocked_emission_coalesces_and_shutdown_drains_latest_off_owner_thread() {
        let owner = std::thread::current().id();
        let (entered_tx, entered_rx) = std::sync::mpsc::channel();
        let (release_tx, release_rx) = std::sync::mpsc::channel();
        let seen = Arc::new(Mutex::new(Vec::new()));
        let worker_seen = Arc::clone(&seen);
        let mut first = true;
        let mut emitter = MetricsEmitter::with_writer(move |snapshot| {
            assert_ne!(owner, std::thread::current().id());
            if first {
                entered_tx.send(()).unwrap();
                release_rx.recv().unwrap();
                first = false;
            }
            worker_seen.lock().unwrap().push(snapshot.counters[0].1);
        });
        let recorder = MetricsRecorder::new();
        emitter.submit(recorder.snapshot());
        entered_rx.recv_timeout(Duration::from_secs(5)).unwrap();
        // A blocked writer must neither block the owner nor grow its mailbox.
        for value in 1..1000 {
            recorder.set_counter("rendered_rows", value);
            emitter.submit(recorder.snapshot());
        }
        let thread = emitter.take_shutdown_thread().unwrap();
        recorder.set_counter("rendered_rows", 1000);
        emitter.submit(recorder.snapshot()); // shutdown rejects further work
        release_tx.send(()).unwrap();
        thread.join().unwrap();
        assert_eq!(*seen.lock().unwrap(), [0, 999]);
    }

    #[test]
    fn disabled_recording_and_counter_saturation_are_honest() {
        let disabled = MetricsRecorder::disabled();
        disabled.record_timing("edit_to_frame", Duration::from_nanos(1));
        disabled.add_counter("rendered_rows", 1);
        disabled.set_counter("worker_count", 1);
        assert_eq!(disabled.counter("rendered_rows"), 0);
        assert_eq!(disabled.counter("worker_count"), 0);
        assert_eq!(disabled.timing_sample_count("edit_to_frame"), 0);
        let recorder = MetricsRecorder::new();
        recorder.add_counter("rendered_rows", u64::MAX);
        recorder.add_counter("rendered_rows", 1);
        assert_eq!(recorder.counter("rendered_rows"), u64::MAX);
        recorder.record_timing("edit_to_frame", Duration::from_nanos(1));
        let parsed: serde_json::Value = serde_json::from_str(&recorder.to_json()).unwrap();
        assert!(parsed["timings_ms"]["edit_to_frame"][0].as_f64().unwrap() > 0.0);
    }

    #[test]
    fn timing_samples_are_bounded_and_percentile_ready() {
        let recorder = MetricsRecorder::new();
        for index in 0..(TIMING_RING_CAPACITY + 200) {
            recorder.record_timing("edit_to_frame", Duration::from_millis(index as u64));
        }
        assert_eq!(
            recorder.timing_sample_count("edit_to_frame"),
            TIMING_RING_CAPACITY
        );
        let snapshot = recorder.snapshot();
        let values = &snapshot.timings[1].1;
        assert_eq!(values.len(), TIMING_RING_CAPACITY);
        // The oldest samples were dropped, so the first retained sample is the
        // 200th recorded millisecond value.
        assert_eq!(values.first().copied(), Some(200.0));
    }

    #[test]
    fn serialized_metrics_keep_fixed_bench_keys_and_actual_observations() {
        let recorder = MetricsRecorder::new();
        recorder.record_timing("open_enqueue_to_ready", Duration::from_micros(1500));
        recorder.set_counter("worker_count", 1);
        recorder.set_counter("token_memory_bytes", 4096);

        let parsed: serde_json::Value =
            serde_json::from_str(&recorder.to_json()).expect("metrics JSON parses");
        let object = parsed.as_object().expect("top-level object");
        assert_eq!(object.len(), 2);
        assert!(object.contains_key("counters"));
        assert!(object.contains_key("timings_ms"));

        let timings = object["timings_ms"].as_object().expect("timings object");
        let timing_keys: Vec<&String> = timings.keys().collect();
        assert_eq!(timing_keys.len(), TIMING_KEYS.len());
        for key in TIMING_KEYS {
            let series = timings
                .get(key)
                .unwrap_or_else(|| panic!("missing timing key {key}"))
                .as_array()
                .expect("timing series array");
            assert_eq!(series.len(), usize::from(key == "open_enqueue_to_ready"));
            for value in series {
                assert!(value.as_f64().is_some(), "timing value must be numeric");
            }
        }

        let counters = object["counters"].as_object().expect("counters object");
        for key in counters.keys() {
            assert!(
                COUNTER_KEYS.contains(&key.as_str()),
                "unexpected counter {key}"
            );
        }
        assert_eq!(counters["worker_count"], serde_json::json!(1));
        assert_eq!(counters["token_memory_bytes"], serde_json::json!(4096));
    }

    #[test]
    fn unobserved_timings_are_explicitly_empty() {
        let recorder = MetricsRecorder::new();
        let snapshot = recorder.snapshot();
        assert_eq!(snapshot.timings.len(), TIMING_KEYS.len());
        for (key, values) in &snapshot.timings {
            assert!(values.is_empty(), "series {key} has no observations");
        }
        // Missing observations survive serialization and re-parsing.
        let parsed: serde_json::Value = serde_json::from_str(&recorder.to_json()).unwrap();
        for key in TIMING_KEYS {
            let series = parsed["timings_ms"][key].as_array().unwrap();
            assert!(series.is_empty());
        }
    }

    #[test]
    fn serialization_contains_only_numeric_fields_plus_fixed_keys() {
        let recorder = MetricsRecorder::new();
        recorder.record_timing("save_to_commit", Duration::from_millis(12));
        recorder.add_counter("rendered_rows", 7);
        let json = recorder.to_json();
        // No whitespace, no free-form text: only the frozen keys appear.
        assert!(!json.contains(' '));
        assert!(!json.contains('\n'));
        for key in TIMING_KEYS.iter().chain(COUNTER_KEYS.iter()) {
            assert!(json.contains(&format!("\"{key}\"")), "missing key {key}");
        }
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();
        let timings = parsed["timings_ms"].as_object().unwrap();
        assert_eq!(timings.len(), TIMING_KEYS.len());
        let counters = parsed["counters"].as_object().unwrap();
        for (key, value) in counters {
            assert!(COUNTER_KEYS.contains(&key.as_str()));
            assert!(value.is_u64());
        }
    }

    #[test]
    fn counters_merge_monotonically_and_ignore_unknown_names() {
        let recorder = MetricsRecorder::new();
        recorder.add_counter("consulted_spans", 3);
        recorder.add_counter("consulted_spans", 4);
        assert_eq!(recorder.counter("consulted_spans"), 7);
        recorder.add_counter("not_a_counter", 100);
        recorder.record_timing("not_a_timing", Duration::from_secs(1));
        let snapshot = recorder.snapshot();
        assert_eq!(snapshot.counters.len(), COUNTER_KEYS.len());
        assert!(
            snapshot
                .counters
                .iter()
                .all(|(key, _)| COUNTER_KEYS.contains(&key.as_str()))
        );
        assert!(
            snapshot
                .timings
                .iter()
                .all(|(key, _)| { TIMING_KEYS.contains(&key.as_str()) })
        );
    }

    #[test]
    fn atomic_write_replaces_existing_file() {
        let dir = std::env::temp_dir().join(format!(
            "omaterm-metrics-test-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("metrics.json");
        std::fs::write(&path, "old").unwrap();
        write_atomic(&path, "{\"timings_ms\":{}}").unwrap();
        assert_eq!(
            std::fs::read_to_string(&path).unwrap(),
            "{\"timings_ms\":{}}"
        );
        assert!(
            std::fs::read_dir(&dir)
                .unwrap()
                .filter_map(Result::ok)
                .all(|entry| entry.file_name() == "metrics.json"),
            "no temp file may remain"
        );
        std::fs::remove_dir_all(&dir).unwrap();
    }
}
