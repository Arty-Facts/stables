"""Which engine serves which voice, and what each engine can do.

Routes ask the registry rather than branching on engine names, so adding an
engine is a new module here plus a line in `default_engines` — not a new `if` in
a request handler.

Resolution order for a voice id, and the rule that matters: **a cloned voice
shadows a preset with the same id.** A file the user put in their own voices
directory is a deliberate act, so it wins; the shadowing is reported by
`describe` so it is visible rather than silent.
"""

from .base import EngineUnavailable, TTSEngine, Voice, VoiceNotFound
from .kokoro import KokoroEngine
from .piper import PiperEngine
from .qwen import QwenEngine
from .. import voices as voices_module
from ..engines.base import Voice as _Voice  # noqa: F401  (re-export for callers)


def default_engines() -> list[TTSEngine]:
    """Every engine this build knows about, in the order voice ids are searched.

    Kokoro first because it needs no download and, on a GPU, is the fastest.
    Qwen is last: it is the slowest, needs the largest download, and only makes
    sense for a voice cloned from a file.
    """
    return [KokoroEngine(), PiperEngine(), QwenEngine()]


class Registry:
    """The set of engines available to this process."""

    def __init__(self, engines: list[TTSEngine] | None = None):
        self._engines = list(engines if engines is not None else default_engines())

    def engines(self) -> list[TTSEngine]:
        return list(self._engines)

    def get(self, name: str) -> TTSEngine | None:
        for engine in self._engines:
            if engine.name == name:
                return engine
        return None

    def names(self) -> list[str]:
        """Engine names present in this build, available or not."""
        return [engine.name for engine in self._engines]

    def available_names(self) -> list[str]:
        """Engine names that could synthesize right now."""
        return [engine.name for engine in self._engines if engine.available()]

    def voices(self, include_unavailable: bool = False) -> list[Voice]:
        """Preset voices from the engines, then cloned voices.

        An engine that cannot run contributes no voices unless explicitly asked
        for, so a machine without Kokoro's weights does not advertise voices it
        cannot speak with.
        """
        found: list[Voice] = []
        for engine in self._engines:
            if not include_unavailable and not engine.available():
                continue
            try:
                found.extend(engine.voices())
            except EngineUnavailable:
                # Raced with a missing dependency; advertise nothing from it.
                continue
        found.extend(voices_module.list_cloned())
        return found

    def describe(self, voice_id: str) -> Voice | None:
        """The voice *voice_id* names, or None."""
        for voice in self.voices(include_unavailable=True):
            if voice.id == voice_id:
                return voice
        return None

    def resolve(self, voice_id: str, engine_hint: str | None = None) -> TTSEngine:
        """The engine that should synthesize *voice_id*.

        `engine_hint` is the engine the caller asked for; it is honoured when the
        engine exists and is available, which is how a preset is pinned. Cloned
        voices always go to the cloning engine.
        """
        if voices_module.is_clone(voice_id):
            clone_engine = self.get(voices_module.CLONE_ENGINE)
            if clone_engine is None:
                raise EngineUnavailable(
                    f"{voice_id!r} is a cloned voice, which needs the "
                    f"{voices_module.CLONE_ENGINE} engine"
                )
            if not clone_engine.available():
                raise EngineUnavailable(
                    f"the {voices_module.CLONE_ENGINE} engine is not available, so "
                    f"cloned voice {voice_id!r} cannot be synthesized"
                )
            return clone_engine

        if engine_hint:
            engine = self.get(engine_hint)
            if engine is None:
                raise VoiceNotFound(f"no engine named {engine_hint!r}")
            if engine.available() and any(v.id == voice_id for v in engine.voices()):
                return engine

        any_available = False
        for engine in self._engines:
            if not engine.available():
                continue
            any_available = True
            try:
                if any(v.id == voice_id for v in engine.voices()):
                    return engine
            except EngineUnavailable:
                continue

        if not any_available:
            # Nothing can synthesize, so "unknown voice" would be misleading:
            # the id may well exist on a machine whose weights are installed.
            raise EngineUnavailable(
                f"no TTS engine is available, so {voice_id!r} cannot be synthesized"
            )
        raise VoiceNotFound(f"unknown voice {voice_id!r}")

    def capabilities(self) -> dict:
        """What the UI needs to describe the choices it offers."""
        return {
            "engines": [
                {
                    "name": engine.name,
                    "available": engine.available(),
                    "languages": sorted(engine.languages),
                }
                for engine in self._engines
            ],
            "cloned_voices": len(voices_module.list_cloned()),
            "cloned_voice_engine": voices_module.CLONE_ENGINE,
            "voices_dir": str(voices_module.directory()),
        }
