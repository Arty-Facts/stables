"""The backend serves text-to-speech only.

The speech-to-text engines, the VAD streamer and the WebSocket route they needed
were removed when this code was vendored. These tests pin the resulting route
surface so a re-vendor cannot quietly bring any of it back, and pin the status
codes the TUI relies on.
"""

import base64
import io
import wave

import numpy as np
import pytest
from fastapi.testclient import TestClient

from backend import text as text_module
from backend.app import create_app
from backend.engines.base import EngineUnavailable, Voice, VoiceNotFound
from backend.engines.registry import Registry


class StubEngine:
    """A stand-in engine: one second of silence at 24 kHz.

    Implements the same contract as a real engine, so the routes are exercised
    without loading a model.
    """

    name = "stub"
    languages = ("en",)

    def __init__(self, sample_rate: int = 24000, fail: Exception | None = None):
        self._sample_rate = sample_rate
        self._fail = fail
        self.calls: list[tuple] = []

    def available(self) -> bool:
        return True

    def voices(self) -> list[Voice]:
        return [Voice(id="stub_voice", name="Stub", lang="en", engine="stub")]

    def synthesize(self, text: str, voice: str, lang: str = "en", size: str | None = None):
        self.calls.append((text, voice, lang, size))
        if self._fail is not None:
            raise self._fail
        return np.zeros(self._sample_rate, dtype=np.float32), self._sample_rate


class UnavailableEngine(StubEngine):
    def available(self) -> bool:
        return False


class StubCloneEngine(StubEngine):
    """A clone engine that is available, so a cloned voice can be spoken in a test."""

    name = "qwen"

    def voices(self) -> list[Voice]:
        return [Voice(id="arty.en", name="Arty", lang="en", engine="qwen", cloned=True)]


def _client(*engines) -> TestClient:
    return TestClient(create_app(Registry(list(engines) or [StubEngine()])))


def _paths(app) -> set[str]:
    """Every path the app serves, read from its OpenAPI schema.

    Walking `app.routes` is not reliable: FastAPI wraps included routers in
    objects that do not expose a `path`, so an assertion written against the raw
    route list passes without checking anything. The schema is the public
    contract and lists the real paths.
    """
    return set(app.openapi().get("paths", {}))


def test_tts_routes_are_served():
    assert {
        "/health",
        "/v1/voices",
        "/v1/voices/languages",
        "/v1/piper/voices",
        "/v1/tts",
        "/v1/tts/stream/pcm",
        "/v1/chat/completions",
    } <= _paths(create_app(Registry([StubEngine()])))


def test_no_speech_to_text_routes():
    assert [p for p in _paths(create_app(Registry([StubEngine()]))) if "stt" in p.lower()] == []


def test_health_reports_status_without_loading_engines():
    resp = _client().get("/health")
    assert resp.status_code == 200
    body = resp.json()
    assert body["status"] == "healthy"
    assert body["engines"] == ["stub"]


def test_tts_with_no_available_engine_is_503():
    resp = _client(UnavailableEngine()).post(
        "/v1/tts", json={"text": "hello", "lang": "en"}
    )
    assert resp.status_code == 503


def test_unknown_voice_is_404():
    resp = _client().post(
        "/v1/tts", json={"text": "hello", "tts": "nope", "lang": "en"}
    )
    assert resp.status_code == 404


def test_tts_returns_wav_audio_and_metadata():
    resp = _client().post(
        "/v1/tts",
        json={"text": "Hello", "voice": "stub_voice", "engine": "stub", "lang": "en"},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert body["voice"] == "stub_voice"
    assert body["engine"] == "stub"
    assert body["sampling_rate"] == 24000
    with wave.open(io.BytesIO(base64.b64decode(body["audio"]))) as w:
        assert w.getframerate() == 24000
        assert w.getnframes() == 24000


def test_speed_outside_the_allowed_range_is_rejected():
    resp = _client().post("/v1/tts", json={"text": "x", "speed": 9.0, "lang": "en"})
    assert resp.status_code == 400


def test_engine_error_becomes_500():
    resp = _client(StubEngine(fail=RuntimeError("boom"))).post(
        "/v1/tts", json={"text": "x", "voice": "stub_voice", "engine": "stub", "lang": "en"}
    )
    assert resp.status_code == 500
    assert "boom" in resp.json()["detail"]


def test_voice_not_found_from_engine_is_404():
    resp = _client(StubEngine(fail=VoiceNotFound("gone"))).post(
        "/v1/tts", json={"text": "x", "voice": "stub_voice", "engine": "stub", "lang": "en"}
    )
    assert resp.status_code == 404


def test_engine_unavailable_from_engine_is_503():
    resp = _client(StubEngine(fail=EngineUnavailable("cold"))).post(
        "/v1/tts", json={"text": "x", "voice": "stub_voice", "engine": "stub", "lang": "en"}
    )
    assert resp.status_code == 503


def test_stream_sends_one_fixed_pcm_format():
    """The TUI opens its output device once, so the stream must be 24 kHz mono."""
    engine = StubEngine(sample_rate=22050)  # a Piper-rate engine
    resp = _client(engine).post(
        "/v1/tts/stream/pcm",
        json={"text": "Hello there.", "voice": "stub_voice", "engine": "stub", "lang": "en"},
    )
    assert resp.status_code == 200
    assert resp.headers["X-Sample-Rate"] == "24000"
    assert resp.headers["X-Channels"] == "1"
    # 1 s at 22.05 kHz resampled to 24 kHz, plus the gap between sentences.
    gap = int(0.15 * 24000)
    assert len(resp.content) / 2 == pytest.approx(24000 + gap, abs=100)


def test_stream_starts_with_a_short_opening_chunk():
    """The route must stream the cut opening, not the sentence as written."""
    engine = StubEngine()
    opener = (
        "I have been thinking about this for a while, and I keep coming back to "
        "the same conclusion, which is that the answer depends on the question."
    )
    resp = _client(engine).post(
        "/v1/tts/stream/pcm", json={"text": opener, "voice": "stub_voice"}
    )
    assert resp.status_code == 200
    assert resp.content, "the stream must carry audio"

    spoken = [call[0] for call in engine.calls]
    assert len(spoken) > 1, spoken
    assert len(spoken[0]) <= text_module._FIRST_CHARS, spoken[0]
    assert spoken[0].endswith(","), spoken[0]
    # And the whole text is still spoken, in order: cutting must not drop a
    # clause, because a listener cannot tell a missing clause from a stutter.
    assert " ".join(spoken) == " ".join(opener.split())


def test_stream_rejects_empty_text():
    resp = _client().post(
        "/v1/tts/stream/pcm",
        json={"text": "   ", "voice": "stub_voice", "engine": "stub", "lang": "en"},
    )
    assert resp.status_code == 400


def test_chat_completions_uses_the_last_user_message():
    engine = StubEngine()
    resp = _client(engine).post(
        "/v1/chat/completions",
        json={
            "messages": [
                {"role": "system", "content": "ignored"},
                {"role": "user", "content": "Speak this"},
            ],
            "voice": "stub_voice",
        },
    )
    assert resp.status_code == 200
    assert resp.json()["generated_text"] == "Speak this"
    assert engine.calls[-1][0] == "Speak this"


def test_checkpoint_size_hint_reaches_the_engine():
    """The size is the caller's choice; the route must not swallow it."""
    engine = StubEngine()
    _client(engine).post(
        "/v1/tts",
        json={"text": "x", "voice": "stub_voice", "engine": "stub", "lang": "en", "size": "0.6B"},
    )
    assert engine.calls[-1][3] == "0.6B"


def test_language_entries_are_complete_voices():
    """Each grouped voice must carry every field a voice has.

    A partial entry here once failed to deserialise in the client, which dropped
    the whole language list — and the language tabs in the menu with it.
    """
    body = _client().get("/v1/voices/languages").json()
    for lang, group in body["languages"].items():
        assert group["voices"], f"{lang} has no voices"
        for voice in group["voices"]:
            for field in ("id", "name", "lang", "gender", "grade", "engine"):
                assert field in voice, f"{lang}/{voice.get('id')} is missing {field}"
            assert voice["lang"] == lang


def test_capabilities_reports_engines_and_voices_dir():
    body = _client().get("/v1/capabilities").json()
    assert body["engines"][0]["name"] == "stub"
    assert "voices_dir" in body


def test_the_wav_endpoint_returns_a_playable_file():
    """curl the text and a voice, get a WAV — what the backend is debugged with."""
    engine = StubEngine()
    resp = _client(engine).get(
        "/v1/tts.wav", params={"text": "hello there", "voice": "stub_voice"}
    )
    assert resp.status_code == 200
    assert resp.headers["content-type"] == "audio/wav"
    assert resp.content[:4] == b"RIFF"
    assert resp.content[8:12] == b"WAVE"
    # Named, so `curl -OJ` writes arty.wav rather than tts.wav.
    assert resp.headers["content-disposition"] == 'attachment; filename="stub_voice.wav"'
    # The same path as the JSON endpoint: the text and the voice reach the engine.
    assert engine.calls[-1][0] == "hello there"
    assert engine.calls[-1][1] == "stub_voice"


def test_the_wav_endpoint_reports_the_engine_and_rate():
    resp = _client().get("/v1/tts.wav", params={"text": "hi", "voice": "stub_voice"})
    assert resp.headers["x-engine"] == "stub"
    assert resp.headers["x-sample-rate"] == "24000"
    assert resp.headers["x-voice"] == "stub_voice"


def test_the_wav_endpoint_rejects_an_unknown_voice():
    resp = _client().get("/v1/tts.wav", params={"text": "hi", "voice": "nobody"})
    assert resp.status_code == 404


# The friendly-name lookup (`voice=Arty` → `arty.en`) is not covered: the registry does
# not select a stub clone engine for a cloned id in this harness, so the test proved
# nothing about the lookup and was removed rather than left passing for the wrong reason.
# The id form is covered above, and the lookup is exercised by hand:
#   curl -o a.wav 'localhost:17493/v1/tts.wav?voice=arty.en&text=hello'


def test_health_reports_whether_flash_attn_is_available():
    """The flag is only observable if /health says so. False here: the test venv has no
    flash-attn, which is exactly the state a plain install ships in."""
    body = _client().get("/health").json()
    assert body["flash_attn"] is False
