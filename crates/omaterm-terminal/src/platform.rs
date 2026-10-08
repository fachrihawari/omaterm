//! OS boundary traits (M11 task 11I, blueprint §50–§51).
//!
//! OS-specific functionality lives behind these small interfaces so future
//! platform work replaces adapters instead of rewriting workspace logic. No
//! `cfg(target_os)` is scattered through workspace modules; the Linux
//! adapters below read `/proc` directly with no new dependencies.
//!
//! Wired today: [`ProcessInspector::cwd_of`] backs CWD refresh. The
//! clipboard path stays on the desktop's GPUI clipboard until a headless
//! consumer needs the seam, and desktop notifications arrive with agent
//! awareness (v0.3) — both traits exist so that work has a seam to target.
//!
//! M18 adds the query prerequisites (blueprint §51, §64): per-process CPU and
//! RSS, a bounded one-shot [`ProcessSnapshot`], and a best-effort
//! [`ProcessInspector::terminate`]. Everything Linux-specific stays in this
//! adapter module — no `cfg` branches leak into workspace logic.

use std::collections::{HashMap, HashSet};
use std::io::Read;
#[cfg(unix)]
use std::os::fd::{AsRawFd, FromRawFd, OwnedFd};
use std::path::PathBuf;
use std::sync::{
    Arc,
    atomic::{AtomicBool, Ordering},
};
use std::time::Instant;

const MAX_SCANNED_PROCESSES: usize = 16_384;
const MAX_FDS: usize = 65_536;
const MAX_PORTS: usize = 4096;
const MAX_PROC_BYTES: usize = 4096;
const MAX_TCP_BYTES: usize = 4 * 1024 * 1024;
const MAX_ROOTS: usize = 512;

/// Cooperative cancellation and a deadline shared by all stages of one scan.
#[derive(Debug, Clone)]
pub struct ProcessScanControl {
    cancelled: Arc<AtomicBool>,
    deadline: Instant,
}

impl ProcessScanControl {
    pub fn new(timeout: std::time::Duration) -> Self {
        Self {
            cancelled: Arc::new(AtomicBool::new(false)),
            deadline: Instant::now() + timeout,
        }
    }
    pub fn cancel(&self) {
        self.cancelled.store(true, Ordering::Relaxed);
    }
    pub fn stopped(&self) -> bool {
        self.cancelled.load(Ordering::Relaxed) || self.expired()
    }
    pub fn expired(&self) -> bool {
        Instant::now() >= self.deadline
    }
}

fn bounded_text(path: impl AsRef<std::path::Path>, cap: usize) -> Option<String> {
    let mut bytes = Vec::new();
    std::fs::File::open(path)
        .ok()?
        .take((cap + 1) as u64)
        .read_to_end(&mut bytes)
        .ok()?;
    if bytes.len() > cap {
        return None;
    }
    String::from_utf8(bytes).ok()
}

/// One inspected process.
#[derive(Debug, Clone, PartialEq)]
pub struct ProcessInfo {
    pub pid: u32,
    pub ppid: u32,
    /// Command name from `/proc/<pid>/stat` (may be truncated by the kernel).
    pub name: String,
    /// CPU usage since the previous [`CpuSampler::sample`]. Always `None` for
    /// a single stat read: a lone sample has no time delta to divide by.
    pub cpu_percent: Option<f32>,
    /// Resident set size in bytes, from `/proc/<pid>/statm`.
    pub memory_bytes: Option<u64>,
}

/// One listening TCP port attributed to an inspected process tree.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ListeningPort {
    pub port: u16,
    pub pid: u32,
}

/// Bounded one-shot view of a set of process trees.
///
/// Produced by [`ProcessInspector::snapshot`]: `/proc` is scanned at most once
/// for the whole request. Table, socket, FD and byte budgets bound intermediate
/// storage; `cap` bounds retained rows, with ownership resolved before that cap.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct ProcessSnapshot {
    pub processes: Vec<ProcessInfo>,
    pub ports: Vec<ListeningPort>,
    /// Set for any omitted data: row/scan/socket/FD/byte budget, inaccessible
    /// proc data, identity changes, cancellation or deadline.
    pub truncated: bool,
    /// Attribution computed from the complete bounded table BEFORE row truncation.
    pub roots: HashMap<u32, u32>,
    /// CPU identity/time captured in the same stat read as the row.
    pub times: HashMap<u32, (u64, u64, u64)>,
}

/// Failure modes for [`ProcessInspector::terminate`].
///
/// Stable variants so callers/agents do not match on error text.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TerminateError {
    NotFound,
    NotOwned,
    PermissionDenied,
    Other(std::io::ErrorKind),
}

/// Process inspection seam: descendants, CWD, listening ports, and the M18
/// snapshot/terminate operations.
pub trait ProcessInspector {
    fn process_info(&self, pid: u32) -> Option<ProcessInfo>;
    fn descendants(&self, pid: u32) -> Vec<ProcessInfo>;
    fn cwd_of(&self, pid: u32) -> Option<PathBuf>;
    fn listening_ports(&self, pid: u32) -> Vec<ListeningPort>;

    /// Bounded one-shot snapshot of `roots` and their descendants (deduped).
    ///
    /// The default implementation uses the Linux adapter helpers; a future
    /// non-Linux adapter overrides it. Kept as a default so existing
    /// implementors (e.g. test stubs) do not have to grow a method they do not
    /// exercise.
    fn snapshot(&self, roots: &[u32], cap: usize) -> ProcessSnapshot {
        #[cfg(unix)]
        {
            linux_snapshot(roots, cap)
        }
        #[cfg(not(unix))]
        {
            let _ = (roots, cap);
            ProcessSnapshot::default()
        }
    }

    /// Request termination of `pid` with `SIGTERM`.
    ///
    /// Default delegates to the Linux free function below. `&mut self` is part
    /// of the contract even though this implementation is stateless, leaving
    /// room for adapters that track outstanding requests.
    fn terminate(&mut self, pid: u32) -> Result<(), TerminateError> {
        #[cfg(unix)]
        {
            linux_terminate(pid)
        }
        #[cfg(not(unix))]
        {
            let _ = pid;
            Err(TerminateError::Other(std::io::ErrorKind::Unsupported))
        }
    }
}

/// Windows adapter. Process listing stays empty until a native query exists;
/// the terminal and CLI do not depend on it.
#[cfg(windows)]
#[derive(Debug, Clone, Copy, Default)]
pub struct WindowsProcessInspector;

#[cfg(windows)]
impl WindowsProcessInspector {
    pub fn snapshot_controlled(
        &self,
        roots: &[u32],
        cap: usize,
        control: &ProcessScanControl,
    ) -> ProcessSnapshot {
        let _ = (roots, cap, control);
        ProcessSnapshot::default()
    }
}

#[cfg(windows)]
impl ProcessInspector for WindowsProcessInspector {
    fn process_info(&self, _pid: u32) -> Option<ProcessInfo> {
        None
    }

    fn descendants(&self, _pid: u32) -> Vec<ProcessInfo> {
        Vec::new()
    }

    fn cwd_of(&self, _pid: u32) -> Option<PathBuf> {
        None
    }

    fn listening_ports(&self, _pid: u32) -> Vec<ListeningPort> {
        Vec::new()
    }

    fn snapshot(&self, _roots: &[u32], _cap: usize) -> ProcessSnapshot {
        ProcessSnapshot::default()
    }

    fn terminate(&mut self, _pid: u32) -> Result<(), TerminateError> {
        Err(TerminateError::Other(std::io::ErrorKind::Unsupported))
    }
}

#[cfg(unix)]
pub type HostProcessInspector = LinuxProcessInspector;
#[cfg(windows)]
pub type HostProcessInspector = WindowsProcessInspector;

/// Clipboard seam. The desktop's GPUI clipboard remains the direct
/// implementation until clipboard policy needs headless coverage.
pub trait ClipboardProvider {
    fn read_text(&self) -> Option<String>;
    fn write_text(&self, text: &str);
}

/// Desktop-notification seam for future attention indicators (v0.3).
pub trait NotificationProvider {
    fn notify(&self, title: &str, body: &str);
}

/// Linux adapter: `/proc` scan, no new dependencies.
#[cfg(unix)]
#[derive(Debug, Clone, Copy, Default)]
pub struct LinuxProcessInspector;

#[cfg(unix)]
impl LinuxProcessInspector {
    pub fn snapshot_controlled(
        &self,
        roots: &[u32],
        cap: usize,
        control: &ProcessScanControl,
    ) -> ProcessSnapshot {
        let roots_truncated = roots.len() > MAX_ROOTS;
        let roots = &roots[..roots.len().min(MAX_ROOTS)];
        let cap = cap.min(MAX_SCANNED_PROCESSES);
        let mut processes = Vec::new();
        let mut times = HashMap::new();
        let mut truncated = roots_truncated;
        if roots.is_empty() {
            return ProcessSnapshot::default();
        }
        if let Ok(entries) = std::fs::read_dir("/proc") {
            for (scanned, entry) in entries.enumerate() {
                if scanned == MAX_SCANNED_PROCESSES || control.stopped() {
                    truncated = true;
                    break;
                }
                let Ok(entry) = entry else {
                    truncated = true;
                    continue;
                };
                let Ok(pid) = entry.file_name().to_string_lossy().parse::<u32>() else {
                    continue;
                };
                let Some(text) = bounded_text(format!("/proc/{pid}/stat"), MAX_PROC_BYTES) else {
                    truncated = true;
                    continue;
                };
                if let (Some(mut info), Some(time)) =
                    (parse_stat(&text, pid), parse_stat_times(&text))
                {
                    info.memory_bytes = read_statm(pid);
                    times.insert(pid, time);
                    processes.push(info);
                } else {
                    truncated = true;
                }
            }
        } else {
            truncated = true;
        }
        // A parent cannot have started after its child. A reused parent PID
        // seen later in this non-atomic scan must not adopt the old family.
        for process in &mut processes {
            if let (Some(parent), Some(child)) = (times.get(&process.ppid), times.get(&process.pid))
                && parent.0 > child.0
            {
                process.ppid = 0;
                truncated = true;
            }
        }
        let root_times: HashMap<_, _> = roots
            .iter()
            .filter_map(|pid| times.get(pid).map(|t| (*pid, t.0)))
            .collect();
        let mut snapshot = snapshot_from(roots, cap, &processes);
        snapshot.truncated |= truncated;
        times.retain(|pid, _| snapshot.roots.contains_key(pid));
        snapshot.times = times;
        let pids = snapshot.processes.iter().map(|p| p.pid).collect();
        let (ports, partial) = bounded_ports(&pids, control);
        snapshot.ports = ports;
        snapshot.truncated |= partial || control.stopped();
        // FD/statm reads use numeric proc paths. Reject rows (and whole root
        // families) whose identity changed while those reads were in progress.
        let invalid_roots: HashSet<_> = root_times
            .iter()
            .filter_map(|(pid, start)| {
                (control.stopped() || read_stat_times(*pid).map(|t| t.0) != Some(*start))
                    .then_some(*pid)
            })
            .collect();
        snapshot.processes.retain(|process| {
            if control.stopped() {
                return false;
            }
            let valid = snapshot.times.get(&process.pid).map(|t| t.0)
                == read_stat_times(process.pid).map(|t| t.0)
                && !invalid_roots.contains(&snapshot.roots[&process.pid]);
            if !valid {
                snapshot.truncated = true;
            }
            valid
        });
        let retained: HashSet<_> = snapshot.processes.iter().map(|p| p.pid).collect();
        snapshot.roots.retain(|pid, _| retained.contains(pid));
        snapshot.times.retain(|pid, _| retained.contains(pid));
        snapshot.ports.retain(|port| retained.contains(&port.pid));
        snapshot.truncated |= control.stopped();
        snapshot
    }

    /// Bind the target with a pidfd before authorization; never fall back to
    /// numeric kill. Re-read the ancestry immediately before pidfd signalling.
    pub fn terminate_owned(&mut self, pid: u32, roots: &[u32]) -> Result<(), TerminateError> {
        let fd = open_pidfd(pid)?;
        let chain = ancestry(pid, roots)?;
        for (id, parent, start) in chain.into_iter().rev() {
            let text = bounded_text(format!("/proc/{id}/stat"), MAX_PROC_BYTES)
                .ok_or(TerminateError::NotFound)?;
            if parse_stat(&text, id).map(|p| p.ppid) != Some(parent)
                || parse_stat_times(&text).map(|t| t.0) != Some(start)
            {
                return Err(TerminateError::NotFound);
            }
        }
        signal_pidfd(&fd)
    }

    fn all_processes() -> Vec<ProcessInfo> {
        let mut processes = Vec::new();
        let Ok(entries) = std::fs::read_dir("/proc") else {
            return processes;
        };
        for entry in entries.take(MAX_SCANNED_PROCESSES).flatten() {
            let name = entry.file_name().to_string_lossy().into_owned();
            let Ok(pid) = name.parse::<u32>() else {
                continue;
            };
            if let Some(info) = read_stat(pid) {
                processes.push(info);
            }
        }
        processes
    }
}

#[cfg(unix)]
impl ProcessInspector for LinuxProcessInspector {
    fn process_info(&self, pid: u32) -> Option<ProcessInfo> {
        read_stat(pid)
    }

    fn descendants(&self, pid: u32) -> Vec<ProcessInfo> {
        let processes = Self::all_processes();
        collect_descendants(pid, &processes)
    }

    fn cwd_of(&self, pid: u32) -> Option<PathBuf> {
        std::fs::read_link(format!("/proc/{pid}/cwd")).ok()
    }

    fn listening_ports(&self, pid: u32) -> Vec<ListeningPort> {
        self.snapshot(&[pid], MAX_SCANNED_PROCESSES).ports
    }

    fn snapshot(&self, roots: &[u32], cap: usize) -> ProcessSnapshot {
        self.snapshot_controlled(
            roots,
            cap,
            &ProcessScanControl::new(std::time::Duration::from_secs(2)),
        )
    }

    fn terminate(&mut self, pid: u32) -> Result<(), TerminateError> {
        linux_terminate(pid)
    }
}

/// Descendants of `pid` from an already-read process table (deduped, cycle-safe).
#[cfg(unix)]
fn collect_descendants(pid: u32, processes: &[ProcessInfo]) -> Vec<ProcessInfo> {
    let mut children: HashMap<u32, Vec<&ProcessInfo>> = HashMap::new();
    for process in processes {
        children.entry(process.ppid).or_default().push(process);
    }
    let mut out = Vec::new();
    let mut stack = vec![pid];
    let mut seen = HashSet::from([pid]);
    while let Some(parent) = stack.pop() {
        if let Some(kids) = children.remove(&parent) {
            for kid in kids {
                if seen.insert(kid.pid) {
                    stack.push(kid.pid);
                    out.push(kid.clone());
                }
            }
        }
    }
    out
}

/// Build a bounded snapshot from a process table already read once.
#[cfg(unix)]
fn snapshot_from(roots: &[u32], cap: usize, processes: &[ProcessInfo]) -> ProcessSnapshot {
    if processes.is_empty() || roots.is_empty() {
        return ProcessSnapshot::default();
    }
    let mut truncated = false;

    let mut children: HashMap<u32, Vec<u32>> = HashMap::new();
    for process in processes {
        children.entry(process.ppid).or_default().push(process.pid);
    }
    let present: HashSet<_> = processes.iter().map(|p| p.pid).collect();
    let mut owner: HashMap<u32, u32> = roots
        .iter()
        .filter(|root| present.contains(root))
        .map(|root| (*root, *root))
        .collect();
    truncated |= roots.iter().any(|root| !present.contains(root));
    let mut queue: std::collections::VecDeque<u32> = owner.keys().copied().collect();
    while let Some(parent) = queue.pop_front() {
        if let Some(kids) = children.remove(&parent) {
            for kid in kids {
                let root = owner[&parent];
                if let std::collections::hash_map::Entry::Vacant(entry) = owner.entry(kid) {
                    entry.insert(root);
                    queue.push_back(kid);
                }
            }
        }
    }

    let mut selected: Vec<&ProcessInfo> = processes
        .iter()
        .filter(|process| owner.contains_key(&process.pid))
        .collect();
    selected.sort_by_key(|process| process.pid);
    selected.dedup_by_key(|process| process.pid);
    if selected.len() > cap {
        selected.truncate(cap);
        truncated = true;
    }
    let result: Vec<_> = selected.into_iter().cloned().collect();
    owner.retain(|pid, _| result.iter().any(|p| p.pid == *pid));
    ProcessSnapshot {
        processes: result,
        ports: Vec::new(),
        truncated,
        roots: owner,
        times: HashMap::new(),
    }
}

/// Parse `(comm)`, ppid, and memory from `/proc/<pid>/stat` + `statm`. The
/// comm field may contain spaces and parentheses, so the split anchors on the
/// last `)`. CPU is left `None`: one read has no delta.
#[cfg(unix)]
fn read_stat(pid: u32) -> Option<ProcessInfo> {
    let text = bounded_text(format!("/proc/{pid}/stat"), MAX_PROC_BYTES)?;
    let mut info = parse_stat(&text, pid)?;
    info.memory_bytes = read_statm(pid);
    Some(info)
}

#[cfg(unix)]
fn parse_stat(text: &str, pid: u32) -> Option<ProcessInfo> {
    let close = text.rfind(')')?;
    let (comm, rest) = text.split_at(close);
    let name = comm.split_once('(')?.1.to_owned();
    let mut fields = rest[1..].split_whitespace();
    fields.next()?; // state
    let ppid = fields.next()?.parse::<u32>().ok()?;
    Some(ProcessInfo {
        pid,
        ppid,
        name,
        cpu_percent: None,
        memory_bytes: None,
    })
}

/// Resident set size in bytes from `/proc/<pid>/statm` (field 2 = resident
/// pages). Returns `None` if the file is unreadable or page size is unknown.
#[cfg(unix)]
fn read_statm(pid: u32) -> Option<u64> {
    let text = bounded_text(format!("/proc/{pid}/statm"), MAX_PROC_BYTES)?;
    let resident = parse_statm_resident(&text)?;
    let page_size = page_size()?;
    resident.checked_mul(page_size)
}

/// Parse the resident field (second, 0-indexed first = size) of `statm`.
#[cfg(unix)]
fn parse_statm_resident(text: &str) -> Option<u64> {
    text.split_whitespace().nth(1)?.parse::<u64>().ok()
}

/// Page size in bytes via `sysconf(_SC_PAGESIZE)`, falling back to 4096.
#[cfg(unix)]
fn page_size() -> Option<u64> {
    // SAFETY: `sysconf` with a valid name is thread-safe and has no memory
    // preconditions; the result is checked before use.
    let value = unsafe { libc::sysconf(libc::_SC_PAGESIZE) };
    if value > 0 {
        Some(value as u64)
    } else {
        Some(4096)
    }
}

/// Clock ticks per second via `sysconf(_SC_CLK_TCK)`; 100 Hz is the Linux
/// default assumed when the value is unavailable. Documented fallback only.
fn clock_ticks_per_second() -> f64 {
    #[cfg(unix)]
    {
        // SAFETY: as above; `_SC_CLK_TCK` is a valid, side-effect-free name.
        let value = unsafe { libc::sysconf(libc::_SC_CLK_TCK) };
        if value > 0 { value as f64 } else { 100.0 }
    }
    #[cfg(not(unix))]
    {
        100.0
    }
}

/// `(start_time, utime, stime)` in clock ticks from `/proc/<pid>/stat`.
///
/// Parses the whitespace fields *after* the `(comm)` field: utime is stat
/// field 14 and stime field 15, starttime field 22 (1-indexed). Comm may
/// contain spaces/parens, so the anchor is the last `)`. All three are in
/// clock ticks (`sysconf(_SC_CLK_TCK)`).
fn parse_stat_times(text: &str) -> Option<(u64, u64, u64)> {
    let close = text.rfind(')')?;
    let rest = text.get(close + 1..)?;
    let fields: Vec<&str> = rest.split_whitespace().collect();
    // After comm: state=0, ppid=1, ... utime=11, stime=12, ..., starttime=19.
    let utime = fields.get(11)?.parse::<u64>().ok()?;
    let stime = fields.get(12)?.parse::<u64>().ok()?;
    let start_time = fields.get(19)?.parse::<u64>().ok()?;
    Some((start_time, utime, stime))
}

/// Bounded CPU time-delta sampler.
///
/// Holds the previous `(start_time, jiffies, Instant)` per pid. Construct once
/// per observer and call [`CpuSampler::sample`] on each refresh; the first
/// sample for a pid yields `cpu_percent = None` because there is no delta yet.
/// PID reuse (changed `starttime`) resets the entry and yields `None`.
#[derive(Debug, Default)]
pub struct CpuSampler {
    previous: HashMap<u32, (u64, u64, Instant)>,
}

impl CpuSampler {
    pub fn new() -> Self {
        Self {
            previous: HashMap::new(),
        }
    }

    /// Fill in `cpu_percent` for each info by diffing against the previous
    /// sample, then retain only the pids seen in `infos` (drops stale pids).
    pub fn sample(&mut self, infos: &mut [ProcessInfo]) {
        let times = infos
            .iter()
            .filter_map(|info| read_stat_times(info.pid).map(|time| (info.pid, time)))
            .collect();
        self.sample_times(infos, &times);
    }

    pub fn sample_snapshot(&mut self, snapshot: &mut ProcessSnapshot) {
        self.sample_times(&mut snapshot.processes, &snapshot.times);
    }

    fn sample_times(&mut self, infos: &mut [ProcessInfo], times: &HashMap<u32, (u64, u64, u64)>) {
        let now = Instant::now();
        let hertz = clock_ticks_per_second();
        let mut seen: HashSet<u32> = HashSet::with_capacity(infos.len());
        for info in infos.iter_mut() {
            let Some(&(start_time, utime, stime)) = times.get(&info.pid) else {
                info.cpu_percent = None;
                continue;
            };
            seen.insert(info.pid);
            let jiffies = utime.saturating_add(stime);
            info.cpu_percent = match self.previous.get(&info.pid) {
                Some(&(prev_start, prev_jiffies, prev_at))
                    if prev_start == start_time && jiffies >= prev_jiffies =>
                {
                    let elapsed = now.duration_since(prev_at).as_secs_f64();
                    let delta = jiffies.saturating_sub(prev_jiffies) as f64;
                    if elapsed > 0.0 {
                        Some((delta / hertz / elapsed * 100.0) as f32)
                    } else {
                        None
                    }
                }
                // First sample, or PID reused with a different start time.
                _ => None,
            };
            self.previous.insert(info.pid, (start_time, jiffies, now));
        }
        self.previous.retain(|pid, _| seen.contains(pid));
    }
}

fn read_stat_times(pid: u32) -> Option<(u64, u64, u64)> {
    let text = bounded_text(format!("/proc/{pid}/stat"), MAX_PROC_BYTES)?;
    parse_stat_times(&text)
}

/// SIGTERM via a pidfd, so PID reuse cannot redirect the signal. Unsupported
/// kernels fail closed rather than falling back to numeric/group signalling.
///
/// Mapping is by `errno`: `ESRCH` → [`TerminateError::NotFound`], `EPERM` →
/// [`TerminateError::PermissionDenied`], anything else → [`TerminateError::Other`].
#[cfg(unix)]
fn linux_terminate(pid: u32) -> Result<(), TerminateError> {
    signal_pidfd(&open_pidfd(pid)?)
}

#[cfg(unix)]
fn syscall_error() -> TerminateError {
    match std::io::Error::last_os_error().raw_os_error() {
        Some(code) if code == libc::ESRCH => TerminateError::NotFound,
        Some(code) if code == libc::EPERM => TerminateError::PermissionDenied,
        _ => TerminateError::Other(std::io::Error::last_os_error().kind()),
    }
}

#[cfg(unix)]
fn open_pidfd(pid: u32) -> Result<OwnedFd, TerminateError> {
    if pid == 0 || pid > i32::MAX as u32 {
        return Err(TerminateError::NotFound);
    }
    // SAFETY: positive representable PID, flags=0; success owns a new fd.
    let fd = unsafe { libc::syscall(libc::SYS_pidfd_open, pid as libc::pid_t, 0) };
    if fd < 0 {
        return Err(syscall_error());
    }
    // SAFETY: successful pidfd_open returns a new owned descriptor.
    Ok(unsafe { OwnedFd::from_raw_fd(fd as i32) })
}

#[cfg(unix)]
fn signal_pidfd(fd: &OwnedFd) -> Result<(), TerminateError> {
    // SAFETY: fd stays live, SIGTERM is valid, null siginfo and flags=0.
    let rc = unsafe {
        libc::syscall(
            libc::SYS_pidfd_send_signal,
            fd.as_raw_fd(),
            libc::SIGTERM,
            std::ptr::null::<libc::siginfo_t>(),
            0,
        )
    };
    if rc == 0 {
        Ok(())
    } else {
        Err(syscall_error())
    }
}

#[cfg(unix)]
fn ancestry(pid: u32, roots: &[u32]) -> Result<Vec<(u32, u32, u64)>, TerminateError> {
    let mut chain = Vec::new();
    let mut current = pid;
    let mut child_start = u64::MAX;
    for _ in 0..512 {
        let text = bounded_text(format!("/proc/{current}/stat"), MAX_PROC_BYTES)
            .ok_or(TerminateError::NotFound)?;
        let info = parse_stat(&text, current).ok_or(TerminateError::NotFound)?;
        let start = parse_stat_times(&text).ok_or(TerminateError::NotFound)?.0;
        if start > child_start || chain.iter().any(|(id, _, _)| *id == current) {
            break;
        }
        chain.push((current, info.ppid, start));
        if roots.contains(&current) {
            return Ok(chain);
        }
        if info.ppid == 0 {
            break;
        }
        child_start = start;
        current = info.ppid;
    }
    Err(TerminateError::NotOwned)
}

/// Linux snapshot used by the trait default.
#[cfg(unix)]
fn linux_snapshot(roots: &[u32], cap: usize) -> ProcessSnapshot {
    LinuxProcessInspector.snapshot(roots, cap)
}

#[cfg(unix)]
fn bounded_ports(pids: &HashSet<u32>, control: &ProcessScanControl) -> (Vec<ListeningPort>, bool) {
    if pids.is_empty() {
        return (Vec::new(), control.stopped());
    }
    let mut listening = HashMap::new();
    let mut truncated = false;
    let mut scanned_sockets = 0;
    for table in ["/proc/net/tcp", "/proc/net/tcp6"] {
        if control.stopped() {
            return (Vec::new(), true);
        }
        let Some(text) = bounded_text(table, MAX_TCP_BYTES) else {
            truncated = true;
            continue;
        };
        for line in text.lines().skip(1) {
            if scanned_sockets == MAX_FDS || control.stopped() {
                truncated = true;
                break;
            }
            scanned_sockets += 1;
            if let Some((inode, port)) = parse_listen_line(line) {
                listening.insert(inode, port);
            }
        }
    }
    let mut ports = HashSet::new();
    let mut scanned = 0;
    'pids: for pid in pids {
        if control.stopped() {
            truncated = true;
            break;
        }
        let Ok(entries) = std::fs::read_dir(format!("/proc/{pid}/fd")) else {
            truncated = true;
            continue;
        };
        for entry in entries {
            if scanned == MAX_FDS || control.stopped() {
                truncated = true;
                break 'pids;
            }
            scanned += 1;
            let Ok(entry) = entry else {
                truncated = true;
                continue;
            };
            let Ok(link) = std::fs::read_link(entry.path()) else {
                truncated = true;
                continue;
            };
            let text = link.to_string_lossy();
            if let Some(port) = text
                .strip_prefix("socket:[")
                .and_then(|s| s.strip_suffix(']'))
                .and_then(|s| s.parse::<u64>().ok())
                .and_then(|inode| listening.get(&inode))
            {
                if ports.len() == MAX_PORTS && !ports.contains(&(*pid, *port)) {
                    truncated = true;
                    break 'pids;
                }
                ports.insert((*pid, *port));
            }
        }
    }
    let mut ports: Vec<_> = ports
        .into_iter()
        .map(|(pid, port)| ListeningPort { pid, port })
        .collect();
    ports.sort_by_key(|p| (p.pid, p.port));
    (ports, truncated)
}

#[cfg(unix)]
fn parse_listen_line(line: &str) -> Option<(u64, u16)> {
    let mut fields = line.split_whitespace();
    fields.next()?; // sl
    let local = fields.next()?;
    fields.next()?; // rem_address
    let state = fields.next()?;
    if state != "0A" {
        return None;
    }
    let hex_port = local.rsplit(':').next()?;
    let port = u16::from_str_radix(hex_port, 16).ok()?;
    // inode follows st by six fields: tx_queue:rx_queue, tr:tm_when,
    // retrnsmt, uid, timeout, then inode.
    let inode = fields.nth(5)?.parse::<u64>().ok()?;
    Some((inode, port))
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    fn process(pid: u32, ppid: u32) -> ProcessInfo {
        ProcessInfo {
            pid,
            ppid,
            name: format!("p{pid}"),
            cpu_percent: None,
            memory_bytes: None,
        }
    }

    #[test]
    fn snapshot_keeps_attribution_when_sorted_cap_drops_ancestors() {
        let table = vec![
            process(100, 1),
            process(200, 100),
            process(10, 200),
            process(11, 10),
        ];
        let snapshot = snapshot_from(&[100], 2, &table);
        assert_eq!(
            snapshot.processes.iter().map(|p| p.pid).collect::<Vec<_>>(),
            [10, 11]
        );
        assert_eq!(snapshot.roots, HashMap::from([(10, 100), (11, 100)]));
        assert!(snapshot.truncated);
        let exact = snapshot_from(&[100], 4, &table);
        assert!(!exact.truncated, "exactly at cap is complete");
        let zero = snapshot_from(&[100], 0, &table);
        assert!(zero.processes.is_empty() && zero.roots.is_empty() && zero.truncated);
    }

    #[test]
    fn overlapping_roots_and_cycles_have_stable_nearest_root_ownership() {
        let table = vec![process(100, 200), process(200, 100), process(10, 200)];
        for roots in [[100, 200], [200, 100]] {
            let snapshot = snapshot_from(&roots, 3, &table);
            assert_eq!(
                snapshot.roots,
                HashMap::from([(100, 100), (200, 200), (10, 200)])
            );
            assert!(!snapshot.truncated);
        }
    }

    #[test]
    fn absent_root_cannot_adopt_rows_from_a_truncated_table() {
        let snapshot = snapshot_from(&[100], 512, &[process(10, 100), process(11, 10)]);
        assert!(snapshot.processes.is_empty() && snapshot.roots.is_empty() && snapshot.truncated);
    }

    #[test]
    fn sampler_reuse_failed_read_and_counter_regression_reset_baseline() {
        let mut sampler = CpuSampler::new();
        let mut infos = [process(42, 1)];
        sampler.sample_times(&mut infos, &HashMap::from([(42, (100, 50, 0))]));
        sampler.sample_times(&mut infos, &HashMap::from([(42, (101, 80, 0))]));
        assert_eq!(infos[0].cpu_percent, None, "reused PID has no delta");
        sampler.sample_times(&mut infos, &HashMap::from([(42, (101, 1, 0))]));
        assert_eq!(
            infos[0].cpu_percent, None,
            "regressing counters are not zero CPU"
        );
        sampler.sample_times(&mut infos, &HashMap::new());
        assert!(!sampler.previous.contains_key(&42));
        sampler.sample_times(&mut infos, &HashMap::from([(42, (101, 2, 0))]));
        assert_eq!(infos[0].cpu_percent, None, "failed sample retires baseline");
    }

    #[test]
    fn sampler_delta_math_uses_clock_ticks_and_elapsed_time() {
        let mut sampler = CpuSampler::new();
        sampler.previous.insert(
            42,
            (100, 50, Instant::now() - std::time::Duration::from_secs(1)),
        );
        let mut infos = [process(42, 1)];
        let ticks = clock_ticks_per_second() as u64;
        sampler.sample_times(&mut infos, &HashMap::from([(42, (100, 50 + ticks, 0))]));
        let cpu = infos[0].cpu_percent.unwrap();
        assert!(
            (90.0..=100.1).contains(&cpu),
            "one CPU-second / one wall-second: {cpu}"
        );
    }

    #[test]
    fn cancelled_and_expired_scans_stop_before_proc_work() {
        let control = ProcessScanControl::new(std::time::Duration::from_secs(2));
        control.cancel();
        let snapshot =
            LinuxProcessInspector.snapshot_controlled(&[std::process::id()], 512, &control);
        assert!(snapshot.truncated && snapshot.processes.is_empty() && snapshot.ports.is_empty());
        let expired = ProcessScanControl::new(std::time::Duration::ZERO);
        assert!(
            LinuxProcessInspector
                .snapshot_controlled(&[std::process::id()], 512, &expired)
                .truncated
        );
    }

    #[test]
    fn listening_socket_is_attributed_once_even_with_duplicate_fds() {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let _duplicate = listener.try_clone().unwrap();
        let port = listener.local_addr().unwrap().port();
        let me = std::process::id();
        let snapshot = LinuxProcessInspector.snapshot(&[me], 512);
        assert_eq!(
            snapshot
                .ports
                .iter()
                .filter(|p| p.pid == me && p.port == port)
                .count(),
            1
        );
    }

    #[test]
    fn termination_rejects_group_ids_and_bound_dead_pidfd_cannot_hit_new_child() {
        for pid in [0, i32::MAX as u32 + 1, u32::MAX] {
            assert_eq!(linux_terminate(pid), Err(TerminateError::NotFound));
        }
        let mut child = std::process::Command::new("/bin/sleep")
            .arg("30")
            .spawn()
            .unwrap();
        let fd = open_pidfd(child.id()).unwrap();
        child.kill().unwrap();
        child.wait().unwrap();
        let mut replacement = std::process::Command::new("/bin/sleep")
            .arg("30")
            .spawn()
            .unwrap();
        assert_eq!(signal_pidfd(&fd), Err(TerminateError::NotFound));
        assert!(replacement.try_wait().unwrap().is_none());
        replacement.kill().unwrap();
        replacement.wait().unwrap();
    }

    #[test]
    fn terminate_owned_denies_foreign_child_then_signals_verified_family() {
        let mut child = std::process::Command::new("/bin/sleep")
            .arg("30")
            .spawn()
            .unwrap();
        let mut inspector = LinuxProcessInspector;
        assert_eq!(
            inspector.terminate_owned(child.id(), &[i32::MAX as u32]),
            Err(TerminateError::NotOwned)
        );
        assert!(child.try_wait().unwrap().is_none());
        inspector
            .terminate_owned(child.id(), &[std::process::id()])
            .unwrap();
        assert!(!child.wait().unwrap().success());
    }

    #[test]
    fn bounded_reads_distinguish_exact_cap_from_overflow() {
        let path = std::env::temp_dir().join(format!("omaterm-proc-bound-{}", std::process::id()));
        std::fs::write(&path, "1234").unwrap();
        assert_eq!(bounded_text(&path, 4).as_deref(), Some("1234"));
        assert_eq!(bounded_text(&path, 3), None);
        std::fs::remove_file(path).unwrap();
    }

    #[test]
    fn stat_parses_tricky_comm() {
        let info = parse_stat("42 (my proc (x)) S 1 2 3 4", 42).unwrap();
        assert_eq!(info.ppid, 1);
        assert_eq!(info.name, "my proc (x)");
        assert_eq!(info.cpu_percent, None);
        assert_eq!(info.memory_bytes, None);
        assert!(parse_stat("garbage", 1).is_none());
    }

    #[test]
    fn statm_resident_parses_second_field() {
        // size resident shared text lib data dt (pages)
        assert_eq!(parse_statm_resident("100 42 5 1 0 0 0"), Some(42));
        assert_eq!(parse_statm_resident("  7   3 "), Some(3));
        assert_eq!(parse_statm_resident("only-one"), None);
        assert_eq!(parse_statm_resident(""), None);
    }

    #[test]
    fn stat_times_parse_tricky_comm() {
        // Tokens after the closing `)`; indices are 0-based here: 0=state,
        // 1=ppid, and utime/stime/starttime land at 11/12/19.
        let mut tokens: Vec<String> = (0..20).map(|i| (i + 100).to_string()).collect();
        tokens[0] = "S".to_owned();
        tokens[11] = "111".to_owned(); // utime (stat field 14)
        tokens[12] = "222".to_owned(); // stime (stat field 15)
        tokens[19] = "333".to_owned(); // starttime (stat field 22)
        // A comm with spaces and an embedded closer: the parser must anchor on
        // the *last* `)`.
        let line = format!("7 (we ird) proc) {}", tokens.join(" "));
        assert_eq!(parse_stat_times(&line), Some((333, 111, 222)));
        assert_eq!(parse_stat_times("garbage"), None);
        assert_eq!(parse_stat_times("1 (x) S 2"), None);
    }

    #[test]
    fn tcp_table_parses_listen_only() {
        // sl local_address rem_address st ... inode (positions per proc(5)).
        let listen = "  12: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0";
        assert_eq!(parse_listen_line(listen), Some((12345, 0x1F90)));
        let established = listen.replacen(" 0A ", " 01 ", 1);
        assert_eq!(parse_listen_line(&established), None);
        assert_eq!(parse_listen_line("short line"), None);
    }

    #[test]
    fn inspector_sees_current_process() {
        let inspector = LinuxProcessInspector;
        let me = std::process::id();
        let info = inspector.process_info(me).expect("self must be visible");
        assert_eq!(info.pid, me);
        // Resident memory is populated from statm for a live process.
        assert!(info.memory_bytes.unwrap_or(0) > 0);
        assert_eq!(info.cpu_percent, None);
        // Self CWD resolves through the same seam CWD refresh uses.
        assert_eq!(inspector.cwd_of(me), std::env::current_dir().ok());
        // Listening scan must not fail even when nothing listens.
        let _ = inspector.listening_ports(me);
    }

    #[test]
    fn descendants_contain_spawned_child() {
        let inspector = LinuxProcessInspector;
        let mut child = std::process::Command::new("/bin/sleep")
            .arg("30")
            .spawn()
            .expect("spawn sleep");
        let found = inspector
            .descendants(std::process::id())
            .into_iter()
            .any(|info| info.pid == child.id());
        child.kill().ok();
        let _ = child.wait();
        assert!(found, "spawned child must appear as a descendant");
    }

    #[test]
    fn cpu_sampler_first_sample_is_none_then_delta() {
        let me = std::process::id();
        let mut sampler = CpuSampler::new();
        let mut infos = vec![read_stat(me).expect("self visible")];
        sampler.sample(&mut infos);
        assert_eq!(infos[0].cpu_percent, None, "first sample has no delta");

        // Burn some CPU so the second sample has a non-trivial delta.
        let mut acc = 0u64;
        for i in 0..2_000_000u64 {
            acc = acc.wrapping_add(i.wrapping_mul(i));
        }
        std::hint::black_box(acc);
        std::thread::sleep(std::time::Duration::from_millis(50));

        sampler.sample(&mut infos);
        let cpu = infos[0].cpu_percent.expect("second sample has a delta");
        assert!(cpu >= 0.0, "cpu percent must be non-negative: {cpu}");
    }

    #[test]
    fn cpu_sampler_drops_stale_pids() {
        let me = std::process::id();
        let mut sampler = CpuSampler::new();
        let mut infos = vec![read_stat(me).expect("self visible")];
        sampler.sample(&mut infos);
        assert!(sampler.previous.contains_key(&me));
        sampler.sample(&mut []);
        assert!(!sampler.previous.contains_key(&me), "stale pid dropped");
    }

    #[test]
    fn snapshot_membership_and_cap_truncation() {
        let me = std::process::id();
        let mut child_a = std::process::Command::new("/bin/sleep")
            .arg("30")
            .spawn()
            .expect("spawn sleep a");
        let mut child_b = std::process::Command::new("/bin/sleep")
            .arg("30")
            .spawn()
            .expect("spawn sleep b");

        let inspector = LinuxProcessInspector;
        let full = inspector.snapshot(&[me], 10_000);
        let pids: HashSet<u32> = full.processes.iter().map(|p| p.pid).collect();
        assert!(pids.contains(&me));
        assert!(pids.contains(&child_a.id()));
        assert!(pids.contains(&child_b.id()));
        // Sorted ascending.
        assert!(full.processes.windows(2).all(|w| w[0].pid <= w[1].pid));
        // A live table may be partial due to permissions or process/FD exit races.
        // No duplicate pids (dedup).
        assert_eq!(
            full.processes.len(),
            full.processes
                .iter()
                .map(|p| p.pid)
                .collect::<HashSet<_>>()
                .len()
        );

        let capped = inspector.snapshot(&[me], 1);
        assert_eq!(capped.processes.len(), 1);
        assert!(capped.truncated, "cap must set truncated");

        child_a.kill().ok();
        child_b.kill().ok();
        let _ = child_a.wait();
        let _ = child_b.wait();
    }

    #[test]
    fn terminate_spawned_sleep_succeeds_and_notfound_errors() {
        let mut child = std::process::Command::new("/bin/sleep")
            .arg("30")
            .spawn()
            .expect("spawn sleep");
        let pid = child.id();
        let mut inspector = LinuxProcessInspector;
        inspector.terminate(pid).expect("terminate sleep");
        let status = child.wait().expect("wait child");
        assert!(!status.success(), "sleep must be terminated by SIGTERM");
        assert!(inspector.process_info(pid).is_none(), "terminated pid gone");

        // A pid that cannot exist maps to NotFound via ESRCH.
        assert_eq!(
            inspector.terminate(i32::MAX as u32),
            Err(TerminateError::NotFound)
        );
    }
}
