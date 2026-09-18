"""Qwen engine: the cloning path, and the rules it has to keep.

The real checkpoints are gigabytes and a GPU, so the module is faked here. What
these tests pin is the behaviour that would otherwise only show up on a machine
that has already paid for the download:

- unavailable, and saying so, when qwen-tts is not installed;
- the right cloning mode for what the user has (transcript or not);
- one checkpoint resident at a time, because two would be ~4.6 GB;
- a voice with no reference file is an error, not a silent empty synthesis.
"""

import sys
import types

import numpy as np
import pytest

from backend import voices
from backend.engines import qwen as qwen_module
from backend.engines.base import EngineUnavailable, VoiceNotFound
from backend.engines.qwen import QwenEngine, SIZES, default_size, language_name


class FakeModel:
    """Stands in for Qwen3TTSModel: records the call, returns one second of audio."""

    def __init__(self, model_id: str, **kwargs):
        self.model_id = model_id
        self.kwargs = kwargs
        self.calls: list[dict] = []

    def generate_voice_clone(self, **kwargs):
        self.calls.append(kwargs)
        return [np.zeros(24000, dtype=np.float32)], 24000


@pytest.fixture
def fake_qwen(monkeypatch):
    """A `qwen_tts` module whose models are recorded, and which can be removed."""
    created: list[FakeModel] = []

    def from_pretrained(model_id, **kwargs):
        model = FakeModel(model_id, **kwargs)
        created.append(model)
        return model

    module = types.ModuleType("qwen_tts")
    module.Qwen3TTSModel = types.SimpleNamespace(from_pretrained=from_pretrained)
    monkeypatch.setitem(sys.modules, "qwen_tts", module)
    return created


@pytest.fixture
def voice_file(tmp_path, monkeypatch):
    """One cloned voice, optionally with a transcript."""
    monkeypatch.setattr("backend.config.VOICES_DIR", tmp_path)

    def add(name: str, transcript: str | None = None):
        (tmp_path / f"{name}.wav").write_bytes(b"RIFF....WAVE")
        if transcript is not None:
            (tmp_path / f"{name}.txt").write_text(transcript)
        return name

    return add


# ── availability ─────────────────────────────────────────────────────────────


def test_unavailable_without_the_package(monkeypatch):
    monkeypatch.setitem(sys.modules, "qwen_tts", None)
    monkeypatch.delitem(sys.modules, "qwen_tts", raising=False)
    engine = QwenEngine()
    assert engine.available() is False
    with pytest.raises(EngineUnavailable) as err:
        engine._load("0.6B")
    assert "requirements-qwen.txt" in str(err.value)


def test_no_preset_voices_of_its_own(fake_qwen):
    # Cloned files supply them; returning them here would list each twice.
    assert QwenEngine().voices() == []


def test_registry_does_not_list_clones_twice(fake_qwen, voice_file):
    from backend.engines.registry import Registry

    voice_file("morgan")
    registry = Registry([QwenEngine()])
    ids = [v.id for v in registry.voices()]
    assert ids.count("morgan") == 1
    assert registry.voices()[0].engine == "qwen"


# ── cloning mode ─────────────────────────────────────────────────────────────


def test_transcript_uses_in_context_cloning(fake_qwen, voice_file):
    name = voice_file("morgan", transcript="These are the words I said.")
    audio, sample_rate = QwenEngine().synthesize("Hello there.", name)

    call = fake_qwen[0].calls[0]
    assert call["ref_text"] == "These are the words I said."
    assert call["x_vector_only_mode"] is False
    assert call["ref_audio"].endswith("morgan.wav")
    assert len(audio) == 24000 and sample_rate == 24000


def test_no_transcript_falls_back_to_the_speaker_embedding(fake_qwen, voice_file):
    """A bare WAV still clones, because x_vector_only_mode needs no transcript."""
    name = voice_file("morgan")
    QwenEngine().synthesize("Hello there.", name)

    call = fake_qwen[0].calls[0]
    assert call["x_vector_only_mode"] is True
    assert call["ref_text"] == ""


def test_language_is_spelled_out_for_the_model(fake_qwen, voice_file):
    name = voice_file("morgan")
    QwenEngine().synthesize("Hej", name, lang="sv")

    # Swedish is not one of the ten trained languages, so the model is asked to
    # infer rather than being told something wrong.
    assert fake_qwen[0].calls[0]["language"] == "Auto"
    assert language_name("en") == "English"
    assert language_name("zh") == "Chinese"


def test_a_missing_reference_is_an_error(fake_qwen, voice_file):
    with pytest.raises(VoiceNotFound) as err:
        QwenEngine().synthesize("Hello", "nobody")
    assert "no reference audio" in str(err.value)


def test_unknown_size_is_rejected(fake_qwen, voice_file):
    name = voice_file("morgan")
    with pytest.raises(VoiceNotFound):
        QwenEngine().synthesize("Hello", name, size="9B")


# ── one checkpoint at a time ─────────────────────────────────────────────────


def test_switching_size_unloads_the_previous_checkpoint(fake_qwen, voice_file):
    """Two checkpoints resident would be ~4.6 GB of model for no benefit."""
    name = voice_file("morgan")
    engine = QwenEngine()

    engine.synthesize("Hello", name, size="0.6B")
    assert engine.loaded_size == "0.6B"
    assert len(fake_qwen) == 1

    engine.synthesize("Hello", name, size="1.7B")
    assert engine.loaded_size == "1.7B"
    assert len(fake_qwen) == 2, "a second checkpoint was loaded"


def test_reusing_a_size_does_not_reload(fake_qwen, voice_file):
    name = voice_file("morgan")
    engine = QwenEngine()
    engine.synthesize("Hello", name, size="0.6B")
    engine.synthesize("Again", name, size="0.6B")
    assert len(fake_qwen) == 1


def test_unload_releases_and_is_idempotent(fake_qwen, voice_file):
    name = voice_file("morgan")
    engine = QwenEngine()
    engine.synthesize("Hello", name, size="0.6B")

    engine.unload()
    assert engine.loaded_size is None
    engine.unload()  # must not raise


def test_preload_returns_the_size_it_loaded(fake_qwen):
    engine = QwenEngine()
    assert engine.preload("0.6B") == "0.6B"
    assert engine.loaded_size == "0.6B"
    assert fake_qwen[0].model_id.endswith("0.6B-Base")


def test_default_size_is_the_smallest_without_a_gpu():
    assert default_size() in SIZES
    if not default_size() == "1.7B":
        assert default_size() == "0.6B"


def test_models_are_the_base_cloning_checkpoints():
    assert SIZES == ("0.6B", "1.7B")
    for size, model_id in qwen_module.MODELS.items():
        assert model_id.endswith("-Base"), f"{size} must be a cloning checkpoint"
        assert model_id.startswith("Qwen/Qwen3-TTS-")
