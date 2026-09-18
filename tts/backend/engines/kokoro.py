"""Kokoro ONNX engine: the default, and the only one needing no download.

Its weights are baked into the image, so a fresh install can speak immediately.
The model is loaded on first use — a few seconds and roughly 500 MB — and then
kept, because reloading it per request would cost far more than holding it.

The execution provider is chosen by onnxruntime: CUDA when the container has the
libraries (the image installs onnxruntime-gpu last) and CPU otherwise.
"""

import pathlib
import re

import numpy as np

from .. import config
from .base import EngineUnavailable, Voice, VoiceNotFound


# Phoneme batching
# ---------------------------------------------------------------------------

# Kokoro's voice embedding has 510 entries (indices 0..509). Its internal
# `_split_phonemes` uses `> 510`, so a batch of exactly 510 phonemes triggers
# `voice[len(tokens)]` IndexError. Keep a safety margin.
SAFE_PHONEME_LIMIT = 500

_PUNCT_SPLIT_RE = re.compile(r"([.,!?;])")
_PUNCT_CHARS = ".,!?;"


def safe_split_phonemes(phonemes: str, max_len: int = SAFE_PHONEME_LIMIT) -> list[str]:
    """
    Split *phonemes* into batches of at most *max_len* characters.

    Prefers splits at punctuation, then spaces, then hard cuts as a last
    resort. Guarantees every returned batch satisfies len(batch) <= max_len,
    so no audio is truncated downstream.
    """
    if len(phonemes) <= max_len:
        return [phonemes] if phonemes else []

    parts = _PUNCT_SPLIT_RE.split(phonemes)
    batches: list[str] = []
    current = ""

    def flush() -> None:
        nonlocal current
        stripped = current.strip()
        if stripped:
            batches.append(stripped)
        current = ""

    for raw in parts:
        part = raw.strip()
        if not part:
            continue

        if len(part) > max_len:
            flush()
            for sub in _hard_split(part, max_len):
                batches.append(sub)
            continue

        sep = "" if part in _PUNCT_CHARS else (" " if current else "")
        if len(current) + len(sep) + len(part) > max_len:
            flush()
            sep = ""
        current += sep + part

    flush()
    return batches


def _hard_split(s: str, max_len: int) -> list[str]:
    """Split *s* into pieces of length <= max_len, preferring spaces."""
    pieces: list[str] = []
    while s:
        if len(s) <= max_len:
            pieces.append(s)
            return pieces
        cut = s.rfind(" ", 0, max_len + 1)
        if cut <= 0:
            cut = max_len
        pieces.append(s[:cut].strip())
        s = s[cut:].lstrip()
    return pieces


# ---------------------------------------------------------------------------


# ---------------------------------------------------------------------------

_GRADES = {"af_heart": "A", "af_bella": "A"}


def voice_metadata(voice_id: str) -> Voice:
    """Describe a Kokoro voice from its id.

    Kokoro ids encode the details: `af_heart` is American female, `bm_george`
    British male. Grades are the project's own quality marks, not the model's.
    """
    region = voice_id[0] if voice_id else "a"
    gender = voice_id[1] if len(voice_id) > 1 else "f"
    lang = "en-gb" if region == "b" else "en"
    name = voice_id[3:].replace("_", " ").title() if len(voice_id) > 3 else voice_id
    return Voice(
        id=voice_id,
        name=name,
        lang=lang,
        engine="kokoro",
        gender=gender,
        grade=_GRADES.get(voice_id, "B"),
    )


def _kokoro_lang(voice_id: str) -> str:
    """The phonemizer language for *voice_id*, which kokoro-onnx wants spelled out."""
    return "en-gb" if voice_id.startswith("b") else "en-us"


class KokoroEngine:
    """Kokoro ONNX text-to-speech."""

    name = "kokoro"
    languages = ("en",)

    def __init__(self, model_path=None, voices_path=None):
        self._model_path = pathlib.Path(model_path or config.KOKORO_MODEL)
        self._voices_path = pathlib.Path(voices_path or config.KOKORO_VOICES)
        self._kokoro = None

    def available(self) -> bool:
        if not (self._model_path.exists() and self._voices_path.exists()):
            return False
        try:
            import kokoro_onnx  # noqa: F401
        except ImportError:
            return False
        return True

    def _load(self):
        """Load the model once and keep it."""
        if self._kokoro is None:
            if not self.available():
                raise EngineUnavailable(
                    "Kokoro needs "
                    f"{config.KOKORO_MODEL.name} and {config.KOKORO_VOICES.name} "
                    f"in {self._model_path.parent}"
                )
            from kokoro_onnx import Kokoro

            kokoro = Kokoro(str(self._model_path), str(self._voices_path))
            # Kokoro's own splitter overruns the voice embedding on long input;
            # see safe_split_phonemes above.
            kokoro._split_phonemes = lambda phonemes: safe_split_phonemes(phonemes)
            self._kokoro = kokoro
        return self._kokoro

    def voices(self) -> list[Voice]:
        return [voice_metadata(v) for v in self._load().get_voices()]

    def synthesize(
        self, text: str, voice: str, lang: str = "en", size: str | None = None
    ) -> tuple[np.ndarray, int]:
        # `size` is ignored: Kokoro ships one checkpoint.
        kokoro = self._load()
        known = kokoro.get_voices()
        if voice and voice not in known:
            raise VoiceNotFound(f"Kokoro has no voice {voice!r}")
        # speed=1.0 here: the caller applies the stretch, so every engine gets
        # identical speed behaviour.
        audio, sample_rate = kokoro.create(
            text, voice=voice, speed=1.0, lang=_kokoro_lang(voice)
        )
        return np.asarray(audio, dtype=np.float32), int(sample_rate)
