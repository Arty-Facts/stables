#!/usr/bin/env python3
"""Prove voice cloning works inside the container: reference in, speech out, twice.

Run inside the voice image (it needs `qwen-tts` and the baked models):

    docker run --rm --gpus all \
      -v "$HOME/.stables/voices:/data/voices" -v "$HOME/.stables/tts/hf:/data/hf" \
      -v "$PWD/agent_tools:/work:ro" \
      -e HF_HOME=/data/hf -e STABLES_VOICES_DIR=/data/voices \
      stables-tts-qwen:local python3 /work/check_clone.py

What it checks, in order, because each step is a place the chain can break:

1. the base engine speaks, so the reference audio is real speech
2. the reference lands where the voice registry looks for it
3. `preload()` fetches the checkpoint (the installer's own code path)
4. the clone generates audio from that reference
5. twice, under two names, to show a voice is a file rather than a special case
6. the output is not silence, and is not the reference played back

Exit status is 1 if any step fails, so it can gate a release.
"""

from __future__ import annotations

import os
import pathlib
import sys
import time

import numpy as np
import soundfile as sf

# The server sets ONNX_PROVIDER from what onnxruntime reports before anything loads a
# model (start_server.py), so TensorRT is never attempted. Without it, onnxruntime
# tries its TensorRT provider, fails to load libnvinfer, and prints an error that
# reads like a broken install. This script creates sessions of its own, so it has to
# make the same choice.
sys.path.insert(0, "/app")
try:
    from backend import device

    os.environ.setdefault("ONNX_PROVIDER", device.report()["onnx_provider"])
except Exception as e:  # a diagnostic must not fail on the thing it reports on
    print(f"  ..    could not ask the backend for its provider: {e}")

REFERENCE_TEXT = os.environ.get(
    "CLONE_REF_TEXT",
    "The deploy finished, and the tests are green. Nothing needs your attention.",
)
CLONE_TEXT = os.environ.get("CLONE_TEXT", "This is a cloned voice speaking.")
REFERENCE_VOICE = os.environ.get("CLONE_REFERENCE_VOICE", "af_heart")
SIZE = os.environ.get("CLONE_SIZE", "0.6B")
VOICES = pathlib.Path(os.environ.get("STABLES_VOICES_DIR", "/data/voices"))
NAMES = ("heart-a", "heart-b")

failures: list[str] = []


def check(label: str, ok: bool, detail: str = "") -> None:
    print(f"  {'ok  ' if ok else 'FAIL'}  {label}" + (f"  -- {detail}" if detail else ""))
    if not ok:
        failures.append(label)


def describe(audio: np.ndarray, sample_rate: int) -> str:
    peak = float(np.abs(audio).max()) if audio.size else 0.0
    rms = float(np.sqrt(np.mean(np.square(audio)))) if audio.size else 0.0
    return f"{len(audio) / sample_rate:.2f}s at {sample_rate} Hz, peak {peak:.3f}, rms {rms:.4f}"


def main() -> int:
    from backend import config
    from backend.engines.kokoro import KokoroEngine
    from backend.engines.qwen import QwenEngine

    print(f"voices dir: {config.VOICES_DIR}")
    print(f"hf cache:   {config.HF_HOME}")

    # 1. Real speech to clone from. Synthesized rather than shipped, so the test has
    # no fixture to go stale and no third-party audio in the repository.
    print("\nreference audio")
    kokoro = KokoroEngine()
    check("the base engine is available", kokoro.available())
    if not kokoro.available():
        return 1
    reference, ref_rate = kokoro.synthesize(REFERENCE_TEXT, REFERENCE_VOICE, "en")
    reference = np.asarray(reference, dtype=np.float32)
    check("the base engine speaks", len(reference) > ref_rate // 2, describe(reference, ref_rate))

    # 2. A voice is a file. Both names get the same reference: the point is that the
    # registry finds them, not that they differ.
    voices = pathlib.Path(config.VOICES_DIR)
    voices.mkdir(parents=True, exist_ok=True)
    for name in NAMES:
        path = voices / f"{name}.wav"
        sf.write(str(path), reference, ref_rate)
        check(f"{path.name} written where the registry looks", path.exists(), f"{path}")

    # 3. The installer's own preload path, which is what --preload runs.
    print(f"\npreloading {SIZE}")
    engine = QwenEngine()
    check("the cloning engine is installed", engine.available())
    if not engine.available():
        return 1
    started = time.time()
    loaded = engine.preload(SIZE)
    check("preload fetches the checkpoint", loaded == SIZE, f"{loaded} in {time.time() - started:.0f}s")

    # 4 and 5. Clone, twice, under two names.
    for name in NAMES:
        print(f"\ncloning as {name}")
        started = time.time()
        try:
            audio, rate = engine.synthesize(CLONE_TEXT, name, "en", SIZE)
        except Exception as e:  # the engine's own error type, plus anything else
            check(f"{name} speaks", False, f"{type(e).__name__}: {e}")
            continue
        audio = np.asarray(audio, dtype=np.float32)
        elapsed = time.time() - started
        check(f"{name} speaks", len(audio) > 0, describe(audio, rate) + f" in {elapsed:.1f}s")

        # 6. Silence would pass "it returned an array". And a clone that merely
        # replays the reference is not cloning, so compare the two.
        rms = float(np.sqrt(np.mean(np.square(audio)))) if audio.size else 0.0
        check(f"{name} is not silence", rms > 0.001, f"rms {rms:.4f}")
        if len(audio) >= len(reference):
            same = float(np.abs(audio[: len(reference)] - reference).mean())
            check(f"{name} is not the reference replayed", same > 1e-4, f"mean |diff| {same:.6f}")

    print()
    if failures:
        print(f"{len(failures)} check(s) failed: {', '.join(failures)}")
        return 1
    print("cloning works end to end")
    return 0


if __name__ == "__main__":
    sys.exit(main())
