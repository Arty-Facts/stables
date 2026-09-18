"""Qwen3-TTS: zero-shot voice cloning from a reference WAV.

This engine is optional. The `qwen-tts` package pins `transformers`, depends on
`gradio`, and needs 1.2-3.4 GB of weights per checkpoint, so it is installed
separately (`backend/requirements-qwen.txt`) and reports itself unavailable when
it is missing. A Kokoro-and-Piper install never downloads any of it.

Which cloning mode is used follows from what the user already has:

- with a reference transcript (`<name>.txt` beside the WAV), in-context learning:
  the model hears the reference and copies its delivery as well as its timbre;
- without one, `x_vector_only_mode`: the speaker embedding alone, slightly less
  faithful but it works from a bare WAV.

One checkpoint is held at a time. The 1.7B is ~3.4 GB in bfloat16, so switching
size unloads the previous one instead of keeping both: only the model in use is
allowed to stay resident.
"""

import gc
import pathlib

import numpy as np

from .. import voices
from ..stretch import release_accelerator_memory
from .base import EngineUnavailable, Voice, VoiceNotFound

#: Checkpoint sizes, smallest first.
SIZES = ("0.6B", "1.7B")

#: Hugging Face repository per size. "Base" is the cloning variant; the
#: CustomVoice and VoiceDesign checkpoints are for other jobs.
MODELS = {
    "0.6B": "Qwen/Qwen3-TTS-12Hz-0.6B-Base",
    "1.7B": "Qwen/Qwen3-TTS-12Hz-1.7B-Base",
}

#: What the checkpoints were trained on. Swedish is not among them, which is why
#: Piper keeps that role.
LANGUAGES = ("en", "zh", "ja", "ko", "de", "fr", "ru", "pt", "es", "it")

_LANGUAGE_NAMES = {
    "zh": "Chinese",
    "en": "English",
    "ja": "Japanese",
    "ko": "Korean",
    "de": "German",
    "fr": "French",
    "ru": "Russian",
    "pt": "Portuguese",
    "es": "Spanish",
    "it": "Italian",
}

#: Decoding settings from the reference implementation. Sampling rather than
#: greedy is what makes a clone sound like a person rather than a sample.
_GENERATION = {
    "max_new_tokens": 2048,
    "do_sample": True,
    "top_k": 50,
    "top_p": 1.0,
    "temperature": 0.9,
    "repetition_penalty": 1.05,
    "subtalker_dosample": True,
    "subtalker_top_k": 50,
    "subtalker_top_p": 1.0,
    "subtalker_temperature": 0.9,
}


def language_name(lang: str) -> str:
    """The language spelling the model API expects; "Auto" when unknown."""
    return _LANGUAGE_NAMES.get(lang, "Auto")


def default_size() -> str:
    """The checkpoint to use when a request does not name one.

    Cloning takes minutes per sentence on a CPU with either size, so the smaller
    checkpoint is used there and the better one where a GPU can keep up.
    """
    try:
        import torch
    except ImportError:
        return SIZES[0]
    return SIZES[1] if torch.cuda.is_available() else SIZES[0]


class QwenEngine:
    """Zero-shot cloning, one checkpoint resident at a time."""

    name = "qwen"
    languages = LANGUAGES

    def __init__(self, model_ids: dict | None = None, device: str | None = None):
        self._model_ids = dict(model_ids or MODELS)
        # None means decide from the hardware when the first model is loaded.
        self._device = device
        self._loaded_size: str | None = None
        self._model = None

    def available(self) -> bool:
        try:
            import qwen_tts  # noqa: F401
        except ImportError:
            return False
        return True

    def voices(self) -> list[Voice]:
        """None of its own: every Qwen voice is a cloned file.

        `voices.list_cloned()` supplies them and the registry labels them with
        this engine's name, so a cloned voice is never listed twice.
        """
        return []

    @property
    def loaded_size(self) -> str | None:
        """Which checkpoint is resident, if any. Reported by /v1/capabilities."""
        return self._loaded_size

    def unload(self) -> None:
        """Drop the resident checkpoint and return its memory."""
        if self._model is None and self._loaded_size is None:
            return
        self._model = None
        self._loaded_size = None
        # The module's tensors are only freed once the last reference is gone,
        # and the accelerator keeps the freed blocks until asked to let go.
        gc.collect()
        release_accelerator_memory()

    def _torch_dtype(self, device: str):
        import torch

        return torch.float32 if device == "cpu" else torch.bfloat16

    def _load(self, size: str):
        """Load *size*, unloading whatever was resident before."""
        if self._loaded_size == size and self._model is not None:
            return self._model
        if not self.available():
            raise EngineUnavailable(
                "cloned voices need the qwen-tts package: "
                "pip install -r backend/requirements-qwen.txt"
            )
        if size not in self._model_ids:
            raise VoiceNotFound(
                f"unknown Qwen3-TTS size {size!r}; expected one of "
                f"{', '.join(self._model_ids)}"
            )

        self.unload()

        import torch
        from qwen_tts import Qwen3TTSModel

        device = self._device or ("cuda:0" if torch.cuda.is_available() else "cpu")
        kwargs: dict = {
            "device_map": device,
            "dtype": self._torch_dtype(device),
        }
        # flash-attention is a real speedup where it is installed, and a hard
        # failure where it is not, so it is used only when it can be imported.
        if device.startswith("cuda"):
            try:
                import flash_attn  # noqa: F401

                kwargs["attn_implementation"] = "flash_attention_2"
            except ImportError:
                pass

        model = Qwen3TTSModel.from_pretrained(self._model_ids[size], **kwargs)
        self._model = model
        self._loaded_size = size
        return model

    def preload(self, size: str | None = None) -> str:
        """Load a checkpoint now, so the first request does not wait on a download.

        Returns the size that was loaded. `stables install tts --preload` uses
        this to pay the 1.2-3.4 GB cost at install time instead of mid-sentence.
        """
        chosen = size or default_size()
        self._load(chosen)
        return chosen

    @staticmethod
    def _reference(voice_id: str) -> tuple[pathlib.Path, str]:
        try:
            return voices.reference(voice_id)
        except FileNotFoundError as e:
            raise VoiceNotFound(
                f"cloned voice {voice_id!r} has no reference audio in "
                f"{voices.directory()}"
            ) from e

    def synthesize(
        self,
        text: str,
        voice: str,
        lang: str = "en",
        size: str | None = None,
    ) -> tuple[np.ndarray, int]:
        reference_audio, reference_text = self._reference(voice)
        model = self._load(size or default_size())

        audio, sample_rate = model.generate_voice_clone(
            text=text,
            language=language_name(lang),
            # A path, not an array: the model reads the file itself and we never
            # hold a second copy of the reference in memory.
            ref_audio=str(reference_audio),
            # Empty rather than None: the call still takes the argument when the
            # embedding-only path is used.
            ref_text=reference_text,
            x_vector_only_mode=not reference_text,
            **_GENERATION,
        )
        return np.asarray(audio[0], dtype=np.float32), int(sample_rate)
