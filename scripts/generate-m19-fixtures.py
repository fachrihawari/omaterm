#!/usr/bin/env python3
"""Create disposable, deterministic M19 editor fixtures.

The fixture tree is generated rather than committed because the cap boundary
files are intentionally larger than 1 MiB. It is safe to run only against a
new or empty output directory; this script never removes an existing tree.
"""

from __future__ import annotations

import argparse
import json
import os
import stat
import sys
from pathlib import Path


MAX_EDITOR_BYTES = 1024 * 1024
MAX_EDITOR_LINES = 20_000
SAME_SIZE_MTIME_NS = 1_700_000_000_123_456_789


def write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)


def text_lines(count: int) -> bytes:
    if count == 0:
        return b""
    return (b"x\n" * (count - 1)) + b"x"


def require_empty_output(path: Path) -> None:
    if path.exists() and any(path.iterdir()):
        raise ValueError(f"output directory is not empty: {path}")
    path.mkdir(parents=True, exist_ok=True)


def symlink(target: str, link: Path) -> None:
    if link.exists() or link.is_symlink():
        raise ValueError(f"refusing to replace fixture path: {link}")
    link.parent.mkdir(parents=True, exist_ok=True)
    os.symlink(target, link)


def generate(output: Path) -> dict[str, object]:
    require_empty_output(output)
    project_a = output / "proj-a"
    project_b = output / "proj-b"
    outside = output / "outside"

    language_files = {
        "src/main.rs": b"fn main() { println!(\"fixture\"); }\n",
        "docs/notes.md": b"# Fixture\n\n`code`\n",
        "config/settings.toml": b"enabled = true\ncount = 7\n",
        "data/value.json": b'{"enabled": true, "count": -12.5e2}\n',
        "scripts/run.sh": b"#!/bin/sh\nif [ $# -gt 0 ]; then echo \"$1\"; fi\n",
        "plain.txt": b"plain text\n",
        "empty.txt": b"",
    }
    for relative, data in language_files.items():
        write(project_a / relative, data)
        write(project_b / relative, data)

    write(
        project_a / "text/unicode-tabs.txt",
        "combining: e\u0301\nCJK: \u754c\nemoji: \U0001f469\u200d\U0001f4bb\ntab:\tstop\nspaces:    kept\n".encode(),
    )
    write(project_a / "newlines/lf.txt", b"one\ntwo\n")
    write(project_a / "newlines/crlf.txt", b"one\r\ntwo\r\n")
    write(project_a / "newlines/mixed.txt", b"one\r\ntwo\nthree\r\n")
    write(project_a / "newlines/no-final-newline.txt", b"one\ntwo")
    write(project_a / "long/single-line.txt", b"x" * (256 * 1024))
    write(project_a / "limits/at-byte-cap.txt", b"x" * MAX_EDITOR_BYTES)
    write(project_a / "limits/over-byte-cap.txt", b"x" * (MAX_EDITOR_BYTES + 1))
    write(project_a / "limits/at-line-cap.txt", text_lines(MAX_EDITOR_LINES))
    write(project_a / "limits/over-line-cap.txt", text_lines(MAX_EDITOR_LINES + 1))
    write(project_a / "limits/token-dense.rs", b"let a=1;/*x*/" * 40_000)
    write(project_a / "invalid/binary-nul.bin", b"text\x00binary\n")
    write(project_a / "invalid/invalid-utf8.txt", b"valid\xff\xfeinvalid\n")
    (project_a / "invalid/directory").mkdir(parents=True)
    write(project_a / "invalid/unreadable.txt", b"permissions test\n")
    os.chmod(project_a / "invalid/unreadable.txt", 0)
    fifo = project_a / "invalid/fifo"
    if os.name == "posix":
        os.mkfifo(fifo)

    write(outside / "secret.txt", b"outside root\n")
    symlink("src", project_a / "links/contained-src")
    symlink("../../outside/secret.txt", project_a / "links/escape-file")
    symlink("../../outside", project_a / "links/escape-ancestor")

    write(project_a / "mutable/replace-me.txt", b"original\n")
    write(project_a / "mutable/replacement-source.txt", b"replace!\n")
    write(project_a / "mutable/same-size-before.txt", b"abcdefgh\n")
    write(project_a / "mutable/same-size-after.txt", b"ABCDEFGH\n")
    for name in ("same-size-before.txt", "same-size-after.txt"):
        os.utime(project_a / "mutable" / name, ns=(SAME_SIZE_MTIME_NS, SAME_SIZE_MTIME_NS))
    write(project_a / "root-replacement/original/document.txt", b"original root\n")
    write(project_a / "root-replacement/replacement/document.txt", b"replacement root\n")

    non_utf8_hex = None
    if os.name == "posix":
        non_utf8_name = b"non-utf8-\xff.txt"
        non_utf8_path = os.path.join(os.fsencode(project_a), b"paths", non_utf8_name)
        os.makedirs(os.path.dirname(non_utf8_path), exist_ok=True)
        with open(non_utf8_path, "wb") as handle:
            handle.write(b"non-UTF-8 pathname\n")
        non_utf8_hex = (b"paths/" + non_utf8_name).hex()

    manifest: dict[str, object] = {
        "format": 1,
        "generator": "scripts/generate-m19-fixtures.py",
        "projects": ["proj-a", "proj-b"],
        "same_relative_path": "src/main.rs",
        "expected": {
            "languages": sorted(language_files),
            "text_cases": [
                "text/unicode-tabs.txt",
                "newlines/lf.txt",
                "newlines/crlf.txt",
                "newlines/mixed.txt",
                "newlines/no-final-newline.txt",
                "long/single-line.txt",
            ],
            "limits": {
                "max_editor_bytes": MAX_EDITOR_BYTES,
                "max_editor_lines": MAX_EDITOR_LINES,
                "at_byte_cap": "limits/at-byte-cap.txt",
                "over_byte_cap": "limits/over-byte-cap.txt",
                "at_line_cap": "limits/at-line-cap.txt",
                "over_line_cap": "limits/over-line-cap.txt",
                "token_dense": "limits/token-dense.rs",
            },
            "invalid": [
                "invalid/binary-nul.bin",
                "invalid/invalid-utf8.txt",
                "invalid/unreadable.txt",
                "invalid/directory",
                "invalid/fifo" if os.name == "posix" else None,
                "missing.txt",
            ],
            "links": {
                "contained": "links/contained-src",
                "outside_file": "links/escape-file",
                "outside_ancestor": "links/escape-ancestor",
            },
            "mutable": {
                "target": "mutable/replace-me.txt",
                "replacement_source": "mutable/replacement-source.txt",
                "same_size_before": "mutable/same-size-before.txt",
                "same_size_after": "mutable/same-size-after.txt",
                "same_size_mtime_ns": SAME_SIZE_MTIME_NS,
                "root_original": "root-replacement/original",
                "root_replacement": "root-replacement/replacement",
            },
            "non_utf8_relative_path_hex": non_utf8_hex,
        },
        "synthetic_cases": {
            "over_path_cap": "PathBuf from 4097 ASCII bytes; do not create it on disk",
            "save_failures": "inject temp-create/write/file-sync/rename/directory-sync failures",
            "races": "use barriers around descriptor resolution/read/write, never sleeps",
        },
    }
    write(output / "manifest.json", json.dumps(manifest, indent=2, sort_keys=True).encode() + b"\n")
    return manifest


def verify(output: Path, manifest: dict[str, object]) -> None:
    expected = manifest["expected"]
    assert isinstance(expected, dict)
    for relative in expected["languages"]:
        assert (output / "proj-a" / relative).is_file()
        assert (output / "proj-b" / relative).is_file()
    limits = expected["limits"]
    assert isinstance(limits, dict)
    assert (output / "proj-a" / limits["at_byte_cap"]).stat().st_size == MAX_EDITOR_BYTES
    assert (output / "proj-a" / limits["over_byte_cap"]).stat().st_size == MAX_EDITOR_BYTES + 1
    assert (output / "proj-a" / limits["at_line_cap"]).read_bytes().count(b"\n") + 1 == MAX_EDITOR_LINES
    assert (output / "proj-a" / limits["over_line_cap"]).read_bytes().count(b"\n") + 1 == MAX_EDITOR_LINES + 1
    assert stat.S_ISDIR((output / "proj-a" / "invalid/directory").stat().st_mode)
    if os.name == "posix":
        assert stat.S_ISFIFO((output / "proj-a" / "invalid/fifo").stat().st_mode)
    assert (output / "proj-a" / "links/contained-src").is_symlink()
    assert (output / "proj-a" / "links/escape-file").is_symlink()
    mutable = expected["mutable"]
    assert isinstance(mutable, dict)
    assert (output / "proj-a" / mutable["same_size_before"]).stat().st_mtime_ns == SAME_SIZE_MTIME_NS
    assert (output / "proj-a" / mutable["same_size_after"]).stat().st_mtime_ns == SAME_SIZE_MTIME_NS
    non_utf8_hex = expected["non_utf8_relative_path_hex"]
    if non_utf8_hex is not None:
        non_utf8_path = os.path.join(os.fsencode(output / "proj-a"), bytes.fromhex(non_utf8_hex))
        assert os.path.isfile(non_utf8_path)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True, help="new or empty fixture directory")
    args = parser.parse_args()
    try:
        output = args.output.expanduser().resolve()
        manifest = generate(output)
        verify(output, manifest)
    except (OSError, ValueError, AssertionError) as error:
        print(f"fixture generation failed: {error}", file=sys.stderr)
        return 1
    print(f"generated M19 fixtures: {output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
