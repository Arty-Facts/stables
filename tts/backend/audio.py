"""Encoding helpers: float audio in, WAV or PCM bytes out."""

import base64
import io

import numpy as np
import soundfile as sf

def audio_to_wav_bytes(audio_np: np.ndarray, sample_rate: int) -> bytes:
    """Encode a float32 numpy array as WAV bytes."""
    buf = io.BytesIO()
    sf.write(buf, audio_np, sample_rate, format='WAV')
    buf.seek(0)
    return buf.read()


def audio_to_wav_b64(audio_np: np.ndarray, sample_rate: int) -> str:
    """Encode a float32 numpy array as a base64 WAV string."""
    return base64.b64encode(audio_to_wav_bytes(audio_np, sample_rate)).decode('utf-8')


def audio_to_pcm_i16(audio_np: np.ndarray) -> bytes:
    """Convert a float32 audio array to i16 LE PCM bytes."""
    audio_np = np.nan_to_num(audio_np, nan=0.0, posinf=1.0, neginf=-1.0)
    pcm = (audio_np * 32767).clip(-32768, 32767).astype(np.int16)
    return pcm.tobytes()


def make_silence_pcm(duration_s: float, sample_rate: int = 24000) -> bytes:
    """Return *duration_s* seconds of silence as i16 LE PCM bytes."""
    n = int(duration_s * sample_rate)
    return np.zeros(n, dtype=np.int16).tobytes()


# ---------------------------------------------------------------------------


def resample_to(audio: np.ndarray, sample_rate: int, target_rate: int) -> np.ndarray:
    """Resample *audio* to *target_rate*, returning it unchanged when it already is.

    The streaming endpoint promises one fixed rate to its clients (the TUI opens
    its output device once and does not renegotiate), while engines synthesize at
    their own: Kokoro at 24 kHz, Piper at 22.05 kHz. Without this, a Piper voice
    would stream at the wrong speed.
    """
    if sample_rate == target_rate or audio.size == 0:
        return audio
    from math import gcd

    from scipy.signal import resample_poly

    divisor = gcd(int(sample_rate), int(target_rate))
    up = target_rate // divisor
    down = sample_rate // divisor
    return resample_poly(audio, up, down).astype(np.float32)


def pcm_i16(audio: np.ndarray) -> bytes:
    """Convert a float audio array to i16 little-endian PCM bytes."""
    return audio_to_pcm_i16(audio)
