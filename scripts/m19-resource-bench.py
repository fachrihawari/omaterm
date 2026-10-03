#!/usr/bin/env python3
"""Collect M19 S8 process resources and summarize explicit instrumentation.

The metrics input is UTF-8 JSON with this bounded, content-free shape:

{
  "timings_ms": {
    "open_enqueue_to_ready": [1.2, 1.5],
    "edit_to_frame": [0.8],
    "edit_to_highlight": [0.9],
    "save_to_commit": [2.0],
    "close_to_retirement": [0.7],
    "restart_to_usable": [3.1]
  },
  "counters": {
    "rendered_rows": 42,
    "consulted_spans": 90,
    "buffer_copy_count": 1,
    "queue_depth": 0,
    "worker_count": 1,
    "retained_document_bytes": 1024,
    "active_jobs": 0,
    "pending_jobs": 0,
    "result_count": 0,
    "token_memory_bytes": 4096
  }
}

It intentionally accepts no document contents, environment data, command lines,
or arbitrary metric names. The output contains only numeric process/resource
observations and percentile summaries. Run --self-test to validate the parser
and /proc reader against an isolated synthetic proc tree.
"""

from __future__ import annotations

import argparse
import datetime as datetime_module
import json
import math
import os
import stat
import sys
import tempfile
import time
from pathlib import Path
from typing import Any


MAX_METRICS_BYTES = 16 * 1024 * 1024
TIMING_NAMES = (
    "open_enqueue_to_ready",
    "edit_to_frame",
    "edit_to_highlight",
    "save_to_commit",
    "close_to_retirement",
    "restart_to_usable",
)
COUNTER_NAMES = (
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
)


class BenchError(Exception):
    """A deliberately content-free benchmark failure."""


def finite_number(value: object, *, non_negative: bool = True) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise BenchError("metrics JSON has a non-numeric value")
    number = float(value)
    if not math.isfinite(number) or (non_negative and number < 0):
        raise BenchError("metrics JSON has an invalid numeric value")
    return number


def percentile(values: list[float], fraction: float) -> float:
    if not values:
        raise BenchError("cannot calculate a percentile without samples")
    ordered = sorted(values)
    position = (len(ordered) - 1) * fraction
    lower = math.floor(position)
    upper = math.ceil(position)
    if lower == upper:
        return ordered[lower]
    return ordered[lower] + (ordered[upper] - ordered[lower]) * (position - lower)


def summary(values: list[float]) -> dict[str, float | int]:
    return {
        "sample_count": len(values),
        "min": percentile(values, 0.0),
        "p50": percentile(values, 0.50),
        "p95": percentile(values, 0.95),
        "max": percentile(values, 1.0),
    }


def load_metrics(path: Path) -> dict[str, dict[str, list[float]] | dict[str, float]]:
    try:
        info = path.lstat()
    except OSError as error:
        raise BenchError("metrics JSON is unavailable") from error
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise BenchError("metrics JSON must be a regular file")
    if info.st_size > MAX_METRICS_BYTES:
        raise BenchError("metrics JSON exceeds the size limit")
    try:
        raw = path.read_bytes()
        document = json.loads(raw.decode("utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise BenchError("metrics JSON is invalid") from error
    if not isinstance(document, dict) or set(document) - {"timings_ms", "counters"}:
        raise BenchError("metrics JSON has an unsupported schema")

    timings = document.get("timings_ms")
    if not isinstance(timings, dict) or set(timings) != set(TIMING_NAMES):
        raise BenchError("metrics JSON must provide every required timing series")
    parsed_timings: dict[str, list[float]] = {}
    for name in TIMING_NAMES:
        values = timings[name]
        if not isinstance(values, list) or not values:
            raise BenchError("metrics JSON has an empty timing series")
        parsed_timings[name] = [finite_number(value) for value in values]

    counters = document.get("counters", {})
    if not isinstance(counters, dict) or set(counters) - set(COUNTER_NAMES):
        raise BenchError("metrics JSON has an unsupported counter")
    parsed_counters = {name: finite_number(value) for name, value in counters.items()}
    return {"timings_ms": parsed_timings, "counters": parsed_counters}


def proc_stat(proc_dir: Path) -> tuple[int, int]:
    try:
        raw = (proc_dir / "stat").read_text(encoding="ascii")
        closing_paren = raw.rfind(")")
        fields = raw[closing_paren + 2 :].split()
        if closing_paren < 1 or len(fields) <= 19:
            raise ValueError("malformed stat")
        cpu_ticks = int(fields[11]) + int(fields[12])
        start_time_ticks = int(fields[19])
    except (OSError, UnicodeDecodeError, ValueError) as error:
        raise BenchError("process is unavailable or its proc stat is invalid") from error
    if cpu_ticks < 0 or start_time_ticks < 0:
        raise BenchError("process proc stat has invalid counters")
    return cpu_ticks, start_time_ticks


def count_entries(path: Path) -> int:
    try:
        with os.scandir(path) as entries:
            return sum(1 for _ in entries)
    except OSError as error:
        raise BenchError("process resource directory is unavailable") from error


def rss_bytes(proc_dir: Path) -> int:
    try:
        fields = (proc_dir / "statm").read_text(encoding="ascii").split()
        pages = int(fields[1])
    except (OSError, UnicodeDecodeError, ValueError, IndexError) as error:
        raise BenchError("process RSS is unavailable") from error
    if pages < 0:
        raise BenchError("process RSS is invalid")
    return pages * os.sysconf("SC_PAGE_SIZE")


class ProcessSampler:
    def __init__(self, proc_root: Path, pid: int) -> None:
        try:
            root_info = proc_root.lstat()
        except OSError as error:
            raise BenchError("proc root is unavailable") from error
        if stat.S_ISLNK(root_info.st_mode) or not stat.S_ISDIR(root_info.st_mode):
            raise BenchError("proc root must be a directory")
        self.proc_dir = proc_root / str(pid)
        self.pid = pid
        self.clock_ticks = os.sysconf("SC_CLK_TCK")
        if not isinstance(self.clock_ticks, int) or self.clock_ticks <= 0:
            raise BenchError("system clock tick rate is unavailable")
        _, self.start_time_ticks = proc_stat(self.proc_dir)
        self.previous_ticks: int | None = None
        self.previous_time: float | None = None

    def sample(self) -> dict[str, float | int | None]:
        before_ticks, before_start = proc_stat(self.proc_dir)
        if before_start != self.start_time_ticks:
            raise BenchError("process identity changed during collection")
        observed_at = time.monotonic()
        fd_count = count_entries(self.proc_dir / "fd")
        thread_count = count_entries(self.proc_dir / "task")
        current_rss = rss_bytes(self.proc_dir)
        after_ticks, after_start = proc_stat(self.proc_dir)
        if after_start != self.start_time_ticks or after_ticks < before_ticks:
            raise BenchError("process identity changed during collection")

        cpu_percent: float | None = None
        if self.previous_ticks is not None and self.previous_time is not None:
            elapsed = observed_at - self.previous_time
            if elapsed <= 0 or after_ticks < self.previous_ticks:
                raise BenchError("process CPU sample is invalid")
            cpu_percent = (after_ticks - self.previous_ticks) / self.clock_ticks / elapsed * 100.0
        self.previous_ticks = after_ticks
        self.previous_time = observed_at
        return {
            "monotonic_seconds": observed_at,
            "fd_count": fd_count,
            "thread_count": thread_count,
            "rss_bytes": current_rss,
            "cpu_percent": cpu_percent,
        }


def collect_samples(sampler: ProcessSampler, samples: int, interval_seconds: float) -> list[dict[str, float | int | None]]:
    collected = [sampler.sample()]
    for _ in range(1, samples):
        time.sleep(interval_seconds)
        collected.append(sampler.sample())
    return collected


def process_summary(samples: list[dict[str, float | int | None]]) -> dict[str, dict[str, float | int]]:
    result: dict[str, dict[str, float | int]] = {}
    for name in ("fd_count", "thread_count", "rss_bytes"):
        result[name] = summary([float(sample[name]) for sample in samples])
    cpu_values = [float(sample["cpu_percent"]) for sample in samples if sample["cpu_percent"] is not None]
    if cpu_values:
        result["cpu_percent"] = summary(cpu_values)
    return result


def write_report(path: Path, report: dict[str, Any], force: bool) -> None:
    if path.exists() or path.is_symlink():
        if path.is_symlink() or not force:
            raise BenchError("refusing to replace the output report")
    parent = path.parent
    if not parent.is_dir():
        raise BenchError("output parent directory is unavailable")
    encoded = (json.dumps(report, indent=2, sort_keys=True, allow_nan=False) + "\n").encode("utf-8")
    try:
        descriptor, temporary_name = tempfile.mkstemp(prefix=".m19-resource-", suffix=".json", dir=parent)
        with os.fdopen(descriptor, "wb") as temporary:
            temporary.write(encoded)
            temporary.flush()
            os.fsync(temporary.fileno())
        os.replace(temporary_name, path)
    except OSError as error:
        try:
            os.unlink(temporary_name)
        except (OSError, UnboundLocalError):
            pass
        raise BenchError("could not write the output report") from error


def build_report(sampler: ProcessSampler, samples: list[dict[str, float | int | None]], interval_seconds: float, metrics: dict[str, dict[str, list[float]] | dict[str, float]]) -> dict[str, Any]:
    timings = metrics["timings_ms"]
    counters = metrics["counters"]
    assert isinstance(timings, dict)
    assert isinstance(counters, dict)
    return {
        "format": 1,
        "collected_at_utc": datetime_module.datetime.now(datetime_module.timezone.utc).isoformat(),
        "process": {
            "pid": sampler.pid,
            "start_time_ticks": sampler.start_time_ticks,
            "clock_ticks_per_second": sampler.clock_ticks,
            "requested_sample_count": len(samples),
            "sample_interval_seconds": interval_seconds,
            "samples": samples,
            "summary": process_summary(samples),
        },
        "instrumentation": {
            "timings_ms": {name: summary(values) for name, values in timings.items()},
            "counters": counters,
        },
    }


def self_test() -> None:
    assert percentile([1.0, 2.0, 3.0, 4.0], 0.5) == 2.5
    assert math.isclose(percentile([1.0, 2.0, 3.0, 4.0], 0.95), 3.85)
    with tempfile.TemporaryDirectory() as temporary:
        root = Path(temporary)
        process = root / "4242"
        (process / "fd").mkdir(parents=True)
        (process / "task" / "4242").mkdir(parents=True)
        (process / "fd" / "0").touch()
        (process / "fd" / "1").touch()
        # The field positions match Linux /proc/<pid>/stat after the command name.
        fields = ["S"] + ["0"] * 21
        fields[11] = "7"
        fields[12] = "3"
        fields[19] = "12345"
        (process / "stat").write_text("4242 (test process) " + " ".join(fields), encoding="ascii")
        (process / "statm").write_text("10 2 0 0 0 0 0\n", encoding="ascii")
        metrics_path = root / "metrics.json"
        metrics_path.write_text(
            json.dumps({"timings_ms": {name: [1.0, 2.0] for name in TIMING_NAMES}, "counters": {"worker_count": 1}}),
            encoding="utf-8",
        )
        sampler = ProcessSampler(root, 4242)
        sample = sampler.sample()
        assert sample["fd_count"] == 2
        assert sample["thread_count"] == 1
        assert sample["rss_bytes"] == 2 * os.sysconf("SC_PAGE_SIZE")
        fields[11] = "8"
        (process / "stat").write_text("4242 (test process) " + " ".join(fields), encoding="ascii")
        time.sleep(0.001)
        assert sampler.sample()["cpu_percent"] is not None
        assert load_metrics(metrics_path)["counters"] == {"worker_count": 1.0}
        report_path = root / "report.json"
        write_report(report_path, {"format": 1}, force=False)
        assert json.loads(report_path.read_text(encoding="utf-8")) == {"format": 1}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--pid", type=int, help="existing positive process ID to sample")
    parser.add_argument("--proc-root", type=Path, help="explicit proc filesystem root, normally /proc")
    parser.add_argument("--metrics-json", type=Path, help="explicit instrumentation JSON path")
    parser.add_argument("--output", type=Path, help="explicit new report JSON path")
    parser.add_argument("--samples", type=int, default=20, help="number of snapshots (default: 20)")
    parser.add_argument("--interval-seconds", type=float, default=1.0, help="delay between snapshots (default: 1.0)")
    parser.add_argument("--force", action="store_true", help="replace an existing non-symlink output report")
    parser.add_argument("--self-test", action="store_true", help="run isolated parser and proc-reader checks")
    args = parser.parse_args()
    supplied = (args.pid, args.proc_root, args.metrics_json, args.output)
    if args.self_test:
        if any(value is not None for value in supplied):
            parser.error("--self-test does not accept collection paths or a PID")
        return args
    if any(value is None for value in supplied):
        parser.error("--pid, --proc-root, --metrics-json, and --output are required")
    if args.pid <= 0:
        parser.error("--pid must be positive")
    if args.samples <= 0:
        parser.error("--samples must be positive")
    if not math.isfinite(args.interval_seconds) or args.interval_seconds <= 0:
        parser.error("--interval-seconds must be finite and positive")
    return args


def main() -> int:
    args = parse_args()
    try:
        if args.self_test:
            self_test()
            print("m19 resource bench self-test passed")
            return 0
        assert args.proc_root is not None
        assert args.metrics_json is not None
        assert args.output is not None
        sampler = ProcessSampler(args.proc_root, args.pid)
        samples = collect_samples(sampler, args.samples, args.interval_seconds)
        metrics = load_metrics(args.metrics_json)
        report = build_report(sampler, samples, args.interval_seconds, metrics)
        write_report(args.output, report, args.force)
    except BenchError as error:
        print(f"m19 resource bench failed: {error}", file=sys.stderr)
        return 1
    print("m19 resource report written")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
