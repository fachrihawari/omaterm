#!/usr/bin/env python3
"""Check repository Markdown links and numbered blueprint references (stdlib only)."""

import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit


def main():
    root = Path(__file__).resolve().parents[1]
    files = sorted(root.glob("*.md"))
    files += sorted((root / "docs").rglob("*.md"))
    files += sorted((root / ".gemini").glob("*.md"))
    blueprint = (root / "OMATERM_AGENT_BLUEPRINT.md").read_text()
    sections = set(re.findall(r"^# (\d+)\.", blueprint, re.MULTILINE))
    errors = []
    links = 0
    references = 0

    ipc = (root / "docs/08-milestone-8-ipc.md").read_text()
    cli = (root / "docs/09-milestone-9-cli.md").read_text()
    method = r"[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*"
    wire_methods = set(re.findall(rf"^\| `({method})` \|", ipc, re.MULTILINE))
    cli_methods = set(re.findall(rf"^\| `[^`]+` \| `({method})` \|",
                                 cli, re.MULTILINE))
    for method in sorted(cli_methods - wire_methods):
        errors.append(f"CLI coverage method missing from IPC table: {method}")
    for method in sorted(wire_methods - cli_methods):
        errors.append(f"IPC method missing from CLI table: {method}")

    coverage_map = (root / "docs/blueprint-coverage-map.md").read_text()
    coverage_table = coverage_map.split("## Coverage Table\n", 1)[1].split(
        "\n## Open gaps", 1
    )[0]
    coverage_sections = re.findall(r"^\| (\d+) \|", coverage_table, re.MULTILINE)
    expected_sections = {str(section) for section in range(1, 80)}
    actual_sections = set(coverage_sections)
    for section in sorted(expected_sections - actual_sections, key=int):
        errors.append(f"Blueprint coverage map missing §{section}")
    for section in sorted(actual_sections - expected_sections, key=int):
        errors.append(f"Blueprint coverage map has invalid §{section}")
    for section in sorted(actual_sections, key=int):
        if coverage_sections.count(section) != 1:
            errors.append(f"Blueprint coverage map lists §{section} more than once")

    for file in files:
        text = file.read_text()
        # Examples are not live Markdown links or specification references.
        text = re.sub(r"^```[^\n]*\n.*?^```[^\n]*$", "", text,
                      flags=re.MULTILINE | re.DOTALL)
        for target in re.findall(r"\[[^\]\n]+\]\(([^)\s]+)\)", text):
            url = urlsplit(target)
            if url.scheme or url.netloc or not url.path:
                continue
            links += 1
            if not (file.parent / unquote(url.path)).exists():
                errors.append(f"{file.relative_to(root)}: missing link {target}")
        for number in re.findall(r"§(\d+)", text):
            references += 1
            if number not in sections:
                errors.append(f"{file.relative_to(root)}: missing blueprint §{number}")

    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"PASS: {len(files)} Markdown files, {links} local link targets, "
          f"{references} numbered blueprint references.")
    print(f"PASS: all {len(cli_methods)} CLI methods and {len(wire_methods)} IPC methods map both ways.")
    print("External URLs and heading anchors require separate review.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
