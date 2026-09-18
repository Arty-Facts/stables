"""Synthesis: one shot, streamed PCM, and the chat-completions shim.

The engine is chosen per voice through the registry, so a Swedish Piper voice and
an English Kokoro voice are both just voices here — the routes never branch on
engine names. Speed is applied centrally as a pitch-preserving stretch, which
means every engine behaves the same way for `speed`.

Streaming sends one fixed format — i16 little-endian mono at `STREAM_SAMPLE_RATE`
— because the TUI opens its output device once and plays what arrives. Engines
that synthesize at another rate are resampled, so a Piper voice is not streamed
~9% off-speed.
"""

import asyncio
import base64

import numpy as np
from fastapi import APIRouter, HTTPException
from fastapi.responses import StreamingResponse
from typing import Literal

from pydantic import BaseModel

from .. import config
from .. import text as text_module
from ..audio import audio_to_pcm_i16, audio_to_wav_b64, make_silence_pcm, resample_to
from ..engines.base import EngineUnavailable, TTSEngine, VoiceNotFound
from ..engines.registry import Registry
from ..stretch import release_accelerator_memory, time_stretch_audio

#: The only PCM format the streaming endpoint produces.
STREAM_SAMPLE_RATE = 24000
#: Silence between streamed sentences, so they do not run into each other.
STREAM_GAP_S = 0.15


class TTSRequest(BaseModel):
    text: str
    voice: str = "auto"  # "auto" picks the voice of the detected language
    speed: float = 1.0
    lang: str = "auto"
    size: str | None = None  # checkpoint hint; only engines with variants use it
    #: Stretch quality: how many Griffin-Lim iterations to spend. Higher is
    #: cleaner and slower; omitted means the server picks by its own device.
    quality: Literal["low", "medium", "high"] | None = None


class StreamTTSRequest(TTSRequest):
    pass


class ChatMessage(BaseModel):
    role: str
    content: str


class ChatCompletionRequest(BaseModel):
    messages: list[ChatMessage]
    voice: str = "auto"
    speed: float = 1.0


def _validate_speed(speed: float) -> None:
    if not (config.SPEED_MIN <= speed <= config.SPEED_MAX):
        raise HTTPException(
            status_code=400,
            detail=f"Speed must be between {config.SPEED_MIN} and "
            f"{config.SPEED_MAX}, got {speed}",
        )


def _resolve_language(request: TTSRequest) -> str:
    if request.lang and request.lang != "auto":
        return request.lang
    return text_module.detect_language(request.text)


def _resolve_voice(voice: str, lang: str) -> str:
    if voice and voice != "auto":
        return voice
    return config.language_default(lang)["voice"]


def _engine_for(registry: Registry, voice: str) -> TTSEngine:
    """The engine that can speak *voice*.

    There is no way for a caller to choose one: a voice belongs to exactly one
    engine, so the name would either be redundant or wrong.
    """
    try:
        return registry.resolve(voice, None)
    except VoiceNotFound as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except EngineUnavailable as e:
        raise HTTPException(status_code=503, detail=str(e)) from e


def _synthesize(
    engine: TTSEngine,
    text: str,
    voice: str,
    lang: str,
    speed: float,
    size: str | None = None,
    quality: str | None = None,
) -> tuple[np.ndarray, int]:
    """Synthesize and stretch, with engine failures mapped to HTTP statuses.

    Blocking work: callers run it in a worker thread so the event loop keeps
    serving other requests while a slow engine is thinking.
    """
    try:
        audio, sample_rate = engine.synthesize(text, voice, lang, size)
    except VoiceNotFound as e:
        raise HTTPException(status_code=404, detail=str(e)) from e
    except EngineUnavailable as e:
        raise HTTPException(status_code=503, detail=str(e)) from e
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"{engine.name} error: {e}") from e

    stretched = time_stretch_audio(np.asarray(audio, dtype=np.float32), speed, quality)
    return stretched, int(sample_rate)


def router(registry: Registry) -> APIRouter:
    api = APIRouter()

    @api.post("/v1/tts")
    async def text_to_speech(request: TTSRequest):
        _validate_speed(request.speed)

        lang = _resolve_language(request)
        voice = _resolve_voice(request.voice, lang)
        engine = _engine_for(registry, voice)

        audio, sample_rate = await asyncio.to_thread(
            _synthesize, engine, request.text, voice, lang, request.speed, request.size,
            request.quality
        )
        return {
            "audio": audio_to_wav_b64(audio, sample_rate),
            "text": request.text,
            "voice": voice,
            "lang": lang,
            "speed": request.speed,
            "sampling_rate": sample_rate,
            "duration": len(audio) / sample_rate,
            "engine": engine.name,
        }

    @api.post("/v1/tts/stream/pcm")
    async def stream_tts_pcm(request: StreamTTSRequest):
        """Raw i16 LE PCM at `STREAM_SAMPLE_RATE`, mono."""
        _validate_speed(request.speed)

        lang = _resolve_language(request)
        voice = _resolve_voice(request.voice, lang)
        engine = _engine_for(registry, voice)

        chunks = text_module.split_for_streaming(request.text)
        if not chunks:
            raise HTTPException(status_code=400, detail="no text to speak")

        async def generate():
            try:
                for chunk in chunks:
                    # One sentence at a time: audio starts as soon as the first
                    # is ready, and only one chunk's tensors exist at a time.
                    audio, sample_rate = await asyncio.to_thread(
                        _synthesize, engine, chunk, voice, lang, request.speed, request.size,
                        request.quality
                    )
                    yield audio_to_pcm_i16(
                        resample_to(audio, sample_rate, STREAM_SAMPLE_RATE)
                    )
                    yield make_silence_pcm(STREAM_GAP_S, STREAM_SAMPLE_RATE)
            finally:
                # Runs whether the stream finished or the client disconnected.
                release_accelerator_memory()

        return StreamingResponse(
            generate(),
            media_type="audio/pcm",
            headers={
                "X-Sample-Rate": str(STREAM_SAMPLE_RATE),
                "X-Channels": "1",
                "X-Engine": engine.name,
            },
        )

    @api.post("/v1/chat/completions")
    async def chat_completions(request: ChatCompletionRequest):
        """OpenAI-compatible shape: the last user message becomes audio."""
        text = next((m.content for m in request.messages if m.role == "user"), None)
        if not text:
            raise HTTPException(status_code=400, detail="No user message found")
        result = await text_to_speech(
            TTSRequest(text=text, voice=request.voice, speed=request.speed)
        )
        return {
            "audio": result["audio"],
            "generated_text": text,
            "sampling_rate": result["sampling_rate"],
            "voice": result["voice"],
            "speed": result["speed"],
        }

    return api
