"""Voice listing.

The shapes are the ones the TUI already speaks: a flat list at `/v1/voices`, a
per-language grouping at `/v1/voices/languages`, and the Piper subset at
`/v1/piper/voices`. Every entry carries the engine that can synthesize it, which
is what lets the UI tell the user that a cloned voice is slow on CPU.
"""

from fastapi import APIRouter

from ..engines.base import Voice
from ..engines.registry import Registry


def _as_json(voice: Voice) -> dict:
    return {
        "id": voice.id,
        "name": voice.name,
        "lang": voice.lang,
        "gender": voice.gender,
        "grade": voice.grade,
        "engine": voice.engine,
        "cloned": voice.cloned,
        "note": voice.note,
    }


def router(registry: Registry) -> APIRouter:
    api = APIRouter()

    @api.get("/v1/voices")
    async def list_voices():
        return {"voices": [_as_json(v) for v in registry.voices()]}

    @api.get("/v1/voices/languages")
    async def list_voices_by_language():
        """Voices grouped by language code, in the engine that provides them."""
        languages: dict[str, dict] = {}
        for voice in registry.voices():
            # A clone speaks whatever it is asked to, so it cannot be filed under
            # one language; it appears under its engine instead.
            key = voice.lang
            group = languages.setdefault(key, {"engine": voice.engine, "voices": []})
            # The same shape as /v1/voices. A client that reads these as voices
            # needs every field a voice has: an incomplete entry here failed to
            # deserialise, which took the whole language list — and with it the
            # language tabs in the menu — down with it.
            group["voices"].append(_as_json(voice))
        return {"languages": languages}

    @api.get("/v1/piper/voices")
    async def list_piper_voices():
        piper = registry.get("piper")
        if piper is None:
            return {"voices": []}
        return {
            "voices": [
                {"id": v.id, "name": v.name, "lang": v.lang}
                for v in piper.voices()
            ]
        }

    return api
