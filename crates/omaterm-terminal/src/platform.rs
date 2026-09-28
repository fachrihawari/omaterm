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

use std::collections::{HashMap, HashSet};
use std::path::PathBuf;

/// One inspected process.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProcessInfo {
    pub pid: u32,
    pub ppid: u32,
    /// Command name from `/proc/<pid>/stat` (may be truncated by the kernel).
    pub name: String,
}

/// One listening TCP port attributed to an inspected process tree.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ListeningPort {
    pub port: u16,
    pub pid: u32,
}

/// Process inspection seam: descendants, CWD, and listening ports.
pub trait ProcessInspector {
    fn process_info(&self, pid: u32) -> Option<ProcessInfo>;
    fn descendants(&self, pid: u32) -> Vec<ProcessInfo>;
    fn cwd_of(&self, pid: u32) -> Option<PathBuf>;
    fn listening_ports(&self, pid: u32) -> Vec<ListeningPort>;
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
        let mut children: HashMap<u32, Vec<&ProcessInfo>> = HashMap::new();
        for process in &processes {
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

    fn cwd_of(&self, pid: u32) -> Option<PathBuf> {
        std::fs::read_link(format!("/proc/{pid}/cwd")).ok()
    }

    fn listening_ports(&self, pid: u32) -> Vec<ListeningPort> {
        let mut pids = HashSet::from([pid]);
        for child in self.descendants(pid) {
            pids.insert(child.pid);
        }
        let listening = listen_inodes();
        let mut ports = Vec::new();
        for pid in pids {
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
}

/// Parse `(comm)`, ppid from `/proc/<pid>/stat`. The comm field may contain
/// spaces and parentheses, so the split anchors on the last `)`.
fn read_stat(pid: u32) -> Option<ProcessInfo> {
    let text = std::fs::read_to_string(format!("/proc/{pid}/stat")).ok()?;
    parse_stat(&text, pid)
}

fn parse_stat(text: &str, pid: u32) -> Option<ProcessInfo> {
    let close = text.rfind(')')?;
    let (comm, rest) = text.split_at(close);
    let name = comm.split_once('(')?.1.to_owned();
    let mut fields = rest[1..].split_whitespace();
    fields.next()?; // state
    let ppid = fields.next()?.parse::<u32>().ok()?;
    Some(ProcessInfo { pid, ppid, name })
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
        assert!(parse_stat("garbage", 1).is_none());
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
}
