#!/usr/bin/env python3
"""Scan files or directories for secret-looking strings and provider leaks.

Use before publishing a repo, shipping a binary, or committing generated
artifacts. Works on text and on binaries (binaries are reduced to printable
strings first, like `strings -a`).

Usage:
    python3 agent_tools/scan_secrets.py PATH [PATH ...]
    python3 agent_tools/scan_secrets.py --providers PATH      # also flag vendor names
    python3 agent_tools/scan_secrets.py --quiet PATH

Exit status: 0 clean, 1 findings, 2 usage error.

Notes:
    - Matches are printed REDACTED: only the first 6 and last 4 chars survive.
    - Placeholders (sk-your-key, sk-test, example, changeme, xxxx) are ignored
      for the generic apiKey rules but still reported for hard key prefixes.
"""

from __future__ import annotations

import argparse
import os
import re
import sys

# Hard credential shapes. These are reported even in short form.
HARD_PATTERNS = [
    ("private-key", re.compile(rb"-----BEGIN [A-Z ]{0,20}PRIVATE KEY-----")),
    ("aws-access-key", re.compile(rb"AKIA[0-9A-Z]{16}")),
    ("github-token", re.compile(rb"gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{20,}")),
    ("slack-token", re.compile(rb"xox[baprs]-[A-Za-z0-9-]{10,}")),
    ("google-api-key", re.compile(rb"AIza[0-9A-Za-z_-]{30,}")),
    ("openai-key", re.compile(rb"sk-(?:ant-)?[A-Za-z0-9_-]{24,}")),
    ("stripe-key", re.compile(rb"[rs]k_(?:live|test)_[A-Za-z0-9]{16,}")),
    ("jwt", re.compile(rb"eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}")),
]

# config-style assignments: NAME=value or "NAME": "value".
ASSIGN = re.compile(
    rb"""(?ix)
    \b(api[_-]?key|token|secret|password|passwd|access[_-]?key|private[_-]?key)\b
    \s*[:=]\s*
    ["']?([A-Za-z0-9_\-./+=$]{8,})["']?
    """
)

# Values that are obviously placeholders or variable references.
PLACEHOLDER = re.compile(
    rb"(?ix)^(\$|\$\{|<|your[-_]|my[-_]|test|dummy|fake|example|sample|placeholder|"
    rb"changeme|redacted|xxx+|\*+|none|null|true|false|ollama|test[-_]?key[-_]?literal)"
)

# Vendor / endpoint leaks the Stables repo forbids in tracked files.
VENDOR_PATTERNS = [
    ("vendor-endpoint", re.compile(rb"(?i)api\.(openai|anthropic|deepseek|mistral|groq|x)\.com")),
    ("vendor-name", re.compile(rb"(?i)\b(deepseek|anthropic|openai|contextvision|artylocal|inferno|openrouter)\b")),
]

TEXT_SUFFIX_BLOCK = {".png", ".jpg", ".jpeg", ".gif", ".webp", ".zip", ".gz", ".tgz", ".pdf", ".woff", ".woff2"}


def printable_strings(data: bytes, min_len: int = 6) -> list[tuple[int, bytes]]:
    """Yield (offset, string) for printable runs, like `strings -a`."""
    out = []
    start = None
    for i, b in enumerate(data):
        printable = 32 <= b < 127 or b in (9,)
        if printable:
            if start is None:
                start = i
        else:
            if start is not None and i - start >= min_len:
                out.append((start, data[start:i]))
            start = None
    if start is not None and len(data) - start >= min_len:
        out.append((start, data[start:]))
    return out


def redact(match: bytes) -> str:
    s = match.decode("utf-8", "replace")
    if len(s) <= 12:
        return s
    return f"{s[:6]}…{s[-4:]}"


def loc(path: str, base: int, data: bytes, pos: int, is_binary: bool) -> str:
    """Human location: a byte offset in binaries, a line number in text."""
    if is_binary:
        return f"{path}:byte {base + pos}"
    return f"{path}:{data.count(chr(10).encode(), 0, pos) + 1}"


def scan_blob(path: str, data: bytes, check_vendors: bool) -> list[str]:
    findings: list[str] = []
    patterns = list(HARD_PATTERNS)
    if check_vendors:
        patterns += VENDOR_PATTERNS

    is_binary = b"\0" in data[:8192]
    units = printable_strings(data) if is_binary else [(0, data)]

    for off, chunk in units:
        # Hard + vendor patterns: report every occurrence.
        for name, pat in patterns:
            for m in pat.finditer(chunk):
                findings.append(f"{name:16} {loc(path, off, data, m.start(), is_binary)}  {redact(m.group(0))}")

        # Assignment patterns: skip obvious placeholders / $VAR refs.
        for m in ASSIGN.finditer(chunk):
            value = m.group(2)
            if PLACEHOLDER.match(value):
                continue
            findings.append(f"{'assignment':16} {loc(path, off, data, m.start(), is_binary)}  {redact(m.group(0))}")

    return findings


def iter_files(paths: list[str]):
    for p in paths:
        if os.path.isdir(p):
            for root, dirs, files in os.walk(p):
                dirs[:] = [d for d in dirs if d not in (".git", "node_modules", "__pycache__")]
                for f in files:
                    fp = os.path.join(root, f)
                    if os.path.splitext(f)[1].lower() in TEXT_SUFFIX_BLOCK:
                        continue
                    yield fp
        else:
            yield p


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("paths", nargs="+")
    ap.add_argument("--providers", action="store_true", help="also flag vendor names and endpoints")
    ap.add_argument("--quiet", action="store_true", help="only print the summary line")
    args = ap.parse_args()

    total = 0
    scanned = 0
    for path in iter_files(args.paths):
        try:
            with open(path, "rb") as fh:
                data = fh.read()
        except OSError as e:
            print(f"skip {path}: {e}", file=sys.stderr)
            continue
        scanned += 1
        findings = scan_blob(path, data, args.providers)
        if findings and not args.quiet:
            print(f"== {path}")
            for line in findings:
                print(f"   {line}")
        total += len(findings)

    print(f"scanned {scanned} file(s); {total} finding(s)")
    return 1 if total else 0


if __name__ == "__main__":
    raise SystemExit(main())
