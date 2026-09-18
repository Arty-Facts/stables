"""Piper engine: small, fast, and covers languages Kokoro does not.

Synthesis runs in the `piper` CLI rather than in-process, so the ONNX weights are
read from disk by that process and this module holds only a path and a config per
voice. The CLI is looked for next to the running interpreter first, which makes a
virtualenv work whether or not it was activated.

Voice models are fetched from the upstream `rhasspy/piper-voices` repository on
first use and cached under `models/piper/`.
"""

import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request

import numpy as np
import soundfile as sf

from collections import OrderedDict

from .. import config
from .base import EngineUnavailable, Voice, VoiceNotFound

#: How many loaded voices to hold. Each keeps its ONNX weights in memory, about
#: 63 MB, so this is the trade: a reload costs ~600 ms and this is what it costs
#: to avoid one. Two is enough to move between a couple of voices without
#: reloading, and small enough not to matter on a low-end machine.
VOICES_IN_MEMORY = 2


def _python_api_available() -> bool:
    """Whether the in-process path can be used at all."""
    try:
        import piper  # noqa: F401
    except ImportError:
        return False
    return True


def _load_piper_voice(model_path: str):
    """Load a Piper voice through the Python API.

    A function of its own so tests can replace it: faking the `piper` module in
    `sys.modules` outlives the test and every test after it imports the fake.
    """
    from piper import PiperVoice

    return PiperVoice.load(model_path)

VOICES_BASE_URL = "https://huggingface.co/rhasspy/piper-voices/resolve/main"

# Long enough for a paragraph on a CPU-only host; the CLI synthesizes the whole
# text in one process.
_CLI_TIMEOUT_S = 120


def find_piper() -> str | None:
    """The `piper` CLI: beside the interpreter first, then on PATH.

    Returns None when it is not installed, so callers can report the engine as
    unavailable instead of failing later with FileNotFoundError.
    """
    name = "piper.exe" if os.name == "nt" else "piper"
    beside = pathlib.Path(sys.executable).parent / name
    if beside.exists():
        return str(beside)
    return shutil.which("piper")


def _remote_path(voice_id: str) -> str:
    """Map `sv_SE-lisa-medium` to its path within the voices repository."""
    parts = voice_id.split("-")
    if len(parts) < 3:
        return voice_id
    lang, region, quality = parts[0], parts[1], parts[-1]
    name = "-".join(parts[2:-1])
    if name:
        return f"{lang}/{lang}_{region}/{name}/{quality}/{voice_id}"
    return f"{lang}/{lang}_{region}/{quality}/{voice_id}"


def _voice_lang(voice_id: str) -> str:
    """`sv_SE-lisa-medium` and `en_US-amy-low` both give a bare "sv" / "en"."""
    return voice_id.split("-")[0].split("_")[0]


def voice_parts(voice_id: str) -> tuple[str, str]:
    """Split a Piper id into (speaker, quality).

    Piper ids are `{lang}_{REGION}-{speaker}-{quality}`, e.g. `sv_SE-lisa-medium`
    or `en_US-libritts_r-medium`. The speaker can itself contain dashes, so it is
    everything between the language and the trailing quality — reading the last
    segment instead would call every voice after its quality (every Swedish voice
    was named "Medium").
    """
    segments = voice_id.split("-")
    if len(segments) < 3:
        return voice_id, ""
    return "-".join(segments[1:-1]), segments[-1]


class _NothingSynthesized(RuntimeError):
    """The in-process call returned no audio, so the CLI is used instead."""


_warned = False


def _warn_in_process(exc: Exception) -> None:
    """Say it once, on stderr, and carry on with the CLI.

    Once, because this is per-request and a line per sentence would be noise; said
    at all, because falling back silently is how a broken fast path goes unnoticed.
    """
    global _warned
    if not _warned:
        _warned = True
        print(f"[piper] in-process synthesis unavailable ({exc}); using the CLI", file=sys.stderr)


class PiperEngine:
    """Piper voices, synthesized by the CLI."""

    name = "piper"

    def __init__(self, models_dir=None):
        self._models_dir = pathlib.Path(models_dir or config.PIPER_MODELS_DIR)
        # voice id -> (model path, config), and the loaded synthesizers.
        self._voices: dict[str, tuple[pathlib.Path, dict]] = {}
        self._cache: OrderedDict[str, object] = OrderedDict()
        self._voices_in_memory = max(1, VOICES_IN_MEMORY)

    @property
    def languages(self) -> tuple[str, ...]:
        return tuple(sorted({v.lang for v in self.voices()}))

    def available(self) -> bool:
        """Whether this engine can synthesize at all.

        Either path will do: the in-process API is the fast one, and the CLI is
        the fallback. Requiring the CLI would report a working install as
        unavailable.
        """
        return find_piper() is not None or _python_api_available()

    def _model_files(self) -> list[pathlib.Path]:
        if not self._models_dir.is_dir():
            return []
        return sorted(p for p in self._models_dir.glob("*.onnx") if p.is_file())

    def voices(self) -> list[Voice]:
        return [self._describe(p) for p in self._model_files()]

    @staticmethod
    def _describe(model: pathlib.Path) -> Voice:
        voice_id = model.stem
        speaker, quality = voice_parts(voice_id)
        return Voice(
            id=voice_id,
            name=speaker.replace("_", " ").title(),
            lang=_voice_lang(voice_id),
            engine="piper",
            # Piper ids only imply a gender for the hand-recorded voices; the rest
            # are dataset voices and are left as the neutral default.
            gender="f" if speaker.lower() in {"lisa", "emma", "amy", "alba"} else "m",
            note=quality,
        )

    def _ensure_model(self, voice_id: str) -> pathlib.Path:
        """Return the cached model path, downloading it once if needed."""
        self._models_dir.mkdir(parents=True, exist_ok=True)
        model = self._models_dir / f"{voice_id}.onnx"
        if model.exists():
            return model

        remote = f"{VOICES_BASE_URL}/{_remote_path(voice_id)}.onnx"
        try:
            urllib.request.urlretrieve(remote, model)
        except (urllib.error.URLError, urllib.error.HTTPError) as e:
            # Leave nothing half-written behind for the next attempt to trust.
            model.unlink(missing_ok=True)
            raise VoiceNotFound(f"cannot fetch Piper voice {voice_id!r}: {e}") from e

        config_file = self._models_dir / f"{voice_id}.onnx.json"
        if not config_file.exists():
            try:
                urllib.request.urlretrieve(f"{remote}.json", config_file)
            except (urllib.error.URLError, urllib.error.HTTPError):
                # Optional: only the model is required to synthesize.
                config_file.unlink(missing_ok=True)
        return model

    def _load(self, voice_id: str) -> tuple[pathlib.Path, dict]:
        """Resolve a voice to (model path, config), downloading if needed."""
        if voice_id in self._voices:
            return self._voices[voice_id]

        model = self._models_dir / f"{voice_id}.onnx"
        if not model.exists():
            model = self._ensure_model(voice_id)

        settings: dict = {}
        config_file = model.with_suffix(".onnx.json")
        if config_file.exists():
            with open(config_file) as fh:
                settings = json.load(fh)

        self._voices[voice_id] = (model, settings)
        return model, settings

    def _loaded(self, voice_id: str):
        """The synthesizer for *voice_id*, loaded at most once per process.

        Piper's weights are read by whoever synthesizes. The CLI reads them from
        disk on every request, which measured ~600 ms of a ~800 ms request, so an
        ordinary Swedish sentence was mostly model loading. Holding the loaded
        voice turns that into a one-off, and `VOICES_IN_MEMORY` bounds what that
        costs: the least recently used voice is released when a new one arrives.
        """
        voice = self._cache.get(voice_id)
        if voice is not None:
            self._cache.move_to_end(voice_id)
            return voice

        model, _settings = self._load(voice_id)
        voice = _load_piper_voice(str(model))
        self._cache[voice_id] = voice
        while len(self._cache) > self._voices_in_memory:
            evicted, _ = self._cache.popitem(last=False)
            # Dropping the reference is what frees the weights; nothing else
            # holds it once the sentence it was used for is done.
            del evicted
        return voice

    def _synthesize_in_process(self, text: str, voice_id: str) -> tuple[np.ndarray, int]:
        """Synthesize with the loaded voice, without starting a process."""
        voice = self._loaded(voice_id)
        chunks = list(voice.synthesize(text))
        if not chunks:
            # The Python API accepted the call and produced nothing, which is how
            # it behaves when it is called wrongly. Better to say so and use the
            # path that works than to return silence.
            raise _NothingSynthesized(f"piper produced no audio for {voice_id!r}")

        samples = np.concatenate([np.asarray(c.audio_float_array, dtype=np.float32) for c in chunks])
        return samples, int(chunks[0].sample_rate)

    def synthesize(
        self, text: str, voice: str, lang: str = "en", size: str | None = None
    ) -> tuple[np.ndarray, int]:
        # `size` is ignored: a Piper voice is one model file.
        try:
            return self._synthesize_in_process(text, voice)
        except ImportError:
            # No Python API in this install; the CLI is the fallback.
            pass
        except _NothingSynthesized as e:
            _warn_in_process(e)
        return self._synthesize_cli(text, voice)

    def _synthesize_cli(
        self, text: str, voice: str
    ) -> tuple[np.ndarray, int]:
        """Synthesize by running the `piper` CLI.

        Kept as the fallback rather than deleted: it needs no Python API and it is
        what worked before, so an install where the in-process path is unavailable
        still speaks.
        """
        executable = find_piper()
        if executable is None:
            raise EngineUnavailable(
                "the piper CLI is not installed (pip install piper-tts)"
            )
        model, _settings = self._load(voice)

        # The CLI writes a WAV; delete=False plus the finally means it is removed
        # on success, on failure and on timeout alike.
        with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
            tmp_path = tmp.name
        try:
            proc = subprocess.run(
                [executable, "--model", str(model), "--output_file", tmp_path],
                input=text.encode("utf-8"),
                capture_output=True,
                timeout=_CLI_TIMEOUT_S,
            )
            if proc.returncode != 0:
                raise RuntimeError(
                    f"piper failed: {proc.stderr.decode(errors='replace').strip()}"
                )
            audio, sample_rate = sf.read(tmp_path, dtype="float32", always_2d=False)
        finally:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass

        if audio.ndim > 1:
            audio = audio.mean(axis=1)
        return np.asarray(audio, dtype=np.float32), int(sample_rate)
