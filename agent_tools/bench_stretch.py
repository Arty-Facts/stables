#!/usr/bin/env python3
"""Benchmark the phase-vocoder stretch, on CPU and GPU, with quality proxies.

The stretch runs on every request whose speed is not 1.0, so its cost matters
most on the machines this project targets: low-end boxes without a GPU. This
measures the current implementation against candidate replacements and reports,
for each, both the time and how far its output drifts from the current one.

Quality here is a proxy, not a verdict. Magnitude distance, duration accuracy,
level and dropout length catch the ways a reconstruction goes wrong (smearing,
truncation, silence). Whether it still sounds right is a judgement only listening
can make, so each variant is also written out as a WAV.

Usage:
    python3 agent_tools/bench_stretch.py [--repeat 3] [--out DIR]
"""

from __future__ import annotations

import argparse
import pathlib
import sys
import time

import numpy as np
import torch
import torchaudio.transforms as T

ROOT = pathlib.Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tts"))

from backend.engines.piper import PiperEngine  # noqa: E402
from backend.stretch import (  # noqa: E402
    QUALITY_ITERATIONS,
    _STRETCH_CHUNK_SAMPLES,
    _STRETCH_OVERLAP_SAMPLES,
    iterations_for,
    time_stretch_audio,
)

N_FFT = 4096
WIN_LENGTH = N_FFT // 8
HOP_LENGTH = WIN_LENGTH // 8

_SAMPLE_RATE = 22050

#: Set from --quality; None means the backend's own default.
QUALITY: str | None = None


def _stft(waveform, window):
    return torch.stft(
        waveform.unsqueeze(0),
        n_fft=N_FFT,
        hop_length=HOP_LENGTH,
        win_length=WIN_LENGTH,
        window=window,
        center=True,
        return_complex=True,
    )


def _vocoder(waveform, speed):
    """The STFT and phase-vocoder step, which every variant shares."""
    window = torch.hann_window(WIN_LENGTH, device=waveform.device)
    stft = _stft(waveform, window)
    stretched = T.TimeStretch(n_freq=stft.shape[1], hop_length=HOP_LENGTH).to(
        waveform.device
    )(stft, overriding_rate=speed)
    return window, stretched


def _fit(out, source_length, speed):
    target = int(round(source_length / speed))
    if out.shape[-1] > target:
        return out[:target]
    if out.shape[-1] < target:
        pad = torch.zeros(target - out.shape[-1], device=out.device, dtype=out.dtype)
        return torch.cat((out, pad))
    return out


def variant_griffin_lim(waveform, speed, n_iter=None):
    """The current implementation: re-estimate the phase from the magnitude."""
    window, stretched = _vocoder(waveform, speed)
    gl = T.GriffinLim(
        n_fft=N_FFT,
        n_iter=n_iter if n_iter is not None else iterations_for(QUALITY),
        hop_length=HOP_LENGTH,
        win_length=WIN_LENGTH,
        power=1,
        momentum=0.999,
    ).to(waveform.device)
    return _fit(gl(torch.abs(stretched))[0], waveform.shape[-1], speed)


def variant_istft(waveform, speed):
    """Keep the phase the vocoder propagated instead of re-estimating it.

    Griffin-Lim exists to recover a phase when only a magnitude is known. The
    phase vocoder already has one, so the 32 iterations are re-deriving what is
    already in hand.
    """
    window, stretched = _vocoder(waveform, speed)
    out = torch.istft(
        stretched,
        n_fft=N_FFT,
        hop_length=HOP_LENGTH,
        win_length=WIN_LENGTH,
        window=window,
        center=True,
        length=int(round(waveform.shape[-1] / speed)),
    )
    # istft keeps the leading batch dimension, unlike GriffinLim's [0] above.
    return _fit(out[0], waveform.shape[-1], speed)


def variant_gl_small(waveform, speed):
    return variant_griffin_lim(waveform, speed, n_iter=8)


def variant_inference(waveform, speed):
    """The current maths, with autograd switched off."""
    with torch.inference_mode():
        return variant_griffin_lim(waveform, speed)


def _compiled(waveform, speed):
    @torch.compile
    def run(w, s):
        return variant_griffin_lim(w, s)

    return run(waveform, speed)


# ── measurement ──────────────────────────────────────────────────────────────


def _log_magnitude(audio: np.ndarray, sr: int = _SAMPLE_RATE) -> np.ndarray:
    w = torch.hann_window(1024)
    spec = torch.stft(
        torch.from_numpy(np.asarray(audio).reshape(-1)).float().unsqueeze(0),
        n_fft=1024,
        hop_length=256,
        win_length=1024,
        window=w,
        center=True,
        return_complex=True,
    ).abs()
    return (20 * torch.log10(spec.clamp_min(1e-6))).numpy().ravel()


def _dropout_ms(audio: np.ndarray, sr: int = _SAMPLE_RATE) -> float:
    """Longest run of near-silence, which is what a broken reconstruction sounds like."""
    quiet = np.abs(audio) < 1e-4
    longest = current = 0
    for q in quiet:
        current = current + 1 if q else 0
        longest = max(longest, current)
    return longest / sr * 1000


def _timeit(fn, repeat: int) -> float:
    fn()  # warm up
    best = float("inf")
    for _ in range(repeat):
        t0 = time.perf_counter()
        fn()
        best = min(best, time.perf_counter() - t0)
    return best


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--repeat", type=int, default=3)
    ap.add_argument("--out", default=str(ROOT / ".stretch-samples"))
    ap.add_argument("--voice", default="sv_SE-lisa-medium")
    ap.add_argument("--lang", default="sv")
    ap.add_argument(
        "--text",
        default="Det här är ett test av hur lång tid det tar att sträcka ut en mening.",
    )
    ap.add_argument("--speed", type=float, default=1.2)
    ap.add_argument(
        "--quality",
        choices=sorted(QUALITY_ITERATIONS),
        default=None,
        help="stretch quality; default is what the backend would choose",
    )
    args = ap.parse_args()

    global QUALITY
    QUALITY = args.quality

    out_dir = pathlib.Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)

    # Synthesized through the registry, so any voice works and the sample is
    # whatever the caller actually listens to.
    from backend.engines.registry import Registry

    registry = Registry()
    engine = registry.resolve(args.voice, None)
    print(f"speech sample: {args.voice} on {engine.name}")
    audio, sr = engine.synthesize(args.text, args.voice, args.lang)
    print(f"  {len(audio) / sr:.2f}s at {sr} Hz, {len(audio)} samples")

    variants = {
        f"griffin-lim x{iterations_for(QUALITY)} ({QUALITY or 'default'})": variant_griffin_lim,
        **{
            f"griffin-lim x{n} ({name})": (lambda w, sp, it=n: variant_griffin_lim(w, sp, it))
            for name, n in QUALITY_ITERATIONS.items()
            if n != iterations_for(QUALITY)
        },
        "istft (vocoder phase)": variant_istft,
        "current + inference_mode": variant_inference,
        "torch.compile of current": _compiled,
    }

    for device_name in ("cpu", "cuda"):
        if device_name == "cuda" and not torch.cuda.is_available():
            continue
        print(f"\n=== {device_name} ===")
        device = torch.device(device_name)
        wave = torch.from_numpy(audio).float().to(device)
        reference = None

        for label, fn in variants.items():
            if label.startswith("torch.compile"):
                # Compile once per shape, as the server would.
                t0 = time.perf_counter()
                result = fn(wave, args.speed)
                compile_s = time.perf_counter() - t0
            else:
                compile_s = 0.0

            try:
                with torch.inference_mode():
                    out = fn(wave, args.speed).detach().cpu().numpy()
            except Exception as e:  # a candidate that cannot run is a result too
                print(f"  {label:26s} FAILED: {type(e).__name__}: {str(e)[:70]}")
                continue

            ms = _timeit(lambda f=fn: f(wave, args.speed), args.repeat)
            notes = f"{ms * 1000:7.1f} ms"
            if compile_s:
                notes += f"  (first call {compile_s:.1f}s)"

            if reference is None:
                reference = out
                notes += "   reference"
            else:
                mag = np.abs(_log_magnitude(out) - _log_magnitude(reference)).mean()
                notes += f"   spectrum {mag:5.1f} dB off"
            notes += f"   dropout {_dropout_ms(out):4.1f} ms   rms {np.sqrt((out**2).mean()):.4f}"

            import soundfile as sf

            slug = "".join(c if c.isalnum() else "-" for c in label).strip("-")
            voice = args.voice.split("-")[0]
            sf.write(
                out_dir / f"{device_name}-{voice}-x{args.speed:g}-q{QUALITY or 'auto'}-{slug}.wav",
                out,
                sr,
            )
            print(f"  {label:26s} {notes}")

        import soundfile as sf

        sf.write(
            out_dir / f"{device_name}-{args.voice.split('-')[0]}-x1-unstretched.wav",
            audio,
            sr,
        )

        # The speed=1.0 path should cost nothing at all.
        ms = _timeit(lambda: time_stretch_audio(audio, 1.0), args.repeat)
        print(f"  {'speed=1.0 (no stretch)':26s} {ms * 1000:7.1f} ms")

    print(f"\nsamples written to {out_dir} — listen before trusting any of this")
    print(
        f"chunking: {_STRETCH_CHUNK_SAMPLES} samples/window, "
        f"{_STRETCH_OVERLAP_SAMPLES} overlap ({_STRETCH_CHUNK_SAMPLES / sr:.0f}s at this rate)"
    )
    print(
        f"quality: {QUALITY or 'default'} -> {iterations_for(QUALITY)} griffin-lim iterations"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
