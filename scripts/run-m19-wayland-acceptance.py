#!/usr/bin/env python3
"""Run the M19 native Wayland acceptance harness and record durable evidence.

This script drives a real desktop session. It:

* generates disposable M19 fixtures through ``scripts/generate-m19-fixtures.py``
  into an isolated evidence directory;
* launches the desktop binary with an isolated ``HOME`` and ``XDG_*``
  environment so the user's real workspace is never touched;
* verifies that the compositor's active window belongs to the launched app
  *before* any input is injected, and aborts on a mismatch;
* optionally drives bounded semantic flows from ``--scripted-steps`` through
  a verified external helper (``--pointer-helper``), and exits blocked when
  flows cannot run rather than claiming input coverage;
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
    0  all required evidence passed (collection alone is never acceptance)
    2  blocked: a required external tool or the pointer helper was absent
    3  failed: focus mismatch, fixture/binary failure, or an internal error
    4  pending: partial evidence collected, required cases remain unexecuted

``--allow-input`` is required for scripted flows. Without flows the app can be
observed, but the run exits blocked because no workload was driven. Helpers must
implement the negotiated protocol documented by ``--helper-interface``. Example
steps: [{"flow":"small-file-cycles","cycles":20},
        {"flow":"cap-file-cycles","cycles":20},
        {"flow":"normal-exit","cycles":1}]. These are partial evidence only.
"""

from __future__ import annotations

import argparse
import datetime as datetime_module
import hashlib
import json
import math
import os
import shutil
import signal
import socket
import stat
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
EXIT_PENDING = 4

DEFAULT_DESKTOP_ACTIVE_WINDOW_TIMEOUT_SECONDS = 15.0
DEFAULT_FIXTURE_MANIFEST = "manifest.json"
SOCKET_NAME = "omaterm.sock"
SCREENSHOT_PREFIX = "screen-"

REQUIRED_COMMANDS = ("git", "cargo", "python3")
FLOWS = ("small-file-cycles", "cap-file-cycles", "editor-idle", "terminal-idle", "normal-exit")
CASE_REQUIREMENTS = (
    "E01: root/alias/race/cap reads and writes require S1 test evidence",
    "E02: async/pending/version-safe save requires tests and native pending-work flows",
    "E03: aggregate/history/queue caps and indexed rendering require tests and measured counters",
    "E04: schema migration, privacy and actual restart are not driven by this harness",
    "E05: all dirty/conflict/project/shutdown choices and failures require native evidence",
    "E06: IME/clipboard/grapheme/selection/focus and no-input-leak matrix requires native evidence",
    "E07: all entry routes, dedup and terminal parity require native activation and tests",
    "E08: all-language/fallback captures and stale/cancellation tests require evidence",
    "E09: cap/aggregate/idle baselines, budgets, all timing series and normal cleanup require evidence",
    "E10: final Rust gates, regression logs and redaction review were not executed",
)
HELPER_INTERFACE = """Optional external helper interface (version 1):
  helper --help          (read-only; advertises the protocol and both flags below)
  helper --m19-describe   (read-only; MUST NOT inject input)
  stdout JSON: {"format":1,"protocol":"omaterm-m19-helper",
                "pid_focus_guard":true,"flows":["small-file-cycles",...]}
  helper --m19-step       (request JSON on stdin)
  request: {"format":1,"flow":"small-file-cycles","cycles":20,
            "target_pid":123,"compositor":"hyprland","fixtures":"/isolated/fixtures"}
  stdout JSON: {"format":1,"flow":"small-file-cycles","target_pid":123,
                "status":"executed","focus_guarded":true,"assertions_passed":true}
The helper must check the compositor's exact focused PID immediately before EVERY
keyboard/pointer event and abort on focus loss. It must assert real flow outcomes,
not just command delivery, and must not leave detached input tasks. A cycle means
native open -> edit -> save -> close with on-disk verification; cap-file-cycles
uses cap-sized fixtures. Idle flows measure the named surface without editing.
normal-exit closes the window through the UI, never via process signals.
Only the flows in FLOWS are supported; restart/IME/conflict matrices are pending.
No generic input binary is assumed to implement this protocol. Unsupported tools
and non-executed replies are blocked; step nonzero exits and executed assertion
failures are failed. Raw helper output is never recorded.
"""

# Optional helper protocol, deliberately negotiated before input. --m19-describe
# must return {"format":1,"protocol":"omaterm-m19-helper",
# "pid_focus_guard":true,"flows":[...]} without injecting input. Each
# --m19-step receives a JSON request on stdin with format, flow, cycles,
# target_pid, compositor and fixtures directory. The helper MUST recheck exact
# focused PID before EVERY event and stop on mismatch. Successful replies are
# {"format":1,"flow":...,"target_pid":...,"status":"executed",
# "focus_guarded":true,"assertions_passed":true}. A reply is partial helper
# evidence, never a substitute for the full E01-E10 acceptance register.


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
    input_data: bytes | None = None,
) -> subprocess.CompletedProcess[bytes]:
    try:
        return subprocess.run(
            argv,
            env=env,
            cwd=cwd,
            timeout=timeout,
            check=False,
            stdin=subprocess.DEVNULL if input_data is None else None,
            input=input_data,
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
        for name in ("hyprland", "sway"):
            if name in value.lower():
                return {"name": name, "source": variable}
    return {"name": desktop, "source": "desktop-flag"}


def active_window_command(compositor: str) -> list[str] | None:
    lowered = compositor.lower()
    if "hypr" in lowered:
        return ["hyprctl", "activewindow", "-j"]
    if "sway" in lowered:
        return ["swaymsg", "-t", "get_tree"]
    return None


def focused_pid_matches(payload: Any, compositor: str, pid: int) -> bool:
    """Only compositor focus plus exact integer PID grants input ownership."""
    if not isinstance(payload, dict) or type(pid) is not int or pid <= 0:
        return False
    if "hypr" in compositor.lower():
        # activewindow is the focused window query, not a clients/title search.
        return type(payload.get("pid")) is int and payload["pid"] == pid
    if "sway" in compositor.lower():
        focused = []

        def visit(node: Any) -> None:
            if not isinstance(node, dict):
                return
            if node.get("focused") is True:
                focused.append(node)
            for key in ("nodes", "floating_nodes"):
                children = node.get(key, [])
                if isinstance(children, list):
                    for child in children:
                        visit(child)

        visit(payload)
        return len(focused) == 1 and type(focused[0].get("pid")) is int and focused[0]["pid"] == pid
    return False


def verify_active_window(compositor: str, pid: int, timeout: float) -> dict[str, Any]:
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
        result = run(command, env=helper_environment(), timeout=max(0.1, deadline - time.monotonic()))
        if result.returncode == 0:
            try:
                payload = json.loads(result.stdout)
            except (ValueError, UnicodeDecodeError):
                payload = None
            if focused_pid_matches(payload, compositor, pid):
                return {"confirmed": True, "pid": pid, "query": command[0], "waited_seconds": timeout - max(0.0, deadline - time.monotonic())}
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
    help_result = run_helper(helper, "--help", timeout=5.0)
    if (help_result.returncode != 0
            or any(marker not in help_result.stdout for marker in
                   (b"omaterm-m19-helper", b"--m19-describe", b"--m19-step"))):
        raise BlockedError("helper help does not document the supported M19 interface")
    result = run_helper(helper, "--m19-describe", timeout=5.0)
    try:
        description = json.loads(result.stdout)
    except (ValueError, UnicodeDecodeError):
        description = None
    if (result.returncode != 0 or not isinstance(description, dict)
            or description.get("format") != 1
            or description.get("protocol") != "omaterm-m19-helper"
            or description.get("pid_focus_guard") is not True
            or not isinstance(description.get("flows"), list)
            or any(flow not in FLOWS for flow in description["flows"])):
        raise BlockedError("helper does not advertise the supported PID-guarded M19 interface")
    return {
        "path": str(helper),
        "sha256": sha256_file(helper),
        "size_bytes": helper.stat().st_size,
        "interface_verified": True,
        "flows": description["flows"],
    }


def run_helper(helper: Path, flag: str, *, timeout: float,
               input_data: bytes | None = None) -> subprocess.CompletedProcess[bytes]:
    """Own a process group so timeout cleanup cannot leave input children running."""
    try:
        process = subprocess.Popen([str(helper), flag], env=helper_environment(),
                                   stdin=subprocess.PIPE if input_data is not None else subprocess.DEVNULL,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    except OSError as error:
        raise BlockedError("could not execute the external helper") from error
    timed_out = False
    try:
        stdout, stderr = process.communicate(input=input_data, timeout=timeout)
    except subprocess.TimeoutExpired:
        timed_out = True
        stdout, stderr = b"", b""
    finally:
        # This group is created by this invocation; it never belongs to the app
        # or compositor. Detached input tasks violate the helper protocol.
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait(timeout=5.0)
        for stream in (process.stdin, process.stdout, process.stderr):
            if stream is not None:
                stream.close()
    if timed_out:
        if flag == "--m19-step":
            raise AcceptanceError("external helper flow exceeded its bounded deadline")
        raise BlockedError("external helper flow exceeded its bounded deadline")
    return subprocess.CompletedProcess([str(helper), flag], process.returncode, stdout, stderr)


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
    environment.update(compositor_environment())
    return environment


def compositor_environment() -> dict[str, str]:
    """Allowlist connection handles, with relative Wayland sockets made absolute.

    XDG_RUNTIME_DIR belongs to the test for app IPC. The compositor socket still
    belongs to the desktop session. No tokens, DBus credentials or arbitrary
    OMATERM_* variables are inherited.
    """
    display = os.environ.get("WAYLAND_DISPLAY")
    if not display:
        raise BlockedError("WAYLAND_DISPLAY is unavailable; run in a native Wayland session")
    path = Path(display)
    if not path.is_absolute():
        original_runtime = os.environ.get("XDG_RUNTIME_DIR")
        if not original_runtime or not Path(original_runtime).is_absolute():
            raise BlockedError("relative Wayland display needs the original runtime directory")
        path = Path(original_runtime) / path
    try:
        info = path.stat()
    except OSError as error:
        raise BlockedError("Wayland connection socket is unavailable") from error
    if not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.getuid():
        raise BlockedError("Wayland connection must be a socket owned by the current user")
    environment = {"WAYLAND_DISPLAY": str(path)}
    for name in ("HYPRLAND_INSTANCE_SIGNATURE", "SWAYSOCK"):
        if os.environ.get(name):
            environment[name] = os.environ[name]
    return environment


def helper_environment() -> dict[str, str]:
    environment = {
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "LANG": "C.UTF-8",
    }
    environment.update(compositor_environment())
    runtime = os.environ.get("XDG_RUNTIME_DIR")
    if runtime and Path(runtime).is_absolute():
        environment["XDG_RUNTIME_DIR"] = runtime
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
    if not report.is_file():
        return {"invoked": True, "ok": False, "reason": "bench produced no report"}
    try:
        collected = json.loads(report.read_bytes())
        instrumentation = collected["instrumentation"]
        unobserved = instrumentation["unobserved_timings"]
    except (OSError, ValueError, KeyError, TypeError):
        return {"invoked": True, "ok": False, "reason": "bench report is invalid"}
    return {
        "invoked": True,
        "ok": True,
        "script": str(bench),
        "script_sha256": sha256_file(bench),
        "report": str(report),
        "report_sha256": sha256_file(report) if report.is_file() else None,
        "unobserved_timings": unobserved,
        "latency_pass": None,
        "acceptance_status": "pending",
    }


def load_steps(path: Path) -> list[dict[str, Any]]:
    """Fixed semantic flows only; never execute arbitrary commands or key text."""
    try:
        if path.stat().st_size > 16384:
            raise BlockedError("scripted steps exceed the size limit")
        steps = json.loads(path.read_bytes())
    except (OSError, ValueError) as error:
        raise BlockedError("scripted steps JSON is unavailable or invalid") from error
    if not isinstance(steps, list) or not 1 <= len(steps) <= 32:
        raise BlockedError("scripted steps must contain 1 to 32 bounded flows")
    for index, step in enumerate(steps):
        if (not isinstance(step, dict) or set(step) != {"flow", "cycles"}
                or step["flow"] not in FLOWS or type(step["cycles"]) is not int
                or not 1 <= step["cycles"] <= 20):
            raise BlockedError("scripted steps contain an unsupported flow or cycle count")
        if step["flow"] == "normal-exit" and (index != len(steps) - 1 or step["cycles"] != 1):
            raise BlockedError("normal-exit must be the final flow with one cycle")
    return steps


def execute_step(helper: Path, interface: dict[str, Any], step: dict[str, Any],
                 process: OwnedProcess, compositor: str, fixtures: Path,
                 timeout: float) -> dict[str, Any]:
    if step["flow"] not in interface["flows"]:
        raise BlockedError("helper does not implement the requested flow")
    if not process.is_alive():
        raise AcceptanceError("desktop exited before the scripted flow")
    verify_active_window(compositor, process.pid, 0.1)
    request = {"format": 1, **step, "target_pid": process.pid,
               "compositor": compositor, "fixtures": str(fixtures)}
    result = run_helper(helper, "--m19-step", timeout=timeout,
                        input_data=json.dumps(request).encode("utf-8"))
    if result.returncode != 0:
        raise AcceptanceError("external helper step exited nonzero")
    try:
        reply = json.loads(result.stdout)
    except (ValueError, UnicodeDecodeError):
        reply = None
    if (isinstance(reply, dict) and reply.get("status") == "executed"
            and reply.get("assertions_passed") is False):
        raise AcceptanceError("executed helper flow assertions failed")
    if (not isinstance(reply, dict)
            or reply.get("format") != 1 or reply.get("status") != "executed"
            or reply.get("flow") != step["flow"]
            or type(reply.get("target_pid")) is not int or reply["target_pid"] != process.pid
            or reply.get("focus_guarded") is not True
            or reply.get("assertions_passed") is not True):
        raise BlockedError("helper could not execute/assert the PID-guarded flow")
    if step["flow"] != "normal-exit":
        verify_active_window(compositor, process.pid, 0.1)
    return {**step, "status": "executed", "target_pid": process.pid,
            "focus_guarded": True, "helper_assertions_passed": True}


def acceptance_outcome(evidence: dict[str, Any], cases: dict[str, Any]) -> tuple[str, int]:
    cleanup = evidence.get("cleanup", {})
    if not cleanup.get("all_pids_cleaned") or not cleanup.get("all_sockets_cleaned"):
        return "failed", EXIT_FAILED
    if not evidence.get("workload", {}).get("driven", False):
        return "blocked", EXIT_BLOCKED
    if evidence.get("resource_bench", {}).get("ok") is not True:
        return "blocked", EXIT_BLOCKED
    if (evidence.get("resource_bench", {}).get("unobserved_timings")
            or evidence.get("normal_exit", {}).get("observed") is not True
            or evidence.get("normal_exit", {}).get("socket_removed") is not True
            or any(case.get("status") != "passed" for case in cases.values())
            or set(cases) != {f"E{index:02d}" for index in range(1, 11)}):
        return "pending", EXIT_PENDING
    return "ok", EXIT_OK


def self_test() -> None:
    """Isolated parser, PID identity, helper execution and status regression tests."""
    import contextlib
    import io
    from unittest.mock import patch

    assert parse_args(["--self-test"]).self_test
    base = ["--desktop", "/test/app", "--cli", "/test/cli", "--output", "/test/report",
            "--output-name", "fresh"]
    assert parse_args(base).pointer_helper is None
    for extra in (["--interval-seconds", "nan"], ["--step-timeout", "inf"],
                  ["--samples", "0"], ["--scripted-steps", "/test/steps"]):
        with contextlib.redirect_stderr(io.StringIO()):
            try:
                parse_args(base + extra)
            except SystemExit as error:
                assert error.code != 0
            else:
                raise AssertionError("invalid arguments accepted")

    assert focused_pid_matches({"pid": 42, "title": "other"}, "Hyprland", 42)
    for payload in ({"pid": 43, "title": "OmaTerm"}, {"pid": "42"}, {"pid": True}, {}, "OmaTerm"):
        assert not focused_pid_matches(payload, "Hyprland", 42)
    tree = {"nodes": [{"pid": 42, "focused": False, "name": "OmaTerm"}],
            "floating_nodes": [{"pid": 43, "focused": True}]}
    assert not focused_pid_matches(tree, "sway", 42)
    tree["floating_nodes"][0]["pid"] = 42
    assert focused_pid_matches(tree, "sway", 42)
    tree["nodes"][0]["focused"] = True
    assert not focused_pid_matches(tree, "sway", 42)
    assert active_window_command("river") is None

    cases = {f"E{index:02d}": {"status": "passed"} for index in range(1, 11)}
    evidence = {"workload": {"driven": True}, "resource_bench": {"ok": True},
                "cleanup": {"all_pids_cleaned": True, "all_sockets_cleaned": True},
                "normal_exit": {"observed": True, "socket_removed": True}}
    assert acceptance_outcome(evidence, cases) == ("ok", EXIT_OK)
    evidence["workload"]["driven"] = False
    assert acceptance_outcome(evidence, cases) == ("blocked", EXIT_BLOCKED)
    evidence["workload"]["driven"] = True
    evidence["resource_bench"]["ok"] = False
    assert acceptance_outcome(evidence, cases) == ("blocked", EXIT_BLOCKED)
    evidence["resource_bench"]["ok"] = True
    cases["E06"]["status"] = "pending"
    assert acceptance_outcome(evidence, cases) == ("pending", EXIT_PENDING)
    cases["E06"]["status"] = "passed"
    evidence["normal_exit"]["observed"] = False
    assert acceptance_outcome(evidence, cases) == ("pending", EXIT_PENDING)
    evidence["cleanup"]["all_pids_cleaned"] = False
    assert acceptance_outcome(evidence, cases) == ("failed", EXIT_FAILED)
    for blocked_field in ("workload", "resource_bench"):
        key = "driven" if blocked_field == "workload" else "ok"
        evidence[blocked_field][key] = False
        for cleanup_key in ("all_pids_cleaned", "all_sockets_cleaned"):
            evidence["cleanup"] = {"all_pids_cleaned": True, "all_sockets_cleaned": True}
            evidence["cleanup"][cleanup_key] = False
            assert acceptance_outcome(evidence, cases) == ("failed", EXIT_FAILED)
        evidence[blocked_field][key] = True
    evidence["cleanup"] = {"all_pids_cleaned": True, "all_sockets_cleaned": True}
    evidence["normal_exit"]["observed"] = True
    evidence["resource_bench"]["unobserved_timings"] = ["restart_to_usable"]
    assert acceptance_outcome(evidence, cases) == ("pending", EXIT_PENDING)

    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        # Exercise the actual blocked error handler, including report and exit
        # status, without launching the desktop or injecting any input.
        report_path = root / "blocked-report.json"
        blocked_args = parse_args(base)
        blocked_args.output = report_path
        for cleanup_result in (
                {"all_pids_cleaned": True, "all_sockets_cleaned": True},
                {"all_pids_cleaned": False, "all_sockets_cleaned": True},
                {"all_pids_cleaned": True, "all_sockets_cleaned": False},
                AcceptanceError("cleanup failed"), OSError("cleanup failed"),
                subprocess.TimeoutExpired("cleanup", 1)):
            cleanup_kwargs = ({"side_effect": cleanup_result} if isinstance(cleanup_result, Exception)
                              else {"return_value": cleanup_result})
            expected = ("blocked", EXIT_BLOCKED) if cleanup_result == {
                "all_pids_cleaned": True, "all_sockets_cleaned": True} else ("failed", EXIT_FAILED)
            with patch(__name__ + ".parse_args", return_value=blocked_args), \
                    patch(__name__ + ".self_check", side_effect=BlockedError("prerequisite absent")), \
                    patch(__name__ + ".cleanup_and_verify", **cleanup_kwargs), \
                    contextlib.redirect_stderr(io.StringIO()) as diagnostics:
                assert main() == expected[1]
            blocked_report = json.loads(report_path.read_bytes())
            assert blocked_report["status"] == expected[0]
            assert blocked_report["reason"] == "prerequisite absent"
            assert f"acceptance {expected[0]}:" in diagnostics.getvalue()
            assert all(case["status"] == "pending" for case in blocked_report["cases"].values())
            if expected[0] == "failed":
                assert "cleanup_reason" in blocked_report

        wayland_socket = socket.socket(socket.AF_UNIX)
        wayland_socket.bind(str(root / "wayland-test"))
        try:
            with patch.dict(os.environ, {"WAYLAND_DISPLAY": "wayland-test", "XDG_RUNTIME_DIR": str(root),
                                         "SWAYSOCK": str(root / "sway.sock"),
                                         "HYPRLAND_INSTANCE_SIGNATURE": "test-signature",
                                         "OMATERM_TOKEN": "never-inherit", "AWS_SECRET_ACCESS_KEY": "never-inherit"}, clear=True):
                environment = isolated_environment(root / "home", root / "runtime")
                assert environment["WAYLAND_DISPLAY"] == str(root / "wayland-test")
                assert environment["XDG_RUNTIME_DIR"] == str(root / "runtime")
                assert environment["SWAYSOCK"] == str(root / "sway.sock")
                assert environment["HYPRLAND_INSTANCE_SIGNATURE"] == "test-signature"
                assert "OMATERM_TOKEN" not in environment and "AWS_SECRET_ACCESS_KEY" not in environment
                assert "OMATERM_TOKEN" not in helper_environment()
        finally:
            wayland_socket.close()

        steps_path = root / "steps.json"
        steps_path.write_text(json.dumps([{"flow": "small-file-cycles", "cycles": 20},
                                          {"flow": "normal-exit", "cycles": 1}]))
        assert len(load_steps(steps_path)) == 2
        for invalid in ([], [{"flow": "small-file-cycles", "cycles": True}],
                        [{"flow": "normal-exit", "cycles": 2}],
                        [{"flow": "arbitrary-command", "cycles": 1}]):
            steps_path.write_text(json.dumps(invalid))
            try:
                load_steps(steps_path)
            except BlockedError:
                pass
            else:
                raise AssertionError("unbounded/unsupported flow accepted")

        # This test helper executes only the protocol, never desktop input.
        helper = root / "helper"
        helper.write_text(
            f"#!{sys.executable}\nimport json, sys\n"
            "if sys.argv[1] == '--help':\n"
            " print('omaterm-m19-helper --m19-describe --m19-step')\n"
            "elif sys.argv[1] == '--m19-describe':\n"
            " print(json.dumps({'format':1,'protocol':'omaterm-m19-helper','pid_focus_guard':True,'flows':['small-file-cycles']}))\n"
            "else:\n"
            " request=json.load(sys.stdin)\n"
            " print(json.dumps({'format':1,'status':'executed','flow':request['flow'],'target_pid':request['target_pid'],'focus_guarded':True,'assertions_passed':True}))\n")
        helper.chmod(0o700)
        process = OwnedProcess(subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"],
                                               stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                               stderr=subprocess.DEVNULL))
        try:
            with patch(__name__ + ".helper_environment", return_value={}), patch(__name__ + ".verify_active_window") as focus:
                interface = verify_pointer_helper(helper)
                unsupported = subprocess.CompletedProcess([str(helper), "--help"], 0, b"generic input tool", b"")
                with patch(__name__ + ".run_helper", return_value=unsupported) as probe:
                    try:
                        verify_pointer_helper(helper)
                    except BlockedError:
                        pass
                    else:
                        raise AssertionError("unknown helper interface accepted")
                    assert probe.call_count == 1 and probe.call_args.args[1] == "--help"
                executed = execute_step(helper, interface, {"flow": "small-file-cycles", "cycles": 1},
                                        process, "sway", root, 5.0)
                assert executed["status"] == "executed" and focus.call_count == 2
                for reply in ({"status": "pending"}, {"format": 1, "status": "executed", "flow": "small-file-cycles",
                              "target_pid": process.pid + 1, "focus_guarded": True, "assertions_passed": True}):
                    invalid = subprocess.CompletedProcess([str(helper), "--m19-step"], 0, json.dumps(reply).encode(), b"")
                    with patch(__name__ + ".run_helper", return_value=invalid):
                        try:
                            execute_step(helper, interface, {"flow": "small-file-cycles", "cycles": 1},
                                         process, "sway", root, 5.0)
                        except BlockedError:
                            pass
                        else:
                            raise AssertionError("unexecuted or wrong-PID flow accepted")
                failed_reply = {"format": 1, "status": "executed", "flow": "small-file-cycles",
                                "target_pid": process.pid, "focus_guarded": True, "assertions_passed": False}
                for code, payload in ((0, json.dumps(failed_reply).encode()),
                                      (1, json.dumps(failed_reply).encode()), (1, b"")):
                    failure = subprocess.CompletedProcess([str(helper), "--m19-step"], code, payload, b"")
                    with patch(__name__ + ".run_helper", return_value=failure):
                        try:
                            execute_step(helper, interface, {"flow": "small-file-cycles", "cycles": 1},
                                         process, "sway", root, 5.0)
                        except BlockedError as error:
                            raise AssertionError("executed helper failure classified as blocked") from error
                        except AcceptanceError:
                            pass
                        else:
                            raise AssertionError("executed helper failure accepted")
                focus.reset_mock()
                focus.side_effect = AcceptanceError("focus mismatch")
                with patch(__name__ + ".run_helper") as command:
                    try:
                        execute_step(helper, interface, {"flow": "small-file-cycles", "cycles": 1},
                                     process, "sway", root, 5.0)
                    except AcceptanceError:
                        pass
                    else:
                        raise AssertionError("input executed without focus")
                    command.assert_not_called()
            cleanup = cleanup_and_verify([process], root)
            assert cleanup["termination_cleanup_pids"] == [process.pid]
            assert cleanup["normal_exit_evidence"] is False and cleanup["all_pids_cleaned"]
        finally:
            process.terminate()

        sleeper = root / "slow-helper"
        sleeper.write_text(f"#!{sys.executable}\nimport time\ntime.sleep(30)\n")
        sleeper.chmod(0o700)
        started = time.monotonic()
        with patch(__name__ + ".helper_environment", return_value={}):
            try:
                run_helper(sleeper, "--m19-step", timeout=0.02)
            except BlockedError as error:
                raise AssertionError("executed helper timeout classified as blocked") from error
            except AcceptanceError:
                pass
            else:
                raise AssertionError("helper deadline was not enforced")
        assert time.monotonic() - started < 5.0

        metrics = root / "metrics.json"
        metrics.write_text(json.dumps({"timings_ms": {name: [] for name in (
            "open_enqueue_to_ready", "edit_to_frame", "edit_to_highlight", "save_to_commit",
            "close_to_retirement", "restart_to_usable")}, "counters": {}}))
        repo = Path(__file__).resolve().parent.parent
        bench = run_resource_bench(repo, root, os.getpid(), metrics, 2, 0.001)
        assert bench["ok"] and len(bench["unobserved_timings"]) == 6 and bench["latency_pass"] is None
        (root / "m19-resource-report.json").unlink()
        metrics.unlink()
        assert run_resource_bench(repo, root, os.getpid(), metrics, 2, 0.001)["ok"] is False


def cleanup_and_verify(owned: list[OwnedProcess], runtime: Path) -> dict[str, Any]:
    observed_pids = [process.pid for process in owned]
    forced_pids = [process.pid for process in owned if process.is_alive()]
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
        "pid_scope": "launched desktop PIDs only; shell/worker/sidecar acceptance remains pending",
        "termination_cleanup_pids": forced_pids,
        "normal_exit_evidence": False,
        "remaining_pids": remaining_pids,
        "all_pids_cleaned": not remaining_pids,
        "socket_path": str(socket_path),
        "socket_present": socket_path.exists(),
        "socket_in_use": socket_remaining,
        "all_sockets_cleaned": not socket_remaining and not socket_path.exists(),
    }


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "--allow-input",
        action="store_true",
        help="required acknowledgement when --scripted-steps is supplied",
    )
    parser.add_argument(
        "--pointer-helper",
        type=Path,
        help="external helper implementing the negotiated M19 protocol (see source)",
    )
    parser.add_argument(
        "--output-name",
        required=False,
        help="new evidence directory name (created under --evidence-root or a temp root)",
    )
    parser.add_argument(
        "--desktop",
        type=Path,
        required=False,
        help="path to the built omaterm-desktop binary",
    )
    parser.add_argument(
        "--cli",
        type=Path,
        required=False,
        help="path to the built omaterm CLI binary",
    )
    parser.add_argument(
        "--output",
        type=Path,
        required=False,
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
    parser.add_argument("--self-test", action="store_true", help="run isolated harness checks, no desktop input")
    parser.add_argument("--helper-interface", action="store_true", help="print the supported helper contract and exit")
    parser.add_argument("--scripted-steps", type=Path, help="optional bounded semantic flow JSON array")
    parser.add_argument("--step-timeout", type=float, default=120.0, help="per-flow deadline, at most 300 seconds")
    parser.add_argument("--normal-exit-timeout", type=float, default=15.0, help="normal close deadline")
    args = parser.parse_args(argv)

    if args.helper_interface:
        return args
    if args.self_test:
        if any(value is not None for value in (args.desktop, args.cli, args.output, args.output_name,
                                               args.pointer_helper, args.scripted_steps)):
            parser.error("--self-test does not accept launch paths or steps")
        return args
    if any(value is None for value in (args.desktop, args.cli, args.output, args.output_name)):
        parser.error("--desktop, --cli, --output, and --output-name are required")
    if args.scripted_steps and not args.allow_input:
        parser.error("--allow-input is required for --scripted-steps")
    if args.output_name in ("", ".", "..") or "/" in args.output_name:
        parser.error("--output-name must be a simple directory name")
    if args.samples <= 0:
        parser.error("--samples must be positive")
    for name in ("active_window_timeout", "socket_ready_timeout", "interval_seconds", "step_timeout", "normal_exit_timeout"):
        value = getattr(args, name)
        if not math.isfinite(value) or value <= 0:
            parser.error(f"--{name.replace('_', '-')} must be finite and positive")
    if args.step_timeout > 300:
        parser.error("--step-timeout must not exceed 300 seconds")
    return args


def main() -> int:
    args = parse_args()
    if args.helper_interface:
        print(HELPER_INTERFACE)
        return EXIT_OK
    if args.self_test:
        self_test()
        print("m19 Wayland acceptance self-test passed")
        return EXIT_OK

    report: dict[str, Any] = {
        "format": 1,
        "tool": "scripts/run-m19-wayland-acceptance.py",
        "started_at_utc": utc_now(),
        "status": "failed",
        "evidence": {
            "workload": {"driven": False, "steps": []},
            "resource_bench": {"invoked": False, "ok": False},
            "normal_exit": {"observed": False, "status": "pending", "reason": "normal window close not executed"},
        },
        "cases": {f"E{index:02d}": {"status": "pending", "reason": requirement, "artifacts": []}
                  for index, requirement in enumerate(CASE_REQUIREMENTS, 1)},
        "notes": [
            "No secrets, file contents, clipboard data, environment values, "
            "or command output are recorded in this report.",
            "The harness never fabricates pointer, focus, or performance evidence.",
        ],
    }

    owned: list[OwnedProcess] = []
    evidence_root: Path | None = None
    temporary_root: Path | None = None

    try:
        self_check()

        if args.evidence_root is not None:
            root = args.evidence_root.expanduser().resolve()
            root.mkdir(parents=True, exist_ok=True)
        else:
            root = Path(tempfile.mkdtemp(prefix="m19-acceptance-"))
            temporary_root = root
        candidate = root / args.output_name
        if candidate.exists() or candidate.is_symlink():
            raise AcceptanceError("evidence directory already exists; choose a new output name")
        candidate.mkdir(mode=0o700)
        evidence_root = candidate

        repo = Path.cwd().resolve()
        if not (repo / "Cargo.toml").is_file():
            raise AcceptanceError("run this harness from the OmaTerm repository root")

        desktop = resolve_tracked_path(str(args.desktop), label="--desktop")
        cli = resolve_tracked_path(str(args.cli), label="--cli")
        pointer_helper = None
        steps = load_steps(args.scripted_steps) if args.scripted_steps else []
        if steps:
            if args.pointer_helper is None:
                raise BlockedError("scripted flows require a verified external helper")
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
        if pointer_helper is not None:
            report["evidence"]["pointer_helper"] = verify_pointer_helper(pointer_helper)
        else:
            report["evidence"]["pointer_helper"] = {"interface_verified": False, "status": "pending", "reason": "no scripted flows requested"}

        fixtures_dir = evidence_root / "fixtures"
        report["evidence"]["fixtures"] = generate_fixtures(repo, fixtures_dir)

        comp = resolve_compositor_pointer(str(desktop), args.compositor)
        report["evidence"]["compositor"] = {"name": comp["name"], "source": comp["source"]}

        home = evidence_root / "home"
        runtime = evidence_root / "runtime"
        environment = isolated_environment(home, runtime)
        metrics_path = evidence_root / "metrics.json"
        environment["OMATERM_METRICS_PATH"] = str(metrics_path)
        report["evidence"]["metrics"] = {"path": str(metrics_path), "source": "app OMATERM_METRICS_PATH", "observed": False}

        desktop_process = launch_desktop(desktop, environment)
        owned.append(desktop_process)
        socket_path = runtime / SOCKET_NAME
        wait_for_socket(socket_path, desktop_process, args.socket_ready_timeout)
        report["evidence"]["socket"] = {"path": str(socket_path), "created": True}

        focus = verify_active_window(comp["name"], desktop_process.pid, args.active_window_timeout)
        report["evidence"]["focus"] = focus
        report["evidence"]["input_enabled"] = bool(steps and args.allow_input)
        for step in steps:
            if step["flow"] == "normal-exit":
                continue  # sample the still-live app before closing it normally
            assert pointer_helper is not None
            executed = execute_step(pointer_helper, report["evidence"]["pointer_helper"], step,
                                    desktop_process, comp["name"], fixtures_dir, args.step_timeout)
            report["evidence"]["workload"]["steps"].append(executed)
            if step["flow"] in ("small-file-cycles", "cap-file-cycles"):
                report["evidence"]["workload"]["driven"] = True

        screenshot = take_screenshot(args.screenshot_tool, evidence_root / "desktop.png")
        report["evidence"]["screenshot"] = screenshot

        report["evidence"]["resource_bench"] = run_resource_bench(
            repo,
            evidence_root,
            desktop_process.pid,
            metrics_path,
            args.samples,
            args.interval_seconds,
        )
        report["evidence"]["metrics"]["observed"] = metrics_path.is_file()
        if report["evidence"]["resource_bench"]["ok"] is not True:
            raise BlockedError("resource collection failed; actual app metrics may be missing or invalid")
        # Hash only the strict, numeric bench report, never unchecked app output.
        report["evidence"]["cases_partial_only"] = True
        report["cases"]["E09"]["artifacts"] = [report["evidence"]["resource_bench"]["report"]]
        if steps and steps[-1]["flow"] == "normal-exit":
            assert pointer_helper is not None
            executed = execute_step(pointer_helper, report["evidence"]["pointer_helper"], steps[-1],
                                    desktop_process, comp["name"], fixtures_dir, args.step_timeout)
            report["evidence"]["workload"]["steps"].append(executed)
            try:
                code = desktop_process.process.wait(timeout=args.normal_exit_timeout)
            except subprocess.TimeoutExpired as error:
                raise BlockedError("normal window close did not exit within the deadline") from error
            report["evidence"]["normal_exit"] = {
                "observed": code == 0, "exit_code": code,
                "status": "observed" if code == 0 else "failed", "termination_sent": False,
                "socket_removed": not socket_path.exists(),
            }
            if code != 0:
                raise AcceptanceError("desktop did not exit normally with code zero")

        report["evidence"]["cleanup"] = cleanup_and_verify(owned, runtime)
        if not report["evidence"]["cleanup"]["all_pids_cleaned"]:
            raise AcceptanceError("owned processes remained after cleanup")
        if not report["evidence"]["cleanup"]["all_sockets_cleaned"]:
            raise AcceptanceError("owned sockets remained after cleanup")

        report["status"], exit_code = acceptance_outcome(report["evidence"], report["cases"])
        if report["status"] == "blocked":
            report["reason"] = "no open/edit/save/close workload was driven"
        elif report["status"] == "pending":
            report["reason"] = "partial collection only; full E01-E10 evidence remains pending"
        report["finished_at_utc"] = utc_now()
        write_json_report(args.output, report)
        print(f"M19 Wayland acceptance {report['status']}: {args.output}")
        return exit_code

    except (AcceptanceError, OSError, ValueError) as error:
        report["status"] = "blocked" if isinstance(error, BlockedError) else "failed"
        report["reason"] = str(error) if isinstance(error, AcceptanceError) else "harness filesystem or data failure"
        report["finished_at_utc"] = utc_now()
        try:
            report["evidence"]["cleanup"] = cleanup_and_verify(
                owned, evidence_root / "runtime" if evidence_root else Path("/nonexistent")
            )
            cleanup = report["evidence"]["cleanup"]
            if not cleanup.get("all_pids_cleaned") or not cleanup.get("all_sockets_cleaned"):
                report["status"] = "failed"
                report["cleanup_reason"] = "owned processes or sockets remained after cleanup"
        except (AcceptanceError, OSError, ValueError, subprocess.TimeoutExpired):
            report["status"] = "failed"
            report["evidence"]["cleanup"] = {
                "all_pids_cleaned": False, "all_sockets_cleaned": False,
                "verification_completed": False,
            }
            report["cleanup_reason"] = "cleanup verification failed"
        try:
            write_json_report(args.output, report)
        except AcceptanceError as write_error:
            print(f"could not write report: {write_error}", file=sys.stderr)
        print(f"M19 Wayland acceptance {report['status']}: {report['reason']}"
              + (f"; {report['cleanup_reason']}" if "cleanup_reason" in report else ""), file=sys.stderr)
        return EXIT_BLOCKED if report["status"] == "blocked" else EXIT_FAILED
    finally:
        if evidence_root is not None and not args.keep:
            # Evidence is retained only when explicitly requested; the report
            # already captured every recorded hash and path.
            shutil.rmtree(evidence_root, ignore_errors=True)
        if temporary_root is not None and not args.keep:
            shutil.rmtree(temporary_root, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
