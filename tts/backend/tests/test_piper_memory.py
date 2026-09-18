"""Piper keeps its loaded voices, and that is the difference between 800 and 200 ms.

Loading a Piper voice costs about 600 ms of a short request, and the weights are
read by whoever synthesizes — the CLI re-reads them from disk every time. These
tests pin the in-memory path: loaded once, kept under a bound, and never silent.

The loader is replaced through the engine's own `_load_piper_voice` rather than by
faking the `piper` module in `sys.modules`, which outlives the test and breaks
every test after it.
"""

import numpy as np
import pytest

from backend.engines import piper as piper_module
from backend.engines.piper import PiperEngine


class FakeChunk:
    def __init__(self, samples: int, rate: int = 22050):
        self.audio_float_array = np.full(samples, 0.1, dtype=np.float32)
        self.sample_rate = rate
        self.sample_width = 2
        self.sample_channels = 1


class FakeVoice:
    """Stands in for a loaded PiperVoice."""

    def __init__(self, chunks: int = 1, samples: int = 2205, rate: int = 22050):
        self.chunks = chunks
        self.samples = samples
        self.rate = rate
        self.texts: list[str] = []

    def synthesize(self, text, syn_config=None, include_alignments=False):
        self.texts.append(text)
        return [FakeChunk(self.samples, self.rate) for _ in range(self.chunks)]


class SilentVoice(FakeVoice):
    """What the API does when it is called wrongly: accepts, returns nothing."""

    def synthesize(self, text, syn_config=None, include_alignments=False):
        return []


@pytest.fixture
def voice_dir(tmp_path) -> str:
    model = tmp_path / "sv_SE-test-medium.onnx"
    model.write_bytes(b"\0" * 16)
    (tmp_path / "sv_SE-test-medium.onnx.json").write_text("{}")
    return str(tmp_path)


def _patch_loader(monkeypatch, factory):
    """Replace the loader and count how often it runs."""
    calls: list[str] = []

    def loader(path: str):
        calls.append(path)
        return factory()

    monkeypatch.setattr(piper_module, "_load_piper_voice", loader)
    return calls


def test_the_voice_is_loaded_once_for_many_requests(voice_dir, monkeypatch):
    """The whole point: no cold start per sentence."""
    calls = _patch_loader(monkeypatch, FakeVoice)
    engine = PiperEngine(models_dir=voice_dir)

    for _ in range(3):
        audio, rate = engine.synthesize("Hej", "sv_SE-test-medium")

    assert len(calls) == 1, "the voice must be loaded once, not per request"
    assert len(audio) == 2205 and rate == 22050


def test_the_audio_is_not_silently_empty(voice_dir, monkeypatch):
    """A fast path that returns no audio is worse than a slow one.

    An earlier attempt at the in-process API passed arguments in the wrong
    positions and produced zero samples without complaining, so a length check is
    part of the contract rather than an afterthought.
    """
    _patch_loader(monkeypatch, FakeVoice)
    audio, _rate = PiperEngine(models_dir=voice_dir).synthesize("Hej", "sv_SE-test-medium")
    assert audio.size > 0
    assert np.any(audio != 0)


def test_chunks_are_joined_and_the_rate_is_kept(voice_dir, monkeypatch):
    _patch_loader(monkeypatch, lambda: FakeVoice(chunks=3, samples=1000, rate=16000))
    audio, rate = PiperEngine(models_dir=voice_dir).synthesize("Hej", "sv_SE-test-medium")
    assert len(audio) == 3000, "a sentence arrives as several chunks"
    assert rate == 16000


def test_only_a_bounded_number_of_voices_stay_in_memory(voice_dir, monkeypatch):
    """Each loaded voice is ~63 MB, so this cannot be unbounded."""
    calls = _patch_loader(monkeypatch, FakeVoice)
    engine = PiperEngine(models_dir=voice_dir)
    engine._voices_in_memory = 1
    for name in ("sv_SE-test-medium", "sv_SE-test-medium"):
        engine._voices[name] = (engine._models_dir / f"{name}.onnx", {})

    engine._loaded("sv_SE-test-medium")
    other = engine._models_dir / "sv_SE-other-medium.onnx"
    other.write_bytes(b"\0" * 16)
    engine._voices["sv_SE-other-medium"] = (other, {})
    engine._loaded("sv_SE-other-medium")

    assert len(engine._cache) == 1, "the bound must hold"
    engine._loaded("sv_SE-test-medium")
    assert len(calls) == 3, "the evicted voice is loaded again"
    assert len(engine._cache) == 1


def test_silence_from_the_python_api_falls_back_to_the_cli(voice_dir, monkeypatch, capsys):
    """Fail safe, but say so once rather than silently running the slow path."""
    _patch_loader(monkeypatch, SilentVoice)
    engine = PiperEngine(models_dir=voice_dir)
    monkeypatch.setattr(
        engine, "_synthesize_cli", lambda text, voice: (np.ones(10, dtype=np.float32), 22050)
    )

    audio, _rate = engine.synthesize("Hej", "sv_SE-test-medium")
    assert len(audio) == 10, "the CLI result is what came back"
    assert "in-process synthesis unavailable" in capsys.readouterr().err
