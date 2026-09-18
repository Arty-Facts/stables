"""The contract every TTS engine implements.

An engine turns text into audio at its natural rate. `speed` is deliberately not
part of this interface: it is applied once, centrally, as a pitch-preserving
stretch (see `stretch.py`), so every engine gets the same speed behaviour and
none of them has to implement it.

Adding an engine means adding a module here and registering it in `registry.py`
— no route handler changes.
"""

from dataclasses import dataclass
from typing import Protocol, runtime_checkable

import numpy as np


@dataclass(frozen=True)
class Voice:
    """One selectable voice.

    `engine` names the engine that can synthesize it, `lang` is a bare language
    code ("en", "sv"), and `cloned` marks a voice built from a reference WAV in
    the voices directory rather than one shipped by an engine.
    """

    id: str
    name: str
    lang: str
    engine: str
    gender: str = "f"
    grade: str = "B"
    cloned: bool = False
    note: str = ""


@runtime_checkable
class TTSEngine(Protocol):
    """What the registry and the routes require of an engine."""

    #: Stable identifier used in requests and config, e.g. "kokoro".
    name: str
    #: Bare language codes this engine can speak.
    languages: tuple[str, ...]

    def available(self) -> bool:
        """Whether this engine can synthesize right now.

        False means the weights or a dependency are missing. It is not an error:
        an engine that cannot run is simply not offered.
        """

    def voices(self) -> list[Voice]:
        """The voices this engine provides. May be empty."""

    def synthesize(
        self,
        text: str,
        voice: str,
        lang: str = "en",
        size: str | None = None,
    ) -> tuple[np.ndarray, int]:
        """Synthesize *text* with *voice* at the engine's natural rate.

        `size` is a checkpoint hint for engines that have more than one; engines
        with a single checkpoint ignore it.

        Returns `(float audio, sample rate)`. Raises `EngineUnavailable` when the
        engine cannot run and `VoiceNotFound` when it does not know *voice*.
        """


class EngineUnavailable(RuntimeError):
    """The engine cannot synthesize (missing weights, missing dependency)."""


class VoiceNotFound(LookupError):
    """The engine does not provide the requested voice."""
