"""Phase-vocoder time stretching, and the tensor/PCM helpers it needs.

Time stretching is what makes `speed` sound good rather than merely fast: the
engines synthesize at their natural rate and the result is stretched, so pitch is
preserved. torch and torchaudio are imported lazily, because a request that never
changes speed never pays for them.
"""

import numpy as np

# Long input is stretched in bounded, overlapping pieces. Transforming a whole
# multi-minute waveform at once makes the intermediate spectrograms — and
# Griffin-Lim's 32 iterations over each — grow with the duration, which exhausts
# GPU memory on long text (a 6000-character request needed ~6.8 GiB and failed).
# A ~24 s window keeps the cost of one request independent of its length.
_STRETCH_CHUNK_SAMPLES = 1 << 19
_STRETCH_OVERLAP_SAMPLES = 1 << 12

#: Griffin-Lim iterations per quality level. This is the cost centre: on speech
#: the iterations are roughly two thirds of the stretch's CPU time, at about 80 ms
#: each for a 13 s clip, and each step up buys a smaller spectral difference.
#:
#: Measured against the previous fixed 32, best of three, on speech:
#:
#:     high   32 iters   cpu 2.84 s   cuda 26 ms
#:     medium 16 iters   cpu 1.55 s   cuda 14 ms   spectrum 2.8 dB off
#:     low     8 iters   cpu 0.89 s   cuda 8 ms    spectrum 3.3 dB off
#:
#: See agent_tools/bench_stretch.py for the numbers and for listening samples.
QUALITY_ITERATIONS = {"low": 8, "medium": 16, "high": 32}

#: What "the caller did not say" means.
DEFAULT_QUALITY_CPU = "medium"
DEFAULT_QUALITY_GPU = "high"


def default_quality() -> str:
    """The level to use when a request does not name one.

    High where there is a GPU, because 32 iterations cost 26 ms there and the
    difference is free. Medium on a CPU, where the same 32 cost 2.84 s and the
    listener is the one waiting for them.
    """
    from . import device

    return DEFAULT_QUALITY_GPU if device.report()["device"] == "gpu" else DEFAULT_QUALITY_CPU


def iterations_for(quality: str | None) -> int:
    """Iterations for a quality level, falling back to the device's default.

    An unknown level is not an error here: the request models reject those, so
    reaching this with one means an internal caller, and the safe answer is the
    default rather than a traceback in a request thread.
    """
    return QUALITY_ITERATIONS.get(quality or default_quality(), QUALITY_ITERATIONS[DEFAULT_QUALITY_CPU])

_DEVICE = None


def _torch_device():
    global _DEVICE
    if _DEVICE is None:
        import torch
        _DEVICE = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    return _DEVICE


def release_accelerator_memory() -> None:
    """Hand memory used by a finished request back to the operating system.

    Two things otherwise keep it: torch's CUDA caching allocator, which holds
    freed device blocks, and glibc's malloc arenas, which hold freed heap. Left
    alone the process climbs to the high-water mark of the largest request and
    stays there, so a long-running server occupies memory it will never use
    again.

    Only *unused* memory is released — tensors and buffers still referenced by
    the caller are untouched. Safe to call at any point, and a no-op where an
    API is unavailable.
    """
    try:
        import torch
    except ImportError:
        pass
    else:
        if torch.cuda.is_available():
            torch.cuda.empty_cache()

    # glibc keeps freed blocks in per-thread arenas; malloc_trim asks it to
    # return them. Best effort: other libc implementations do not have it.
    try:
        import ctypes

        ctypes.CDLL("libc.so.6").malloc_trim(0)
    except (OSError, AttributeError):
        pass


def _time_stretch_torch(waveform, speed: float, iterations: int):
    """Phase vocoder + Griffin-Lim on *waveform* (1-D torch.Tensor, already on device).

    The caller owns releasing device memory; see `release_accelerator_memory`.
    """
    import torch
    import torchaudio.transforms as T

    n_fft = 4096
    win_length = n_fft // 8
    hop_length = win_length // 8
    device = waveform.device
    window = torch.hann_window(win_length, device=device)

    stft = torch.stft(
        waveform.unsqueeze(0),
        n_fft=n_fft,
        hop_length=hop_length,
        win_length=win_length,
        window=window,
        # Padding the signal by n_fft//2 at both ends. With center=False the
        # final window never reaches the last samples, so the tail of every
        # sentence was silently dropped (~0.15 s of speech per request).
        center=True,
        return_complex=True,
    )

    stretched_stft = T.TimeStretch(
        n_freq=stft.shape[1], hop_length=hop_length
    ).to(device)(stft, overriding_rate=speed)

    griffinlim = T.GriffinLim(
        n_fft=n_fft,
        n_iter=iterations,
        hop_length=hop_length,
        win_length=win_length,
        power=1,
        momentum=0.999,
    ).to(device)

    out = griffinlim(torch.abs(stretched_stft))[0]
    return _fit_length(out, waveform.shape[-1], speed)


def _fit_length(waveform, source_length: int, speed: float):
    """Trim or pad so stretched audio has exactly the expected duration.

    `speed` is documented as scaling the duration by 1/speed, and callers add up
    durations to time their playback. The STFT and Griffin-Lim can only work in
    whole frames, so the raw result is a few samples off either way; this makes
    the contract exact rather than approximately true.
    """
    import torch

    target = int(round(source_length / speed))
    if waveform.shape[-1] > target:
        return waveform[:target]
    if waveform.shape[-1] < target:
        padding = torch.zeros(
            target - waveform.shape[-1],
            device=waveform.device,
            dtype=waveform.dtype,
        )
        return torch.cat((waveform, padding))
    return waveform


def time_stretch_audio(
    audio_np: np.ndarray, speed: float, quality: str | None = None
) -> np.ndarray:
    """
    Time-stretch *audio_np* by *speed* using a phase vocoder + Griffin-Lim.

    Returns the input array unchanged when speed == 1.0.
    For speed != 1.0 delegates to _time_stretch_torch() which requires torch
    and torchaudio (imported lazily).

    The result is a plain numpy array on the host, so any tensors used on the way
    are finished with by the time this returns and their memory is released
    before returning.
    """
    if speed == 1.0:
        return audio_np
    import torch
    waveform = torch.from_numpy(audio_np).float().to(_torch_device())
    try:
        return time_stretch_tensor(waveform, speed, quality).cpu().numpy()
    finally:
        del waveform
        release_accelerator_memory()


def stretch_bounds(total: int) -> list[tuple[int, int]]:
    """Input windows covering `total` samples in bounded, overlapping pieces.

    Every sample is covered: the first window starts at 0 and the last ends at
    `total`. Neighbouring windows overlap by `_STRETCH_OVERLAP_SAMPLES`, so the
    stretched pieces can be crossfaded back into one signal.
    """
    if total <= _STRETCH_CHUNK_SAMPLES:
        return [(0, total)]

    step = _STRETCH_CHUNK_SAMPLES - _STRETCH_OVERLAP_SAMPLES
    bounds: list[tuple[int, int]] = []
    start = 0
    while start + _STRETCH_CHUNK_SAMPLES < total:
        bounds.append((start, start + _STRETCH_CHUNK_SAMPLES))
        start += step

    # The final window ends exactly at the end of the input, so the tail is never
    # dropped. It can overlap the previous window heavily; the crossfade absorbs
    # that overlap.
    last = (total - _STRETCH_CHUNK_SAMPLES, total)
    if bounds[-1][0] < last[0]:
        bounds.append(last)
    elif bounds[-1][1] < total:
        bounds[-1] = last
    return bounds


def _time_stretch_chunked(waveform, speed: float, iterations: int):
    """Stretch a long waveform piecewise, crossfading each overlap.

    Peak memory stays proportional to the window size instead of growing with the
    length of the text being spoken.
    """
    import torch

    pieces = []
    fade = int(_STRETCH_OVERLAP_SAMPLES / speed)
    for start, end in stretch_bounds(waveform.shape[-1]):
        piece = _time_stretch_torch(waveform[..., start:end], speed, iterations)
        if pieces and fade > 0 and piece.shape[-1] > fade and pieces[-1].shape[-1] > fade:
            # The overlap was stretched twice (once as the tail of the previous
            # window, once as the head of this one); blend the two so the seam
            # does not click.
            ramp = torch.linspace(0.0, 1.0, fade, device=piece.device)
            blended = pieces[-1][-fade:] * (1.0 - ramp) + piece[:fade] * ramp
            pieces[-1] = torch.cat((pieces[-1][:-fade], blended))
            piece = piece[fade:]
        pieces.append(piece)
    return torch.cat(pieces) if len(pieces) > 1 else pieces[0]


def time_stretch_tensor(waveform, speed: float, quality: str | None = None):
    """Time-stretch a 1-D torch.Tensor on its current device. Returns a tensor."""
    if speed == 1.0:
        return waveform
    iterations = iterations_for(quality)
    if waveform.shape[-1] > _STRETCH_CHUNK_SAMPLES:
        return _time_stretch_chunked(waveform, speed, iterations)
    return _time_stretch_torch(waveform, speed, iterations)


def audio_to_pcm_i16_torch(waveform) -> bytes:
    """Convert a 1-D torch.Tensor (any device) to i16 LE PCM bytes."""
    import torch
    waveform = torch.nan_to_num(waveform, nan=0.0, posinf=1.0, neginf=-1.0)
    pcm = (waveform * 32767).clamp(-32768, 32767).to(torch.int16)
    return pcm.cpu().contiguous().numpy().tobytes()
