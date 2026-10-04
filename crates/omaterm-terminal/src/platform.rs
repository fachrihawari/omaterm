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
use std::path::PathBuf;
use std::time::Instant;

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
/// for the whole request, ports are read once, and growth halts at `cap`.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct ProcessSnapshot {
    pub processes: Vec<ProcessInfo>,
    pub ports: Vec<ListeningPort>,
    /// Set when the owned set exceeded the requested cap and enumeration
    /// stopped early. Also set if ports were dropped for a truncated set.
    pub truncated: bool,
}

/// Failure modes for [`ProcessInspector::terminate`].
///
/// Stable variants so callers/agents do not match on error text.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TerminateError {
    NotFound,
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
        linux_snapshot(roots, cap)
    }

    /// Request termination of `pid` with `SIGTERM`.
    ///
    /// Default delegates to the Linux free function below. `&mut self` is part
    /// of the contract even though this implementation is stateless, leaving
    /// room for adapters that track outstanding requests.
    fn terminate(&mut self, pid: u32) -> Result<(), TerminateError> {
        linux_terminate(pid)
    }
}

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
#[derive(Debug, Clone, Copy, Default)]
pub struct LinuxProcessInspector;

impl LinuxProcessInspector {
    fn all_processes() -> Vec<ProcessInfo> {
        let mut processes = Vec::new();
        let Ok(entries) = std::fs::read_dir("/proc") else {
            return processes;
        };
        for entry in entries.flatten() {
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
        let mut pids = HashSet::from([pid]);
        for child in self.descendants(pid) {
            pids.insert(child.pid);
        }
        ports_for_pids(&pids)
    }

    fn snapshot(&self, roots: &[u32], cap: usize) -> ProcessSnapshot {
        let processes = Self::all_processes();
        snapshot_from(roots, cap, &processes, listen_inodes)
    }

    fn terminate(&mut self, pid: u32) -> Result<(), TerminateError> {
        linux_terminate(pid)
    }
}

/// Descendants of `pid` from an already-read process table (deduped, cycle-safe).
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
fn snapshot_from(
    roots: &[u32],
    cap: usize,
    processes: &[ProcessInfo],
    listen: impl FnOnce() -> HashMap<u64, u16>,
) -> ProcessSnapshot {
    if processes.is_empty() {
        return ProcessSnapshot::default();
    }
    let mut truncated = false;

    let mut owned: HashSet<u32> = HashSet::new();
    for &root in roots {
        if !owned.insert(root) {
            continue;
        }
        for child in collect_descendants(root, processes) {
            if !owned.insert(child.pid) {
                continue;
            }
            // Cap is enforced before unbounded growth: stop scanning the
            // moment the owned set reaches the limit.
            if owned.len() >= cap {
                truncated = true;
                break;
            }
        }
        if truncated {
            break;
        }
    }
    if cap == 0 {
        truncated = !roots.is_empty();
    }

    let mut result: Vec<ProcessInfo> = processes
        .iter()
        .filter(|process| owned.contains(&process.pid))
        .cloned()
        .collect();
    if result.len() > cap {
        result.truncate(cap);
        truncated = true;
    }
    result.sort_by_key(|process| process.pid);

    let ports = ports_for_pids_with(&owned, listen);
    ProcessSnapshot {
        processes: result,
        ports,
        truncated,
    }
}

/// Listening ports for an explicit pid set, reading the TCP tables once.
fn ports_for_pids(pids: &HashSet<u32>) -> Vec<ListeningPort> {
    ports_for_pids_with(pids, listen_inodes)
}

fn ports_for_pids_with(
    pids: &HashSet<u32>,
    listen: impl FnOnce() -> HashMap<u64, u16>,
) -> Vec<ListeningPort> {
    let listening = listen();
    let mut ports = Vec::new();
    for &pid in pids {
        let Ok(entries) = std::fs::read_dir(format!("/proc/{pid}/fd")) else {
            continue;
        };
        for entry in entries.flatten() {
            let Ok(link) = std::fs::read_link(entry.path()) else {
                continue;
            };
            let text = link.to_string_lossy();
            let Some(inode) = text
                .strip_prefix("socket:[")
                .and_then(|rest| rest.strip_suffix(']'))
                .and_then(|digits| digits.parse::<u64>().ok())
            else {
                continue;
            };
            if let Some(port) = listening.get(&inode) {
                ports.push(ListeningPort { port: *port, pid });
            }
        }
    }
    ports.sort_by_key(|port| (port.pid, port.port));
    ports.dedup();
    ports
}

/// Parse `(comm)`, ppid, and memory from `/proc/<pid>/stat` + `statm`. The
/// comm field may contain spaces and parentheses, so the split anchors on the
/// last `)`. CPU is left `None`: one read has no delta.
fn read_stat(pid: u32) -> Option<ProcessInfo> {
    let text = std::fs::read_to_string(format!("/proc/{pid}/stat")).ok()?;
    let mut info = parse_stat(&text, pid)?;
    info.memory_bytes = read_statm(pid);
    Some(info)
}

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
fn read_statm(pid: u32) -> Option<u64> {
    let text = std::fs::read_to_string(format!("/proc/{pid}/statm")).ok()?;
    let resident = parse_statm_resident(&text)?;
    let page_size = page_size()?;
    resident.checked_mul(page_size)
}

/// Parse the resident field (second, 0-indexed first = size) of `statm`.
fn parse_statm_resident(text: &str) -> Option<u64> {
    text.split_whitespace().nth(1)?.parse::<u64>().ok()
}

/// Page size in bytes via `sysconf(_SC_PAGESIZE)`, falling back to 4096.
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
    // SAFETY: as above; `_SC_CLK_TCK` is a valid, side-effect-free name.
    let value = unsafe { libc::sysconf(libc::_SC_CLK_TCK) };
    if value > 0 { value as f64 } else { 100.0 }
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
        let now = Instant::now();
        let hertz = clock_ticks_per_second();
        let mut seen: HashSet<u32> = HashSet::with_capacity(infos.len());
        for info in infos.iter_mut() {
            seen.insert(info.pid);
            let Some((start_time, utime, stime)) = read_stat_times(info.pid) else {
                info.cpu_percent = None;
                continue;
            };
            let jiffies = utime.saturating_add(stime);
            info.cpu_percent = match self.previous.get(&info.pid) {
                Some(&(prev_start, prev_jiffies, prev_at)) if prev_start == start_time => {
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
    let text = std::fs::read_to_string(format!("/proc/{pid}/stat")).ok()?;
    parse_stat_times(&text)
}

/// Best-effort `SIGTERM` via `libc::kill`, the direct, allocation-free call
/// already available through the crate's `libc` dependency (no shelling out to
/// `kill(1)`, whose stderr would have to be parsed for `EPERM`/`ESRCH`).
///
/// Mapping is by `errno`: `ESRCH` → [`TerminateError::NotFound`], `EPERM` →
/// [`TerminateError::PermissionDenied`], anything else → [`TerminateError::Other`].
fn linux_terminate(pid: u32) -> Result<(), TerminateError> {
    if pid == 0 {
        return Err(TerminateError::NotFound);
    }
    // SAFETY: `kill` is async-signal-safe and only inspects its arguments; we
    // pass a valid signal number and a pid the caller provided.
    let rc = unsafe { libc::kill(pid as libc::pid_t, libc::SIGTERM) };
    if rc == 0 {
        return Ok(());
    }
    match std::io::Error::last_os_error().raw_os_error() {
        Some(code) if code == libc::ESRCH => Err(TerminateError::NotFound),
        Some(code) if code == libc::EPERM => Err(TerminateError::PermissionDenied),
        _ => Err(TerminateError::Other(
            std::io::Error::last_os_error().kind(),
        )),
    }
}

/// Linux snapshot used by the trait default.
fn linux_snapshot(roots: &[u32], cap: usize) -> ProcessSnapshot {
    let processes = LinuxProcessInspector::all_processes();
    snapshot_from(roots, cap, &processes, listen_inodes)
}

/// Inode → port for TCP sockets in LISTEN state (0A), v4 and v6 tables.
fn listen_inodes() -> HashMap<u64, u16> {
    let mut inodes = HashMap::new();
    for table in ["/proc/net/tcp", "/proc/net/tcp6"] {
        let Ok(text) = std::fs::read_to_string(table) else {
            continue;
        };
        for line in text.lines().skip(1) {
            if let Some((inode, port)) = parse_listen_line(line) {
                inodes.insert(inode, port);
            }
        }
    }
    inodes
}

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

#[cfg(test)]
mod tests {
    use super::*;

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
        assert!(!full.truncated);
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
