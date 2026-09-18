#!/usr/bin/env python3
"""Extract gzip/tar members embedded in a binary and scan their contents.

`strings` on a Go binary misses anything inside embedded .tgz assets (skills,
extensions, npm/git packages). Those archives are baked into `stables` by
`all:files/piagent`, so a key hiding in an extension config would not show up
in a plain string scan. This tool finds every gzip member in a file, unpacks
its tar entries in memory, and runs the same secret rules as
`scan_secrets.py` on each entry.

Usage:
    python3 agent_tools/scan_binary_archives.py [--providers] BINARY [BINARY ...]

Exit status: 0 clean, 1 findings, 2 usage error.
"""

from __future__ import annotations

import argparse
import io
import os
import sys
import tarfile
import zlib

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import scan_secrets  # noqa: E402

GZIP_MAGIC = b"\x1f\x8b\x08"


def gzip_members(data: bytes):
    """Yield (offset, decompressed_bytes) for each gzip member."""
    i = 0
    while True:
        i = data.find(GZIP_MAGIC, i)
        if i < 0:
            return
        try:
            d = zlib.decompressobj(16 + zlib.MAX_WBITS)
            out = d.decompress(data[i:]) + d.flush()
        except zlib.error:
            i += 3
            continue
        if out:
            yield i, out
        i += 3


def scan_member(label: str, blob: bytes, check_vendors: bool) -> list[str]:
    """Scan a decompressed member: as tar entries when possible, else raw."""
    findings: list[str] = []
    try:
        tf = tarfile.open(fileobj=io.BytesIO(blob))
    except tarfile.TarError:
        return scan_secrets.scan_blob(f"{label}:raw", blob, check_vendors)

    with tf:
        for m in tf.getmembers():
            if not m.isfile():
                continue
            fh = tf.extractfile(m)
            if fh is None:
                continue
            content = fh.read()
            findings += scan_secrets.scan_blob(f"{label}!{m.name}", content, check_vendors)
    return findings


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("binaries", nargs="+")
    ap.add_argument("--providers", action="store_true", help="also flag vendor names and endpoints")
    args = ap.parse_args()

    total = 0
    for path in args.binaries:
        with open(path, "rb") as fh:
            data = fh.read()
        members = list(gzip_members(data))
        print(f"== {path}: {len(members)} embedded gzip member(s)")
        for off, blob in members:
            label = f"{path}@gzip+{off}"
            findings = scan_member(label, blob, args.providers)
            if findings:
                print(f"   -- member at byte {off} ({len(blob)} bytes)")
                for line in findings:
                    print(f"      {line}")
            total += len(findings)
    print(f"{total} finding(s) inside embedded archives")
    return 1 if total else 0


if __name__ == "__main__":
    raise SystemExit(main())
