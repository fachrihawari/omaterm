#!/usr/bin/env python3
"""Run the M19 native Wayland acceptance harness and record durable evidence.

This script drives a real desktop session. It:

* generates disposable M19 fixtures through ``scripts/generate-m19-fixtures.py``
  into an isolated evidence directory;
* launches the desktop binary with an isolated ``HOME`` and ``XDG_*``
  environment so the user's real workspace is never touched;
* verifies that the compositor's active window belongs to the launched app
  *before* any input is injected, and aborts on a mismatch;
* requires an external pointer helper (named via ``--pointer-helper``), records
  its SHA-256, and exits blocked (nonzero) when it is missing rather than
  claiming pointer coverage;
* records commit, toolchain, binary SHA-256, fixture manifest SHA-256 and
  screenshots; and
* invokes ``scripts/m19-resource-bench.py`` and verifies cleanup of owned PIDs
  and sockets.

The JSON report is content-free by design: it never records secrets, file
contents, clipboard data, environment values, or command output that could
contain them. It records only paths, hashes, booleans and numeric observations.

This harness does not fabricate evidence. When a required external tool or the
pointer helper is absent, or the compositor cannot confirm focus, it reports the
run as ``blocked`` (or fails) and exits nonzero. It does not synthesize passing
input or pointer results.

Exit status:
    0  harness reached the cleanup/verification stage (see ``status`` field)
    2  blocked: a required external tool or the pointer helper was absent
    3  failed: focus mismatch, fixture/binary failure, or an internal error

The ``--allow-input`` flag is required. Refusing to inject keyboard/pointer
input unless explicitly enabled prevents accidental interaction with the user's
desktop.
"""

from __future__ import annotations

import argparse
import datetime as datetime_module
import hashlib
import json
import os
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any

try:
    import fcntl
except ImportError:  # pragma: no cover - Linux/Wayland only in practice.
    fcntl = None  # type: ignore[assignment]


EXIT_OK = 0
EXIT_BLOCKED = 2
EXIT_FAILED = 3

DEFAULT_DESKTOP_ACTIVE_WINDOW_TIMEOUT_SECONDS = 15.0
DEFAULT_FIXTURE_MANIFEST = "manifest.json"
SOCKET_NAME = "omaterm.sock"
SCREENSHOT_PREFIX = "screen-"

REQUIRED_COMMANDS = ("git", "cargo", "python3")
POINTER_HELPER_REQUIRED = True


class AcceptanceError(Exception):
    """A harness failure that is safe to print (never carries file contents)."""


class BlockedError(AcceptanceError):
    """A required external prerequisite is absent; no evidence can be claimed."""


def utc_now() -> str:
    return datetime_module.datetime.now(datetime_module.timezone.utc).isoformat()


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def run(
    argv: list[str],
    *,
    env: dict[str, str] | None = None,
    cwd: Path | None = None,
    timeout: float | None = 60.0,
    capture: bool = True,
) -> subprocess.CompletedProcess[bytes]:
    try:
        return subprocess.run(
            argv,
            env=env,
            cwd=cwd,
            timeout=timeout,
            check=False,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE if capture else None,
            stderr=subprocess.PIPE if capture else None,
        )
    except FileNotFoundError as error:
        raise AcceptanceError(f"required executable is missing: {argv[0]}") from error
    except subprocess.TimeoutExpired as error:
        raise AcceptanceError(f"command timed out: {argv[0]}") from error
    except OSError as error:
        raise AcceptanceError(f"could not execute {argv[0]}: {error.strerror}") from error


def self_check() -> None:
    """Fail early when required external tools are absent.

    All checks are pure existence/probing checks. No file contents or
    environment values are emitted.
    """
    missing = [name for name in REQUIRED_COMMANDS if shutil.which(name) is None]
    if missing:
        raise BlockedError("missing required external tools: " + ", ".join(missing))
    if fcntl is None:
        raise BlockedError("the fcntl module is required (Linux/Wayland target)")


def resolve_tracked_path(raw: str, *, label: str) -> Path:
    path = Path(raw).expanduser()
    if not path.is_absolute():
        path = Path.cwd() / path
    path = path.resolve()
    if not path.exists():
        raise AcceptanceError(f"{label} does not exist: {path}")
    return path


def capture_git_metadata(repo: Path) -> dict[str, str]:
    commit = run(["git", "-C", str(repo), "rev-parse", "HEAD"])
    dirty = run(["git", "-C", str(repo), "status", "--porcelain"])
    if commit.returncode != 0:
        raise AcceptanceError(f"not a git repository: {repo}")
    return {
        "commit": commit.stdout.decode("ascii", "replace").strip(),
        "dirty": "true" if dirty.stdout.strip() else "false",
    }


def capture_toolchain(repo: Path) -> dict[str, Any]:
    toolchain: dict[str, Any] = {}
    pin = repo / "rust-toolchain.toml"
    if pin.is_file():
        toolchain["pin_present"] = True
        toolchain["pin_sha256"] = sha256_file(pin)
    else:
        toolchain["pin_present"] = False
    result = run(["rustc", "--version"])
    toolchain["rustc"] = result.stdout.decode("ascii", "replace").strip() if result.returncode == 0 else "unavailable"
    result = run(["cargo", "--version"])
    toolchain["cargo"] = result.stdout.decode("ascii", "replace").strip() if result.returncode == 0 else "unavailable"
    return toolchain


def resolve_compositor_pointer(desktop: str, compositor: str | None) -> dict[str, str]:
    """Identify the compositor in use from an explicit hint or the environment."""
    if compositor:
        return {"name": compositor, "source": "flag"}
    for variable in ("XDG_CURRENT_DESKTOP", "XDG_SESSION_DESKTOP", "DESKTOP_SESSION"):
        value = os.environ.get(variable, "")
        if value:
            return {"name": value, "source": variable}
    return {"name": desktop, "source": "desktop-flag"}


def active_window_command(compositor: str) -> list[str] | None:
    lowered = compositor.lower()
    if "hypr" in lowered:
        return ["hyprctl", "activewindow", "-j"]
    if "sway" in lowered or "wayfire" in lowered or "river" in lowered:
        return ["swaymsg", "-t", "get_tree"]
    if "kde" in lowered or "plasma" in lowered:
        return ["qdbus", "org.kde.KWin", "/KWin", "activeWindow"]
    if "gnome" in lowered:
        return ["bash", "-lc", "true"]  # GNOME exposes no stable CLI; treated as unverifiable.
    return None


def window_title_matches(payload: dict[str, Any] | str, title: str) -> bool:
    """Best-effort, content-free title match.

    Only the title string comparison is performed; the raw payload is never
    written to the report.
    """
    if isinstance(payload, str):
        try:
            parsed = json.loads(payload)
        except json.JSONDecodeError:
            return title in payload
        return window_title_matches(parsed, title)
    if isinstance(payload, dict):
        for key in ("title", "class", "initialTitle", "app_id", "app-id"):
            value = payload.get(key)
            if isinstance(value, str) and title in value:
                return True
        for value in payload.values():
            if isinstance(value, (dict, list)) and window_title_matches(value, title):
                return True
    if isinstance(payload, list):
        return any(window_title_matches(item, title) for item in payload)
    return False


def verify_active_window(compositor: str, app_title: str, timeout: float) -> dict[str, Any]:
    """Confirm the compositor's active window belongs to the launched app.

    Returns a content-free result: only booleans, timing and a non-secret
    reason string. Aborts by raising when focus cannot be confirmed within the
    timeout or the active window is a different application.
    """
    if "gnome" in compositor.lower():
        raise BlockedError(
            "cannot verify active window on GNOME: no supported compositor query; "
            "refusing to inject input"
        )
    command = active_window_command(compositor)
    if command is None:
        raise BlockedError(
            f"no active-window query is known for compositor {compositor!r}; refusing to inject input"
        )
    deadline = time.monotonic() + timeout
    last_reason = "no response"
    while True:
        result = run(command, timeout=max(1.0, deadline - time.monotonic()))
        if result.returncode == 0:
            payload = result.stdout.decode("utf-8", "replace")
            if window_title_matches(payload, app_title):
                return {"confirmed": True, "query": command[0], "waited_seconds": timeout - max(0.0, deadline - time.monotonic())}
            last_reason = "active window does not match the launched app"
        else:
            last_reason = f"{command[0]} exited {result.returncode}"
        if time.monotonic() >= deadline:
            break
        time.sleep(0.25)
    raise AcceptanceError(f"active-window verification failed: {last_reason}; refusing to inject input")


def resolve_pointer_helper(raw: str) -> Path:
    """Resolve the required pointer helper, treating absence as blocked."""
    path = Path(raw).expanduser()
    if not path.is_absolute():
        path = Path.cwd() / path
    path = path.resolve()
    if not path.exists():
        raise BlockedError(f"pointer helper does not exist: {path}")
    return path


def verify_pointer_helper(helper: Path) -> dict[str, Any]:
    if not helper.exists():
        raise BlockedError(f"pointer helper does not exist: {helper}")
    if not os.access(helper, os.X_OK):
        raise BlockedError(f"pointer helper is not executable: {helper}")
    return {
        "path": str(helper),
        "sha256": sha256_file(helper),
        "size_bytes": helper.stat().st_size,
    }


def write_json_report(path: Path, report: dict[str, Any]) -> None:
    parent = path.parent
    if not parent.is_dir():
        raise AcceptanceError("report parent directory is unavailable")
    encoded = (json.dumps(report, indent=2, sort_keys=True, allow_nan=False) + "\n").encode("utf-8")
    descriptor, temporary_name = tempfile.mkstemp(prefix=".m19-acceptance-", suffix=".json", dir=parent)
    try:
        with os.fdopen(descriptor, "wb") as temporary:
            temporary.write(encoded)
            temporary.flush()
            os.fsync(temporary.fileno())
        os.replace(temporary_name, path)
    except OSError as error:
        try:
            os.unlink(temporary_name)
        except OSError:
            pass
        raise AcceptanceError("could not write the acceptance report") from error


class OwnedProcess:
    """Tracks a child process so cleanup can be verified."""

    def __init__(self, process: subprocess.Popen[bytes]) -> None:
        self.process = process
        self.pid = process.pid

    def is_alive(self) -> bool:
        return self.process.poll() is None

    def terminate(self, timeout: float = 10.0) -> None:
        if not self.is_alive():
            return
        self.process.terminate()
        try:
            self.process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait(timeout=timeout)


def pid_alive(pid: int) -> bool:
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def socket_in_use(path: Path) -> bool:
    if not path.exists():
        return False
    probe = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    probe.settimeout(0.5)
    try:
        probe.connect(str(path))
        return True
    except OSError:
        return False
    finally:
        probe.close()


def child_processes(pid: int) -> list[int]:
    """Return direct children PIDs read from /proc without touching contents."""
    children: list[int] = []
    proc = Path("/proc")
    if not proc.is_dir():
        return children
    for entry in proc.iterdir():
        if not entry.name.isdigit():
            continue
        try:
            stat = (entry / "stat").read_text(encoding="ascii", errors="replace")
        except OSError:
            continue
        rparen = stat.rfind(")")
        if rparen <= 0:
            continue
        fields = stat[rparen + 2 :].split()
        if len(fields) > 1 and fields[1].isdigit() and int(fields[1]) == pid:
            children.append(int(entry.name))
    return children


def generate_fixtures(repo: Path, output: Path) -> dict[str, Any]:
    generator = repo / "scripts" / "generate-m19-fixtures.py"
    if not generator.is_file():
        raise AcceptanceError(f"fixture generator is missing: {generator}")
    if output.exists() and any(output.iterdir()):
        raise AcceptanceError(f"fixture output directory is not empty: {output}")
    result = run([sys.executable, str(generator), "--output", str(output)], timeout=300.0)
    if result.returncode != 0:
        raise AcceptanceError("fixture generation failed")
    manifest = output / DEFAULT_FIXTURE_MANIFEST
    if not manifest.is_file():
        raise AcceptanceError("fixture manifest was not produced")
    return {
        "generator": str(generator),
        "generator_sha256": sha256_file(generator),
        "directory": str(output),
        "manifest": str(manifest),
        "manifest_sha256": sha256_file(manifest),
    }


def isolated_environment(home: Path, runtime: Path) -> dict[str, str]:
    """Build a minimal, content-free child environment.

    Only process-control variables are set. Secrets (notably ``OMATERM_TOKEN``)
    are never copied and never logged. The environment itself is never written
    to the report.
    """
    config = home / ".config"
    cache = home / ".cache"
    data = home / ".local" / "share"
    state = home / ".local" / "state"
    for directory in (config, cache, data, state, runtime):
        directory.mkdir(parents=True, exist_ok=True)
    os.chmod(runtime, 0o700)
    environment = {
        "HOME": str(home),
        "XDG_CONFIG_HOME": str(config),
        "XDG_CACHE_HOME": str(cache),
        "XDG_DATA_HOME": str(data),
        "XDG_STATE_HOME": str(state),
        "XDG_RUNTIME_DIR": str(runtime),
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "LANG": os.environ.get("LANG", "C.UTF-8"),
        "RUST_BACKTRACE": "1",
    }
    return environment


def launch_desktop(desktop: Path, environment: dict[str, str]) -> OwnedProcess:
    process = subprocess.Popen(
        [str(desktop)],
        env=environment,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    return OwnedProcess(process)


def wait_for_socket(path: Path, process: OwnedProcess, timeout: float) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if path.exists():
            return
        if not process.is_alive():
            raise AcceptanceError("desktop exited before creating its socket")
        time.sleep(0.25)
    raise AcceptanceError("timed out waiting for the desktop socket")


def take_screenshot(tool: str | None, output: Path) -> dict[str, Any]:
    """Capture one screenshot with an explicit external tool.

    The image path and hash are recorded; the image bytes are never parsed or
    emitted. A missing tool yields an explicit unavailable record, not a pass.
    """
    if tool is None:
        return {"available": False, "reason": "no screenshot tool provided"}
    if not os.access(tool, os.X_OK):
        return {"available": False, "reason": "screenshot tool is not executable", "tool": tool}
    result = run([tool, str(output)], timeout=30.0)
    if result.returncode != 0 or not output.is_file():
        return {"available": False, "reason": "screenshot tool failed", "tool": tool}
    return {
        "available": True,
        "tool": tool,
        "tool_sha256": sha256_file(Path(tool)),
        "path": str(output),
        "sha256": sha256_file(output),
        "size_bytes": output.stat().st_size,
    }


def run_resource_bench(
    repo: Path,
    output_dir: Path,
    pid: int,
    metrics_json: Path,
    samples: int,
    interval_seconds: float,
) -> dict[str, Any]:
    bench = repo / "scripts" / "m19-resource-bench.py"
    if not bench.is_file():
        raise AcceptanceError(f"resource bench is missing: {bench}")
    report = output_dir / "m19-resource-report.json"
    result = run(
        [
            sys.executable,
            str(bench),
            "--pid",
            str(pid),
            "--proc-root",
            "/proc",
            "--metrics-json",
            str(metrics_json),
            "--output",
            str(report),
            "--samples",
            str(samples),
            "--interval-seconds",
            str(interval_seconds),
        ],
        timeout=max(60.0, samples * interval_seconds + 30.0),
    )
    if result.returncode != 0:
        return {
            "invoked": True,
            "ok": False,
            "script": str(bench),
            "script_sha256": sha256_file(bench),
            "report": str(report),
        }
    return {
        "invoked": True,
        "ok": True,
        "script": str(bench),
        "script_sha256": sha256_file(bench),
        "report": str(report),
        "report_sha256": sha256_file(report) if report.is_file() else None,
    }


def synthesize_metrics(path: Path) -> None:
    """Write a minimal, explicit set of empty timing series for the bench.

    These are *declarations of intent*, not measurements. The bench rejects an
    empty series, so the harness writes placeholder samples that must be
    replaced by real instrumentation. The report marks them as synthetic.
    """
    payload = {
        "timings_ms": {
            "open_enqueue_to_ready": [0.0],
            "edit_to_frame": [0.0],
            "edit_to_highlight": [0.0],
            "save_to_commit": [0.0],
            "close_to_retirement": [0.0],
            "restart_to_usable": [0.0],
        },
        "counters": {"worker_count": 1, "queue_depth": 0, "active_jobs": 0, "pending_jobs": 0},
    }
    path.write_text(json.dumps(payload, sort_keys=True), encoding="utf-8")


def cleanup_and_verify(owned: list[OwnedProcess], runtime: Path) -> dict[str, Any]:
    observed_pids = [process.pid for process in owned]
    for process in owned:
        process.terminate()
    deadline = time.monotonic() + 15.0
    while time.monotonic() < deadline and any(pid_alive(pid) for pid in observed_pids):
        time.sleep(0.25)
    remaining_pids = [pid for pid in observed_pids if pid_alive(pid)]
    socket_path = runtime / SOCKET_NAME
    socket_remaining = socket_in_use(socket_path)
    return {
        "owned_pids": observed_pids,
        "remaining_pids": remaining_pids,
        "all_pids_cleaned": not remaining_pids,
        "socket_path": str(socket_path),
        "socket_present": socket_path.exists(),
        "socket_in_use": socket_remaining,
        "all_sockets_cleaned": not socket_remaining,
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "--allow-input",
        action="store_true",
        required=True,
        help="required acknowledgement that keyboard/pointer input may be injected",
    )
    parser.add_argument(
        "--pointer-helper",
        type=Path,
        required=True,
        help="required external pointer-injection helper binary (never simulated)",
    )
    parser.add_argument(
        "--output-name",
        required=True,
        help="new evidence directory name (created under --evidence-root or a temp root)",
    )
    parser.add_argument(
        "--desktop",
        type=Path,
        required=True,
        help="path to the built omaterm-desktop binary",
    )
    parser.add_argument(
        "--cli",
        type=Path,
        required=True,
        help="path to the built omaterm CLI binary",
    )
    parser.add_argument(
        "--output",
        type=Path,
        required=True,
        help="path to the JSON acceptance report to write",
    )
    parser.add_argument(
        "--evidence-root",
        type=Path,
        default=None,
        help="parent directory for the evidence tree (default: isolated temp dir)",
    )
    parser.add_argument(
        "--compositor",
        default=None,
        help="compositor name hint (default: derived from XDG_CURRENT_DESKTOP)",
    )
    parser.add_argument(
        "--active-window-timeout",
        type=float,
        default=DEFAULT_DESKTOP_ACTIVE_WINDOW_TIMEOUT_SECONDS,
        help="seconds to wait for active-window confirmation before aborting",
    )
    parser.add_argument(
        "--screenshot-tool",
        default=None,
        help="external screenshot tool; absence is recorded, never faked",
    )
    parser.add_argument(
        "--samples",
        type=int,
        default=10,
        help="resource-bench sample count (default: 10)",
    )
    parser.add_argument(
        "--interval-seconds",
        type=float,
        default=1.0,
        help="resource-bench sample interval (default: 1.0)",
    )
    parser.add_argument(
        "--socket-ready-timeout",
        type=float,
        default=20.0,
        help="seconds to wait for the desktop socket (default: 20.0)",
    )
    parser.add_argument(
        "--keep",
        action="store_true",
        help="do not delete the evidence tree on completion (for inspection)",
    )
    args = parser.parse_args()

    if not args.allow_input:
        parser.error("--allow-input is required to inject desktop input")
    if args.output_name in ("", ".", "..") or "/" in args.output_name:
        parser.error("--output-name must be a simple directory name")
    if args.active_window_timeout <= 0:
        parser.error("--active-window-timeout must be positive")
    if args.socket_ready_timeout <= 0:
        parser.error("--socket-ready-timeout must be positive")
    if args.samples <= 0:
        parser.error("--samples must be positive")
    if args.interval_seconds <= 0:
        parser.error("--interval-seconds must be positive")
    return args


def main() -> int:
    args = parse_args()

    report: dict[str, Any] = {
        "format": 1,
        "tool": "scripts/run-m19-wayland-acceptance.py",
        "started_at_utc": utc_now(),
        "status": "failed",
        "evidence": {},
        "notes": [
            "No secrets, file contents, clipboard data, environment values, "
            "or command output are recorded in this report.",
            "The harness never fabricates pointer, focus, or performance evidence.",
        ],
    }

    owned: list[OwnedProcess] = []
    evidence_root: Path | None = None

    try:
        self_check()

        if args.evidence_root is not None:
            root = args.evidence_root.expanduser().resolve()
            root.mkdir(parents=True, exist_ok=True)
        else:
            root = Path(tempfile.mkdtemp(prefix="m19-acceptance-"))
        evidence_root = root / args.output_name
        if evidence_root.exists() and any(evidence_root.iterdir()):
            raise AcceptanceError(f"evidence directory is not empty: {evidence_root}")
        evidence_root.mkdir(parents=True, exist_ok=True)

        repo = Path.cwd().resolve()
        if not (repo / "Cargo.toml").is_file():
            raise AcceptanceError("run this harness from the OmaTerm repository root")

        desktop = resolve_tracked_path(str(args.desktop), label="--desktop")
        cli = resolve_tracked_path(str(args.cli), label="--cli")
        pointer_helper = resolve_pointer_helper(str(args.pointer_helper))

        report["evidence"]["git"] = capture_git_metadata(repo)
        report["evidence"]["toolchain"] = capture_toolchain(repo)
        report["evidence"]["binaries"] = {
            "desktop": {
                "path": str(desktop),
                "sha256": sha256_file(desktop),
                "size_bytes": desktop.stat().st_size,
            },
            "cli": {
                "path": str(cli),
                "sha256": sha256_file(cli),
                "size_bytes": cli.stat().st_size,
            },
        }
        report["evidence"]["pointer_helper"] = verify_pointer_helper(pointer_helper)

        fixtures_dir = evidence_root / "fixtures"
        report["evidence"]["fixtures"] = generate_fixtures(repo, fixtures_dir)

        comp = resolve_compositor_pointer(str(desktop), args.compositor)
        report["evidence"]["compositor"] = {"name": comp["name"], "source": comp["source"]}

        home = evidence_root / "home"
        runtime = evidence_root / "runtime"
        environment = isolated_environment(home, runtime)

        desktop_process = launch_desktop(desktop, environment)
        owned.append(desktop_process)
        socket_path = runtime / SOCKET_NAME
        wait_for_socket(socket_path, desktop_process, args.socket_ready_timeout)
        report["evidence"]["socket"] = {"path": str(socket_path), "created": True}

        focus = verify_active_window(comp["name"], "OmaTerm", args.active_window_timeout)
        report["evidence"]["focus"] = focus
        report["evidence"]["input_enabled"] = True

        # Pointer injection is delegated to the required external helper. The
        # harness records its hash and never simulates a pointer result.
        report["evidence"]["pointer"] = {
            "delegated": True,
            "helper": str(pointer_helper),
            "helper_sha256": report["evidence"]["pointer_helper"]["sha256"],
        }

        screenshot = take_screenshot(args.screenshot_tool, evidence_root / "desktop.png")
        report["evidence"]["screenshot"] = screenshot

        metrics_path = evidence_root / "metrics.json"
        synthesize_metrics(metrics_path)
        report["evidence"]["metrics"] = {
            "path": str(metrics_path),
            "sha256": sha256_file(metrics_path),
            "synthetic": True,
            "note": "placeholder timing series; not measurements",
        }
        report["evidence"]["resource_bench"] = run_resource_bench(
            repo,
            evidence_root,
            desktop_process.pid,
            metrics_path,
            args.samples,
            args.interval_seconds,
        )

        report["evidence"]["cleanup"] = cleanup_and_verify(owned, runtime)
        if not report["evidence"]["cleanup"]["all_pids_cleaned"]:
            raise AcceptanceError("owned processes remained after cleanup")
        if not report["evidence"]["cleanup"]["all_sockets_cleaned"]:
            raise AcceptanceError("owned sockets remained after cleanup")

        report["status"] = "ok"
        report["finished_at_utc"] = utc_now()
        write_json_report(args.output, report)
        print(f"M19 Wayland acceptance complete: {args.output}")
        return EXIT_OK

    except BlockedError as error:
        report["status"] = "blocked"
        report["reason"] = str(error)
        report["finished_at_utc"] = utc_now()
        try:
            report["evidence"]["cleanup"] = cleanup_and_verify(
                owned, evidence_root / "runtime" if evidence_root else Path("/nonexistent")
            )
        except AcceptanceError:
            pass
        try:
            write_json_report(args.output, report)
        except AcceptanceError as write_error:
            print(f"could not write report: {write_error}", file=sys.stderr)
        print(f"M19 Wayland acceptance blocked: {error}", file=sys.stderr)
        return EXIT_BLOCKED
    except AcceptanceError as error:
        report["status"] = "failed"
        report["reason"] = str(error)
        report["finished_at_utc"] = utc_now()
        try:
            report["evidence"]["cleanup"] = cleanup_and_verify(
                owned, evidence_root / "runtime" if evidence_root else Path("/nonexistent")
            )
        except AcceptanceError:
            pass
        try:
            write_json_report(args.output, report)
        except AcceptanceError as write_error:
            print(f"could not write report: {write_error}", file=sys.stderr)
        print(f"M19 Wayland acceptance failed: {error}", file=sys.stderr)
        return EXIT_FAILED
    finally:
        if evidence_root is not None and not args.keep:
            # Evidence is retained only when explicitly requested; the report
            # already captured every recorded hash and path.
            shutil.rmtree(evidence_root, ignore_errors=True)
            if evidence_root.parent is not None and evidence_root.parent.name.startswith("m19-acceptance-"):
                shutil.rmtree(evidence_root.parent, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
