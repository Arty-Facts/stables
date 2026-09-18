"""Nothing belonging to a request may outlive it.

The server is meant to run for weeks without growing: model weights are the only
thing allowed to stay resident, and any file written while serving a request has
to be gone once the response is sent. These tests pin that behaviour.
"""

import os
import subprocess
import wave

import pytest

from backend.engines import piper as piper_module
from backend.engines.piper import PiperEngine, voice_parts


def _write_minimal_wav(path: str, sample_rate: int = 22050, frames: int = 2205) -> None:
    with wave.open(path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(sample_rate)
        w.writeframes(b"\0\0" * frames)


@pytest.fixture
def voice_dir(tmp_path) -> str:
    """A directory holding one already-cached Piper voice."""
    model = tmp_path / "sv_SE-test-medium.onnx"
    model.write_bytes(b"\0" * 1024)
    (tmp_path / "sv_SE-test-medium.onnx.json").write_text("{}")
    return str(tmp_path)


def test_loaded_voice_keeps_a_path_not_the_weights(voice_dir):
    """A 63 MB ONNX file per voice must never be held in memory.

    The CLI reads the model from disk, so caching the bytes here would retain
    tens of megabytes per voice for nothing.
    """
    engine = PiperEngine(models_dir=voice_dir)
    model_path, config = engine._load("sv_SE-test-medium")

    assert isinstance(model_path, __import__("pathlib").Path)
    assert model_path.name == "sv_SE-test-medium.onnx"
    assert config == {}


def test_cleanup_cli_synthesis_removes_its_temp_wav(voice_dir, monkeypatch):
    """The intermediate WAV is deleted once its samples have been read."""
    written: list[str] = []

    def fake_run(cmd, **kwargs):
        out = cmd[cmd.index("--output_file") + 1]
        written.append(out)
        _write_minimal_wav(out)
        return subprocess.CompletedProcess(cmd, 0, b"", b"")

    monkeypatch.setattr(subprocess, "run", fake_run)
    monkeypatch.setattr(piper_module, "find_piper", lambda: "piper")

    audio, sample_rate = PiperEngine(models_dir=voice_dir)._synthesize_cli(
        "Hej", voice="sv_SE-test-medium"
    )

    assert len(audio) == 2205
    assert sample_rate == 22050
    assert written, "expected the CLI to be invoked with an output file"
    assert not os.path.exists(written[0]), "temp WAV left behind on disk"


def test_cleanup_cli_synthesis_removes_its_temp_wav_on_failure(voice_dir, monkeypatch):
    """A failing CLI run must not leak the temp file either."""
    written: list[str] = []

    def failing_run(cmd, **kwargs):
        written.append(cmd[cmd.index("--output_file") + 1])
        return subprocess.CompletedProcess(cmd, 1, b"", b"boom")

    monkeypatch.setattr(subprocess, "run", failing_run)
    monkeypatch.setattr(piper_module, "find_piper", lambda: "piper")

    with pytest.raises(RuntimeError):
        PiperEngine(models_dir=voice_dir)._synthesize_cli("Hej", voice="sv_SE-test-medium")

    assert written and not os.path.exists(written[0]), "temp WAV left behind on failure"


def test_missing_cli_is_reported_as_unavailable(monkeypatch):
    # The CLI is the fallback now, so it is its own path that reports this.
    monkeypatch.setattr(piper_module, "find_piper", lambda: None)
    monkeypatch.setattr(piper_module, "_python_api_available", lambda: False)
    engine = PiperEngine()
    assert engine.available() is False, "neither path is usable"
    monkeypatch.setattr(piper_module, "_python_api_available", lambda: True)
    assert engine.available() is True, "the in-process path is enough on its own"
    with pytest.raises(Exception) as err:
        engine._synthesize_cli("Hej", voice="sv_SE-test-medium")
    assert "piper CLI" in str(err.value)


def test_voice_metadata_is_read_from_the_filename(voice_dir):
    engine = PiperEngine(models_dir=voice_dir)
    voices = engine.voices()
    assert [v.id for v in voices] == ["sv_SE-test-medium"]
    assert voices[0].lang == "sv"
    assert voices[0].engine == "piper"
    # The speaker is the middle segment, not the trailing quality: naming a voice
    # after its quality made every Swedish voice "Medium".
    assert voices[0].name == "Test"
    assert voices[0].note == "medium"


@pytest.mark.parametrize(
    "voice_id,speaker,quality",
    [
        ("sv_SE-lisa-medium", "lisa", "medium"),
        ("en_US-amy-low", "amy", "low"),
        ("en_GB-alba-medium", "alba", "medium"),
        # The speaker can contain dashes and underscores.
        ("en_US-libritts_r-medium", "libritts_r", "medium"),
        ("de_DE-thorsten_emotional-medium", "thorsten_emotional", "medium"),
        # No quality segment to strip.
        ("bare-id", "bare-id", ""),
    ],
)
def test_voice_parts_reads_the_speaker(voice_id, speaker, quality):
    assert voice_parts(voice_id) == (speaker, quality)
