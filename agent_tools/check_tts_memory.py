#!/usr/bin/env python3
"""Measure whether the voice backend's memory footprint stays flat.

Starts the real backend (Kokoro model + Piper voice), fires a run of synthesis
requests at it, and samples the server's RSS after each one. A healthy server
plateaus once the models are loaded; a growing series means something per-request
is being retained.

Also counts files the server leaves in the temp directory, which should be zero.

Usage:
    python3 agent_tools/check_tts_memory.py [--requests 15] [--port 5099]
                                              [--venv agent_venv]
                                              [--tolerance 20]

Exit status is 0 when the footprint plateaus and no temp files are left behind,
1 otherwise, so it can be used as a check.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
VOICE = ROOT / "tts"

# Skip stderr banners and the like; only RSS matters here.
_TEXT = "Hej, det här är ett test av röstutmatning."


def _rss_kb(pid: int) -> int | None:
    try:
        with open(f"/proc/{pid}/status") as fh:
            for line in fh:
                if line.startswith("VmRSS:"):
                    return int(line.split()[1])
    except OSError:
        return None
    return None


def _post(url: str, payload: dict, timeout: float = 180.0):
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def _wait_healthy(url: str, proc: subprocess.Popen, deadline_s: float = 300.0) -> bool:
    start = time.time()
    while time.time() - start < deadline_s:
        if proc.poll() is not None:
            return False
        try:
            with urllib.request.urlopen(f"{url}/health", timeout=5) as resp:
                if resp.status == 200:
                    return True
        except (urllib.error.URLError, OSError):
            pass
        time.sleep(1.0)
    return False


def _temp_wavs(directory: str) -> list[str]:
    try:
        return [n for n in os.listdir(directory) if n.endswith(".wav")]
    except OSError:
        return []


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--requests", type=int, default=15, help="synthesis requests to send")
    ap.add_argument("--port", type=int, default=5099)
    ap.add_argument("--venv", default="agent_venv", help="virtualenv holding the dependencies")
    ap.add_argument(
        "--tolerance",
        type=int,
        default=20,
        help="allowed RSS drift in MB between the first and last third of the run",
    )
    ap.add_argument(
        "--warmup",
        type=int,
        default=2,
        help="requests sent before measuring, to pay one-time import/allocator costs",
    )
    ap.add_argument(
        "--long-text-chars",
        type=int,
        default=0,
        help="if >0, follow the run with one request of this many characters",
    )
    args = ap.parse_args()

    python = ROOT / args.venv / "bin" / "python"
    if not python.exists():
        print(f"error: no interpreter at {python}", file=sys.stderr)
        return 2
    if not (VOICE / "models" / "kokoro-v1.0.onnx").exists():
        print("error: tts/models/kokoro-v1.0.onnx is missing", file=sys.stderr)
        return 2

    url = f"http://127.0.0.1:{args.port}"
    tmpdir = tempfile.gettempdir()
    before_tmp = set(_temp_wavs(tmpdir))

    proc = subprocess.Popen(
        [str(python), "backend/start_server.py", "--host", "127.0.0.1", "--port", str(args.port)],
        cwd=str(VOICE),
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    try:
        if not _wait_healthy(url, proc):
            out = proc.stdout.read() if proc.stdout else ""
            print("error: server did not become healthy\n" + out[-2000:], file=sys.stderr)
            return 1

        model_rss = _rss_kb(proc.pid)
        print(f"server pid {proc.pid}, RSS after model load: {(model_rss or 0) / 1024:.0f} MB")

        def _payload(i: int) -> dict:
            # Alternate engines and speeds so both the Kokoro path and the
            # Piper + time-stretch path are exercised.
            if i % 2 == 0:
                return {"text": _TEXT, "tts": "af_heart", "engine": "kokoro",
                        "lang": "en", "speed": 1.0}
            return {"text": _TEXT, "tts": "sv_SE-lisa-medium", "engine": "piper",
                    "lang": "sv", "speed": 1.2}

        def _send(i: int) -> float:
            t0 = time.time()
            body = _post(f"{url}/v1/tts", _payload(i))
            audio = base64.b64decode(body["audio"])
            rss = (_rss_kb(proc.pid) or 0) / 1024
            print(
                f"  {_payload(i)['engine']:5s} speed={_payload(i)['speed']:<4} "
                f"{len(audio):7d} bytes  {time.time() - t0:5.1f}s  RSS {rss:7.0f} MB"
            )
            return rss

        # Warm-up: importing torch, torchaudio and their CUDA libraries costs
        # hundreds of MB once, on the first request that changes speed. That is a
        # startup cost the server is meant to keep (no repeated cold starts), so
        # it is reported on its own instead of being counted as growth.
        for i in range(args.warmup):
            print(f"warm-up {i + 1}/{args.warmup}:")
            _send(i)
        warmed = _rss_kb(proc.pid) / 1024
        print(f"RSS after warm-up: {warmed:.0f} MB (one-time cost, retained)")

        samples: list[float] = []
        for i in range(args.requests):
            samples.append(_send(i))

        third = max(1, len(samples) // 3)
        early = max(samples[:third])
        late = max(samples[-third:])
        drift = late - early
        print(f"\nmeasured phase: early max {early:.0f} MB, late max {late:.0f} MB, drift {drift:+.0f} MB")

        long_ok = True
        if args.long_text_chars > 0:
            # A big request allocates big transient buffers. Peak is allowed to
            # rise; what matters is that it comes back down instead of ratcheting.
            sentence = "Det här är en längre text som ska läsas upp. "
            text = (sentence * (args.long_text_chars // len(sentence) + 1))[
                : args.long_text_chars
            ]
            before = (_rss_kb(proc.pid) or 0) / 1024
            t0 = time.time()
            body = _post(f"{url}/v1/tts",
                         {"text": text, "tts": "sv_SE-lisa-medium",
                          "engine": "piper", "lang": "sv", "speed": 1.3})
            peak = (_rss_kb(proc.pid) or 0) / 1024
            size = len(base64.b64decode(body["audio"]))
            print(
                f"\nlong request ({len(text)} chars): {size} bytes audio, "
                f"{time.time() - t0:.1f}s, RSS {before:.0f} -> {peak:.0f} MB"
            )
            # Then a quiet period, to see whether the peak is given back.
            time.sleep(2.0)
            settled = (_rss_kb(proc.pid) or 0) / 1024
            print(f"after settling: RSS {settled:.0f} MB")
            long_ok = settled <= late + args.tolerance

        after_tmp = set(_temp_wavs(tmpdir))
        leaked = sorted(after_tmp - before_tmp)
        print(f"temp .wav files left behind: {len(leaked)}")
        for name in leaked[:10]:
            print(f"  leaked: {name}")

        ok = abs(drift) <= args.tolerance and not leaked and long_ok
        print("RESULT:", "PASS" if ok else "FAIL")
        return 0 if ok else 1
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=20)
        except subprocess.TimeoutExpired:
            proc.kill()


if __name__ == "__main__":
    raise SystemExit(main())
